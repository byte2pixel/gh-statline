package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

// fakeClipboard stands in for the system clipboard: it records the text
// and answers with a scripted result, so no test ever touches the real one.
type fakeClipboard struct {
	native bool
	err    error
	text   string
}

func (c *fakeClipboard) write(text string) (bool, error) {
	c.text = text
	return c.native, c.err
}

// pressExport sends the export key and pumps until done reports the app
// has reacted.
func pressExport(t *testing.T, m Model, done func(Model) bool) Model {
	t.Helper()
	model, cmd := m.Update(tea.KeyPressMsg{Code: 'y', Text: "y"})
	return pump(t, model.(Model), cmd, done)
}

// The export key hands the active page's Markdown to the clipboard seam; a
// native copy is confirmed as such.
func TestExportCopiesMarkdownNatively(t *testing.T) {
	deps := testDeps(t)
	clip := &fakeClipboard{native: true}
	deps.Clipboard = clip.write
	m := pressExport(t, New(deps), func(m Model) bool { return m.flash != "" })
	if m.flash != "copied as Markdown" {
		t.Errorf("flash = %q, want the native-copy confirmation", m.flash)
	}
	if !strings.Contains(clip.text, "## testers") || !strings.Contains(clip.text, "| Member |") {
		t.Errorf("clipboard received:\n%s\nwant the team stats table", clip.text)
	}
	if m.err != nil {
		t.Errorf("err = %v, want nil", m.err)
	}
}

// No native clipboard (an SSH session, say): the app falls back to OSC52
// through the terminal and says so.
func TestExportFallsBackToOSC52(t *testing.T) {
	deps := testDeps(t)
	clip := &fakeClipboard{}
	deps.Clipboard = clip.write
	m := pressExport(t, New(deps), func(m Model) bool { return m.flash != "" })
	if m.flash != "copied via terminal (OSC52)" {
		t.Errorf("flash = %q, want the OSC52 fallback note", m.flash)
	}
	if clip.text == "" {
		t.Error("nothing reached the clipboard seam")
	}
}

func TestExportErrorSurfacesInStatus(t *testing.T) {
	deps := testDeps(t)
	clip := &fakeClipboard{err: errors.New("xclip exploded")}
	deps.Clipboard = clip.write
	m := pressExport(t, New(deps), func(m Model) bool { return m.err != nil })
	if m.flash != "" {
		t.Errorf("flash = %q, want none on failure", m.flash)
	}
	if !strings.Contains(m.err.Error(), "xclip exploded") {
		t.Errorf("err = %v, want the clipboard failure", m.err)
	}
}
