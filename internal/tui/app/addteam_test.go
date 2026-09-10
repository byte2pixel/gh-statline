package app

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
	"github.com/byte2pixel/gh-statline/internal/tui/wizard"
)

var freshTeam = config.Team{
	Name: "fresh", Org: "acme",
	Members: []config.Member{{Login: "carol"}},
	Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
}

// a in the team switcher opens the wizard over the page, sized to the
// content area, so the frame still fits the terminal.
func TestSwitcherAddOpensWizard(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	model, _ := m.Update(tea.KeyPressMsg{Code: 't', Text: "t"})
	m = model.(Model)
	model, cmd := m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("a in the switcher produced no message")
	}
	msg := cmd()
	if _, ok := msg.(overlays.TeamAddMsg); !ok {
		t.Fatalf("a emitted %#v, want TeamAddMsg", msg)
	}
	model, _ = m.Update(msg)
	m = model.(Model)
	if m.overlay != overlayWizard {
		t.Fatalf("overlay = %d, want the wizard", m.overlay)
	}
	frame := m.View().Content
	if got := lipgloss.Height(frame); got != 30 {
		t.Errorf("frame is %d rows with the wizard open, terminal is 30", got)
	}
	if v := stripANSI(frame); !strings.Contains(v, "Statline setup") {
		t.Errorf("wizard not rendered in the frame:\n%s", v)
	}
}

// Keys go to the wizard while it is open: q is a letter there, not quit.
// ctrl+c still quits the app, as it does under every modal.
func TestWizardKeepsTheAppKeys(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	model, _ := m.openWizard()
	model, _ = model.(Model).Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = model.(Model)
	if m.quitting || m.overlay != overlayWizard {
		t.Fatalf("q under the wizard: quitting=%v overlay=%d", m.quitting, m.overlay)
	}
	model, _ = m.Update(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})
	if !model.(Model).quitting {
		t.Error("ctrl+c under the wizard did not quit")
	}
}

// A finished wizard appends the team, saves, and switches to it; the same
// save writes default_team, so one file write covers both.
func TestWizardDoneAddsAndSwitches(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	model, _ := m.openWizard()
	team := freshTeam
	model, cmd := model.(Model).Update(wizard.DoneMsg{Team: &team})
	m = model.(Model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if m.overlay != overlayNone {
		t.Errorf("overlay = %d after the wizard finished, want none", m.overlay)
	}
	if m.deps.Team.Name != "fresh" || len(m.deps.Cfg.Teams) != 2 {
		t.Errorf("active team %q among %d, want fresh among 2", m.deps.Team.Name, len(m.deps.Cfg.Teams))
	}
	saved := loadSaved(t)
	if saved.DefaultTeam != "fresh" || len(saved.Teams) != 2 || saved.Teams[1].Name != "fresh" {
		t.Errorf("saved default_team=%q teams=%d, want fresh as the second of two", saved.DefaultTeam, len(saved.Teams))
	}
	if m.flash != "added team fresh" {
		t.Errorf("flash = %q, want added team fresh", m.flash)
	}
	// Let the new team's sync finish before the test closes the database.
	pump(t, m, cmd, func(m Model) bool { return !m.syncing })
}

// Backing out of the wizard returns to the switcher and writes nothing.
func TestWizardCancelledReturnsToSwitcher(t *testing.T) {
	model, _ := New(testDeps(t)).openWizard()
	model, _ = model.(Model).Update(wizard.DoneMsg{})
	if got := model.(Model).overlay; got != overlayTeam {
		t.Errorf("overlay = %d after backing out, want the switcher", got)
	}
	assertNoConfig(t)
}

// The wizard's own failure, an auth or viewer query error, lands in the
// status bar once the overlay is gone.
func TestWizardErrorSurfaces(t *testing.T) {
	model, _ := New(testDeps(t)).openWizard()
	model, _ = model.(Model).Update(wizard.DoneMsg{Err: errors.New("no GitHub credentials found")})
	m := model.(Model)
	if m.overlay != overlayNone {
		t.Errorf("overlay = %d after a wizard failure, want none", m.overlay)
	}
	if m.err == nil || !strings.Contains(m.err.Error(), "credentials") {
		t.Errorf("err = %v, want the wizard's failure", m.err)
	}
}
