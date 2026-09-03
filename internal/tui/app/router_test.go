package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/keys"
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
