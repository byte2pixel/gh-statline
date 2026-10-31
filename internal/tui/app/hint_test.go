package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/tui/pages"
)

// Changing the sort column puts that column's definition in the status bar
// and arms the flash timer to take it down again (#121). A flash still
// wins while both are set: it confirms something the user just did.
func TestSortChangeShowsTheColumnDefinition(t *testing.T) {
	m := New(testDeps(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = model.(Model)

	model, cmd := m.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("sort key change returned no command")
	}
	msg, ok := cmd().(pages.SortChangedMsg)
	if !ok || msg.Key != "reviews" {
		t.Fatalf("sort key change emitted %#v, want SortChangedMsg{reviews}", msg)
	}
	model, cmd = m.Update(msg)
	m = model.(Model)
	if cmd == nil {
		t.Fatal("the sort change armed no timer to clear the definition")
	}
	want := export.TeamColumnDoc("reviews_given")
	if !strings.Contains(m.statusLine(), want) {
		t.Errorf("status line = %q, want the reviews definition %q", m.statusLine(), want)
	}
	if strings.Contains(m.statusLine(), "✓") {
		t.Errorf("status line = %q: a definition is not a confirmation and gets no tick", m.statusLine())
	}

	m.flash = "copied as Markdown"
	if !strings.Contains(m.statusLine(), "copied") {
		t.Errorf("status line = %q, want the flash in front of the definition", m.statusLine())
	}
	m.flash = ""

	model, _ = m.Update(clearFlashMsg{})
	m = model.(Model)
	if strings.Contains(m.statusLine(), want) {
		t.Errorf("status line = %q still shows the definition after the timer", m.statusLine())
	}
}
