package app

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
)

// Mouse input enters the app as a click or wheel message and is resolved
// against the zones the last frame marked: the header tabs first, then the
// active page. These drive that path from the app's own zone manager,
// so a click lands wherever the real frame put the zone.

// waitZone blocks until the zone manager knows id. Scan hands zone bounds
// to a worker goroutine, so Get can lag the frame that marked them.
func waitZone(t *testing.T, z *zone.Manager, id string) *zone.ZoneInfo {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if zi := z.Get(id); !zi.IsZero() {
			return zi
		}
		if time.Now().After(deadline) {
			t.Fatalf("zone %q never resolved", id)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// clickZone renders the current frame, which is what teaches the zone
// manager this frame's bounds, and returns a left click on the first cell
// of the named zone.
func clickZone(t *testing.T, m Model, id string) tea.MouseClickMsg {
	t.Helper()
	_ = m.View()
	zi := waitZone(t, m.z, id)
	return tea.MouseClickMsg{X: zi.StartX, Y: zi.StartY, Button: tea.MouseLeft}
}

// threeWeekTrends is enough series for every trend card to draw. Trends
// load on sync completion, which these tests never run, so the page gets
// its data directly.
func threeWeekTrends() metrics.TrendData {
	day := func(n int) time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n) }
	return metrics.TrendData{
		Weeks: []time.Time{day(0), day(7), day(14)},
		Team: metrics.TeamTrend{
			Opened: []int{3, 5, 4}, Merged: []int{2, 4, 3}, Reviews: []int{7, 9, 8}, Comments: []int{1, 0, 2},
			Cycle: []time.Duration{2 * time.Hour, 0, 45 * time.Minute},
			TTFR:  []time.Duration{30 * time.Minute, 0, time.Hour},
		},
		Members: []metrics.MemberTrend{{
			Login: "alice", Opened: []int{1, 2, 2}, Merged: []int{1, 1, 1},
			Reviews: []int{2, 3, 3}, Comments: []int{0, 0, 1},
		}},
	}
}

// A left click on a header tab switches the route, from any tab to any
// other. A click that hits no zone at all changes nothing.
func TestClickOnTabSwitchesRoute(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	for _, tc := range []struct {
		id   string
		want route
	}{
		{"tab:charts", routeCharts}, {"tab:trends", routeTrends}, {"tab:team", routeTeam},
	} {
		model, cmd := m.Update(clickZone(t, m, tc.id))
		m = model.(Model)
		if m.nav.cur != tc.want {
			t.Errorf("click on %s: route = %d, want %d", tc.id, m.nav.cur, tc.want)
		}
		if cmd != nil {
			t.Errorf("click on %s returned a command", tc.id)
		}
	}
	// The bottom-right corner is the help footer: no tab, no page zone.
	model, cmd := m.Update(tea.MouseClickMsg{X: 109, Y: 29, Button: tea.MouseLeft})
	if m = model.(Model); m.nav.cur != routeTeam || cmd != nil {
		t.Error("a click on empty space changed the route or produced a command")
	}
}

// Only the left button navigates.
func TestRightClickIsIgnored(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	msg := clickZone(t, m, "tab:charts")
	msg.Button = tea.MouseRight
	model, _ := m.Update(msg)
	if got := model.(Model).nav.cur; got != routeTeam {
		t.Errorf("right click on a tab switched the route to %d", got)
	}
}

// An overlay owns the screen: clicks and wheel events under it reach
// neither the tabs nor the page.
func TestMouseUnderOverlayIsIgnored(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	msg := clickZone(t, m, "tab:charts")
	first := m.teamStats.SelectedLogin()
	m.openSwitcher()

	model, _ := m.Update(msg)
	m = model.(Model)
	if m.nav.cur != routeTeam {
		t.Errorf("a tab click under the team switcher switched the route to %d", m.nav.cur)
	}
	model, _ = m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	m = model.(Model)
	if got := m.teamStats.SelectedLogin(); got != first {
		t.Errorf("the wheel under the team switcher moved the selection to %q", got)
	}
	if m.overlay != overlayTeam {
		t.Error("mouse input closed the overlay")
	}
}

// The wheel scrolls the active page: on the team table that is the
// selection, down to the other member and back.
func TestWheelMovesTeamSelection(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	first := m.teamStats.SelectedLogin()

	model, _ := m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelDown})
	m = model.(Model)
	if got := m.teamStats.SelectedLogin(); got == first || got == "" {
		t.Fatalf("wheel down: selection = %q, still %q", got, first)
	}
	model, _ = m.Update(tea.MouseWheelMsg{X: 5, Y: 5, Button: tea.MouseWheelUp})
	m = model.(Model)
	if got := m.teamStats.SelectedLogin(); got != first {
		t.Errorf("wheel up: selection = %q, want %q", got, first)
	}
}

// A click on a member row goes through the router to the team page, which
// asks the app to open that member, like enter on their row. The person
// frame then renders through the app.
func TestClickOnMemberRowOpensPerson(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	model, cmd := m.Update(clickZone(t, m, "row:bob"))
	m = model.(Model)
	if cmd == nil {
		t.Fatal("a member-row click produced no command")
	}
	m = pump(t, m, cmd, func(m Model) bool { return m.nav.cur == routePerson })
	if m.person.Login != "bob" {
		t.Errorf("drilled into %q, want bob", m.person.Login)
	}
	if v := stripANSI(m.View().Content); !strings.Contains(v, "bob") || !strings.Contains(v, "activity (PRs + reviews per day)") {
		t.Errorf("the person frame did not render through the app:\n%s", v)
	}
}

// Chart cards focus on the first click and expand on the second, resolved
// through the app's zone manager rather than one built by hand.
func TestClickOnChartCardFocusesThenExpands(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	model, _ := m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = model.(Model)

	msg := clickZone(t, m, "card:cycle")
	model, _ = m.Update(msg)
	m = model.(Model)
	if m.charts.Focus() != 2 || m.charts.Fullscreen() {
		t.Fatalf("first click: focus = %d, fullscreen = %v; want 2, false", m.charts.Focus(), m.charts.Fullscreen())
	}
	model, _ = m.Update(msg)
	m = model.(Model)
	if !m.charts.Fullscreen() {
		t.Error("second click did not expand the focused card")
	}
}

// Trend cards take clicks the same way.
func TestClickOnTrendCardFocusesThenExpands(t *testing.T) {
	m := teamWithRows(t, testDeps(t))
	m.trends.SetData(threeWeekTrends())
	model, _ := m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	m = model.(Model)

	msg := clickZone(t, m, "trend:merged")
	model, _ = m.Update(msg)
	m = model.(Model)
	if m.trends.Focus() != 1 || m.trends.Fullscreen() {
		t.Fatalf("first click: focus = %d, fullscreen = %v; want 1, false", m.trends.Focus(), m.trends.Fullscreen())
	}
	model, _ = m.Update(msg)
	m = model.(Model)
	if !m.trends.Fullscreen() {
		t.Error("second click did not expand the focused card")
	}
}
