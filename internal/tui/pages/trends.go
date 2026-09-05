package pages

import (
	"fmt"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Trends is the trajectories page: a grid of cards — every headline metric
// as a fixed weekly mini-trend plus the top movers — each expandable to a
// scrollable fullscreen view (per-member breakdowns for the count metrics).
// It ignores the UI time window: the weekly timescale keeps deltas
// comparable.
type Trends struct {
	theme *theme.Theme
	Zones *zone.Manager

	data            metrics.TrendData
	risers, fallers []metrics.Mover
	hasData         bool

	grid cardGrid[trendCtx]
}

func NewTrends(th *theme.Theme, km keys.KeyMap) *Trends {
	t := &Trends{theme: th}
	// Two 3-card rows of metrics above the movers card, which takes the
	// whole bottom row: its rows are wide.
	t.grid = newCardGrid([]card[trendCtx]{
		trendCountCard{k: "opened", name: "PRs opened", slot: 0, metric: metrics.MetricOpened},
		trendCountCard{k: "merged", name: "Merged", slot: 1, metric: metrics.MetricMerged},
		trendCountCard{k: "reviews", name: "Reviews", slot: 2, metric: metrics.MetricReviews},
		trendCountCard{k: "comments", name: "Comments", slot: 3, metric: metrics.MetricComments},
		trendDurCard{k: "cycle", name: "Cycle p50", slot: 4,
			team: func(t metrics.TeamTrend) []time.Duration { return t.Cycle }},
		trendDurCard{k: "ttfr", name: "TTFR p50", slot: 5,
			team: func(t metrics.TeamTrend) []time.Duration { return t.TTFR }},
		trendMoversCard{},
	}, [][]int{{0, 1, 2}, {3, 4, 5}, {6}}, "trend:", km, th)
	t.grid.ctx = t.ctx
	// Trend headlines carry their own delta colors, so the shell leaves them
	// as they are. The cards index the week series, so fullscreen rendering
	// waits for data.
	t.grid.ready = func() bool { return len(t.data.Weeks) > 0 }
	return t
}

func (t *Trends) SetTheme(th *theme.Theme) {
	t.theme = th
	t.grid.setTheme(th)
}

func (t *Trends) SetSize(w, h int) { t.grid.setSize(w, h) }

// SetData installs a fresh series and derives the top movers from it.
func (t *Trends) SetData(d metrics.TrendData) {
	t.data = d
	t.risers, t.fallers = metrics.Movers(d.Members, 3)
	t.hasData = true
	t.grid.refreshFull()
}

// Reset clears the page while a new team's data loads.
func (t *Trends) Reset() {
	t.data = metrics.TrendData{}
	t.risers, t.fallers = nil, nil
	t.hasData = false
	t.grid.closeFull()
}

func (t *Trends) Fullscreen() bool { return t.grid.fullscreen() }

// Export renders the fullscreen card's table or, from the grid, the
// weekly summary series plus the movers. The window is label-only here:
// the trends page ignores it by design.
func (t *Trends) Export(team string, _ metrics.Window) string {
	if title, headers, rows, ok := t.grid.exportTable(t.ctx()); ok {
		return export.Table(title+" — weekly trend", headers, rows)
	}
	return export.Trends(team, t.data, t.risers, t.fallers)
}

func (t *Trends) HandleClick(msg tea.MouseClickMsg) tea.Cmd {
	return t.grid.handleClick(msg, t.Zones)
}

func (t *Trends) Scroll(delta int) { t.grid.scroll(delta) }

// Update receives messages no global handler claimed; the trends page has
// no residual message handling — HandleKey claims everything it reacts to.
func (t *Trends) Update(tea.Msg) tea.Cmd { return nil }

func (t *Trends) HandleKey(msg tea.KeyPressMsg) bool { return t.grid.handleKey(msg) }

func (t *Trends) ctx() trendCtx {
	return trendCtx{styleCtx: styleCtx{t.theme}, d: &t.data, risers: t.risers, fallers: t.fallers}
}

func (t *Trends) View() string {
	switch {
	case !t.hasData:
		return t.theme.Header.Render("\n  No data yet — press s to sync.")
	case len(t.data.Weeks) == 0:
		return t.theme.Header.Render("\n  Trends unlock after the first full sync completes.")
	case t.grid.fullscreen():
		return t.grid.viewFull(t.ctx())
	}
	return t.viewGrid()
}

const minTrendsH = 12

// gridGeometry fits the coverage line, two metric-card rows, and the movers
// row into exactly the page height. ok=false means the terminal is too short.
func (t *Trends) gridGeometry() (rowHs []int, ok bool) {
	avail := t.grid.height - 1 // coverage line
	if avail < minTrendsH {
		return nil, false
	}
	return splitRows(avail, 3), true
}

func (t *Trends) coverageLine() string {
	n := len(t.data.Weeks)
	s := fmt.Sprintf(" %d weeks · %s → now", n, t.data.Weeks[0].Format("Jan 2"))
	if n < metrics.TrendWeeks {
		s += " · history starts " + t.data.Weeks[0].Format("Jan 2") + ", deepening with each sync"
	}
	return t.theme.HelpDesc.Render(s)
}

func (t *Trends) viewGrid() string {
	rowHs, ok := t.gridGeometry()
	if !ok {
		return lipgloss.Place(t.grid.width, t.grid.height, lipgloss.Center, lipgloss.Center,
			t.theme.HelpDesc.Render("terminal too short for the trends grid (need ≥ 13 rows)\nresize, or press 1 for the team view"))
	}
	parts := append([]string{t.coverageLine()}, t.grid.gridRows(t.ctx(), rowHs, t.Zones)...)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}
