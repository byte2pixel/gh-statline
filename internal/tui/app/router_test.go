package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

func TestRouterCycleVisitsTabsAndWraps(t *testing.T) {
	r := newRouter(keys.Default())
	for i, want := range []route{routeCharts, routeTrends, routeTeam, routeCharts} {
		r.cycle()
		if r.cur != want {
			t.Fatalf("cycle %d: cur = %d, want %d", i, r.cur, want)
		}
	}
}

// The person drill-down is a detail view of the team tab: it lights that
// tab and cycles onward from it, into charts.
func TestRouterPersonBelongsToTeamTab(t *testing.T) {
	r := newRouter(keys.Default())
	r.cur = routePerson
	if r.litTab() != routeTeam {
		t.Error("person drill-down must light the Team tab")
	}
	r.cycle()
	if r.cur != routeCharts {
		t.Errorf("cycle from person: cur = %d, want charts", r.cur)
	}
}

func TestRouterJumpKeys(t *testing.T) {
	r := newRouter(keys.Default())
	for _, tc := range []struct {
		key  rune
		want route
	}{{'2', routeCharts}, {'3', routeTrends}, {'1', routeTeam}} {
		if !r.jumpKey(tea.KeyPressMsg{Code: tc.key, Text: string(tc.key)}) {
			t.Fatalf("jump %q not claimed", tc.key)
		}
		if r.cur != tc.want {
			t.Errorf("jump %q: cur = %d, want %d", tc.key, r.cur, tc.want)
		}
	}
	if r.jumpKey(tea.KeyPressMsg{Code: 'x', Text: "x"}) {
		t.Error("x must not be claimed as a tab jump")
	}
}

// A non-tab route has to return where it was opened from, and re-entering
// the route already open must not make it its own return target.
func TestRouterEnterAndBack(t *testing.T) {
	r := newRouter(keys.Default())
	r.cur = routeTrends

	r.enter(routeSyncStatus)
	if r.cur != routeSyncStatus || r.prev != routeTrends {
		t.Fatalf("enter: cur = %d, prev = %d; want sync status returning to trends", r.cur, r.prev)
	}
	r.enter(routeSyncStatus)
	if r.prev != routeTrends {
		t.Errorf("re-enter clobbered prev: %d", r.prev)
	}
	r.back()
	if r.cur != routeTrends {
		t.Errorf("back: cur = %d, want trends", r.cur)
	}
}

// A team switch invalidates the person drill-down, so home has to forget a
// prev pointing at it rather than leave esc aimed at another team's member.
func TestRouterHomeForgetsTheReturnRoute(t *testing.T) {
	r := newRouter(keys.Default())
	r.cur, r.prev = routeSyncStatus, routePerson
	r.home()
	if r.cur != routeTeam || r.prev != routeTeam {
		t.Errorf("home: cur = %d, prev = %d; want both at the team tab", r.cur, r.prev)
	}
}

// Sync status is not a tab: it lights none of them, and it is not in the
// cycle. Tab out of it lands on the tab after the team page, since litTab
// falls through to the first entry.
func TestRouterSyncStatusIsNotATab(t *testing.T) {
	r := newRouter(keys.Default())
	r.cur = routeSyncStatus
	if r.litTab() == routeTeam || r.litTab() == routeCharts || r.litTab() == routeTrends {
		t.Errorf("sync status lit a tab: %d", r.litTab())
	}
	for _, tab := range r.tabs {
		if tab.route == routeSyncStatus {
			t.Error("sync status must not appear in the tab strip")
		}
	}
	// Tab has to do something: a route outside the strip cycles back into
	// it rather than swallowing the key.
	r.cycle()
	if r.cur != routeTeam {
		t.Errorf("cycle from sync status: cur = %d, want the first tab", r.cur)
	}
}

// Tab clicks resolve against the zones the rendered strip marked. A click
// outside every tab is declined, so the page underneath can have it.
func TestRouterClickTab(t *testing.T) {
	r := newRouter(keys.Default())
	th := theme.New(true)
	z := zone.New()
	z.Scan(r.renderTabs(&th, z))

	for _, tc := range []struct {
		id   string
		want route
	}{
		{"tab:trends", routeTrends}, {"tab:charts", routeCharts}, {"tab:team", routeTeam},
	} {
		zi := waitZone(t, z, tc.id)
		msg := tea.MouseClickMsg{X: zi.EndX, Y: zi.EndY, Button: tea.MouseLeft}
		if !r.clickTab(msg, z) {
			t.Fatalf("click on %s not claimed", tc.id)
		}
		if r.cur != tc.want {
			t.Errorf("click on %s: cur = %d, want %d", tc.id, r.cur, tc.want)
		}
	}
	if r.clickTab(tea.MouseClickMsg{X: 200, Y: 5, Button: tea.MouseLeft}, z) {
		t.Error("a click off the strip was claimed as a tab")
	}
}
