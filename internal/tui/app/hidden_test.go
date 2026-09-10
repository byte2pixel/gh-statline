package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
	"github.com/byte2pixel/gh-statline/internal/tui/pages"
)

var keyMembers = tea.KeyPressMsg{Code: 'm', Text: "m"}

// hiddenInDB reads the cache's copy of the flag, the one the metrics use.
func hiddenInDB(t *testing.T, deps Deps, login string) int {
	t.Helper()
	var n int
	if err := deps.DB.QueryRow(`SELECT hidden FROM team_members WHERE team_id = ? AND login = ?`,
		deps.TeamID, login).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// savedHidden reads the flag back from the config the app wrote.
func savedHidden(t *testing.T, login string) bool {
	t.Helper()
	for _, m := range loadSaved(t).Teams[0].Members {
		if m.Login == login {
			return m.Hidden
		}
	}
	t.Fatalf("%s is missing from the saved config", login)
	return false
}

// Applying the picker writes hidden: to the config, flips the cache's copy
// of the flag, and reloads: bob leaves the table.
func TestMembersChosenHidesAndPersists(t *testing.T) {
	deps := testDeps(t)
	m := teamWithRows(t, deps)
	if v := stripANSI(m.teamStats.View()); !strings.Contains(v, "bob") {
		t.Fatalf("bob should be in the table before hiding:\n%s", v)
	}

	model, cmd := m.Update(overlays.MembersChosenMsg{Hidden: map[string]bool{"alice": false, "bob": true}})
	m = model.(Model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if !savedHidden(t, "bob") || savedHidden(t, "alice") {
		t.Error("saved config does not hide bob alone")
	}
	if hiddenInDB(t, deps, "bob") != 1 {
		t.Error("the cache's member row still shows bob")
	}
	if !m.deps.Team.Members[1].Hidden {
		t.Error("the live team profile was not updated")
	}
	if m.flash != "1 of 2 members hidden" {
		t.Errorf("flash = %q, want 1 of 2 members hidden", m.flash)
	}
	pump(t, m, cmd, func(m Model) bool { return !strings.Contains(stripANSI(m.teamStats.View()), "bob") })
}

// Showing a hidden member again is the same path in reverse.
func TestMembersChosenShowsAgain(t *testing.T) {
	deps := testDeps(t)
	deps.Team.Members[1].Hidden = true // shares its backing array with deps.Cfg
	if _, _, err := deps.Store.MirrorTeam(deps.Team); err != nil {
		t.Fatal(err)
	}
	model, _ := New(deps).Update(tea.WindowSizeMsg{Width: 110, Height: 30}) // the table renders only once sized
	model, cmd := model.(Model).Update(overlays.MembersChosenMsg{Hidden: map[string]bool{"alice": false, "bob": false}})
	m := model.(Model)
	if m.err != nil {
		t.Fatal(m.err)
	}
	if savedHidden(t, "bob") || hiddenInDB(t, deps, "bob") != 0 {
		t.Error("bob is still hidden in the config or the cache")
	}
	if m.flash != "0 of 2 members hidden" {
		t.Errorf("flash = %q, want 0 of 2 members hidden", m.flash)
	}
	pump(t, m, cmd, func(m Model) bool { return strings.Contains(stripANSI(m.teamStats.View()), "bob") })
}

// Applying the picker unchanged writes nothing: an untouched file keeps
// its comments.
func TestMembersChosenUnchangedWritesNothing(t *testing.T) {
	m := New(testDeps(t))
	if _, cmd := m.Update(overlays.MembersChosenMsg{Hidden: map[string]bool{"alice": false, "bob": false}}); cmd != nil {
		t.Error("an unchanged selection should not reload anything")
	}
	assertNoConfig(t)
}

// A save failure surfaces in the status bar, with no flash in front of it;
// the change still applies to the session.
func TestMembersChosenSurfacesSaveFailure(t *testing.T) {
	m := New(testDeps(t))
	t.Setenv("STATLINE_CONFIG", t.TempDir()) // a directory makes the rename fail
	model, _ := m.Update(overlays.MembersChosenMsg{Hidden: map[string]bool{"bob": true}})
	m2 := model.(Model)
	if m2.err == nil {
		t.Fatal("config-save failure was not surfaced")
	}
	if m2.flash != "" {
		t.Errorf("flash = %q would hide the error", m2.flash)
	}
	if !m2.deps.Team.Members[1].Hidden {
		t.Error("the in-memory change was dropped along with the save")
	}
}

// m opens the picker on the table's selected row, inside the frame; esc
// closes it and changes nothing.
func TestMembersKeyOpensPickerOnSelectedRow(t *testing.T) {
	m := teamWithRows(t, testDeps(t)) // merged desc: alice's one merged PR puts her first
	model, _ := m.Update(keyMembers)
	m = model.(Model)
	if m.overlay != overlayMembers {
		t.Fatalf("overlay = %d, want the member picker", m.overlay)
	}
	frame := m.View().Content
	if got := lipgloss.Height(frame); got != 30 {
		t.Errorf("frame is %d rows with the picker open, terminal is 30", got)
	}
	if v := stripANSI(frame); !strings.Contains(v, "▸ [x] alice") || !strings.Contains(v, "[x] bob") {
		t.Errorf("picker should open on alice with both shown:\n%s", v)
	}
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("esc under the picker produced no message")
	}
	model, _ = m.Update(cmd())
	if m = model.(Model); m.overlay != overlayNone {
		t.Errorf("overlay = %d after esc, want closed", m.overlay)
	}
	assertNoConfig(t)
}

// Hiding the member whose drill-down is open returns to the team table:
// their page would otherwise show numbers no other view counts.
func TestHidingDrilledMemberReturnsToTeam(t *testing.T) {
	m := New(testDeps(t))
	model, cmd := m.Update(pages.MemberChosenMsg{Login: "bob"})
	m = pump(t, model.(Model), cmd, func(m Model) bool { return m.nav.cur == routePerson })
	model, _ = m.Update(overlays.MembersChosenMsg{Hidden: map[string]bool{"bob": true}})
	if got := model.(Model).nav.cur; got != routeTeam {
		t.Errorf("route = %d after hiding the drilled member, want team", got)
	}
}
