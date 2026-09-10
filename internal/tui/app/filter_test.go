package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
)

// teamWithRows sizes the app and waits for the team table to fill.
func teamWithRows(t *testing.T, deps Deps) Model {
	t.Helper()
	m := New(deps)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = model.(Model)
	return pump(t, m, m.loadData(), func(m Model) bool { return m.teamStats.SelectedLogin() != "" })
}

// Typing a login after / narrows the table, and enter on the narrowed row
// opens that member: the page claims the typed keys ahead of the global
// keymap, then hands enter back to it.
func TestFilterThenEnterDrillsIntoShownMember(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	press := func(k tea.KeyPressMsg) tea.Cmd {
		model, cmd := m.Update(k)
		m = model.(Model)
		return cmd
	}
	press(tea.KeyPressMsg{Code: '/', Text: "/"})
	press(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if v := stripANSI(m.View().Content); !strings.Contains(v, "/b▌") || !strings.Contains(v, "1 of 2 match") {
		t.Fatalf("query line missing from the frame:\n%s", v)
	}
	press(tea.KeyPressMsg{Code: tea.KeyEnter}) // keeps the filter
	cmd := press(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("enter on the narrowed row did not load the member")
	}
	m = pump(t, m, cmd, func(m Model) bool { return m.nav.cur == routePerson })
	if m.person.Login != "bob" {
		t.Errorf("drilled into %q, want bob, the one row the filter kept", m.person.Login)
	}
}

// While typing, q is a letter. The global quit must wait for enter or esc.
func TestFilterTypingDoesNotQuit(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	for _, k := range []tea.KeyPressMsg{{Code: '/', Text: "/"}, {Code: 'q', Text: "q"}} {
		model, _ := m.Update(k)
		m = model.(Model)
	}
	if m.quitting {
		t.Fatal("q while typing a login quit the app")
	}
	if got := m.teamStats.Query(); got != "q" {
		t.Errorf("query = %q, want q", got)
	}
}

// A team switch drops the filter: it was typed against the old team's
// logins.
func TestTeamSwitchClearsFilter(t *testing.T) {
	deps := testDeps(t)
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
	})
	m := teamWithRows(t, deps)
	for _, k := range []tea.KeyPressMsg{{Code: '/', Text: "/"}, {Code: 'a', Text: "a"}, {Code: tea.KeyEnter}} {
		model, _ := m.Update(k)
		m = model.(Model)
	}
	if m.teamStats.Query() != "a" {
		t.Fatal("the filter did not take before the switch")
	}
	model, _ := m.activateTeam("others")
	if got := model.(Model).teamStats.Query(); got != "" {
		t.Errorf("query = %q after a team switch, want none", got)
	}
}
