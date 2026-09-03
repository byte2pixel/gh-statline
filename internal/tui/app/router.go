package app

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// tabDef is one header tab: the route it activates, its rendered label,
// the zone id its clicks resolve through, and the numeric key that jumps
// straight to it.
type tabDef struct {
	route  route
	label  string
	zoneID string
	jump   key.Binding
}

// router owns the route state and everything derived from the tab list:
// cycling, numeric jumps, header rendering, and tab click hit-tests.
// Adding a tabbed page is one tabDef entry; drill-down routes (person)
// are not tabs and only need a litTab mapping.
type router struct {
	cur  route
	tabs []tabDef
}

func newRouter(km keys.KeyMap) router {
	return router{tabs: []tabDef{
		{route: routeTeam, label: "1 Team", zoneID: "tab:team", jump: km.TeamStats},
		{route: routeCharts, label: "2 Charts", zoneID: "tab:charts", jump: km.Charts},
		{route: routeTrends, label: "3 Trends", zoneID: "tab:trends", jump: km.Trends},
	}}
}

// litTab is the header tab the current route lights up: the person
// drill-down keeps the Team tab lit — it's a detail view of that tab.
func (r router) litTab() route {
	if r.cur == routePerson {
		return routeTeam
	}
	return r.cur
}

// cycle advances to the tab after the lit one, wrapping at the end; the
// person drill-down therefore cycles into charts, as before.
func (r *router) cycle() {
	lit := r.litTab()
	for i, t := range r.tabs {
		if t.route == lit {
			r.cur = r.tabs[(i+1)%len(r.tabs)].route
			return
		}
	}
}

// jumpKey routes the numeric tab shortcuts; false leaves the key to the
// rest of the global keymap.
func (r *router) jumpKey(msg tea.KeyPressMsg) bool {
	for _, t := range r.tabs {
		if key.Matches(msg, t.jump) {
			r.cur = t.route
			return true
		}
	}
	return false
}

// clickTab resolves a click against the header tab zones.
func (r *router) clickTab(msg tea.MouseClickMsg, z *zone.Manager) bool {
	for _, t := range r.tabs {
		if z.Get(t.zoneID).InBounds(msg) {
			r.cur = t.route
			return true
		}
	}
	return false
}

// renderTabs draws the header tab strip and marks each tab's click zone.
func (r router) renderTabs(th *theme.Theme, z *zone.Manager) string {
	sep := th.Header.Render(" │ ")
	var b strings.Builder
	for i, t := range r.tabs {
		if i > 0 {
			b.WriteString(sep)
		}
		style := th.Header
		if t.route == r.litTab() {
			style = lipgloss.NewStyle().Bold(true).Foreground(th.Accent)
		}
		b.WriteString(z.Mark(t.zoneID, style.Render(t.label)))
	}
	return b.String()
}
