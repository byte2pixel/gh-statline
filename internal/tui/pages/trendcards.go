package pages

import (
	"fmt"
	"sort"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/components"
)

// trendCtx carries what every trend card needs to draw itself.
type trendCtx struct {
	styleCtx
	d               *metrics.TrendData
	risers, fallers []metrics.Mover // top movers for the grid card
}

// ---- shared card helpers ----

// trendAxis is the bottom line under a weekly chart: first-week label left,
// "now" pinned right.
func trendAxis(ctx trendCtx, w int) string {
	left := ctx.d.Weeks[0].Format("Jan 2")
	const right = "now"
	if pad := w - len(left) - len(right); pad > 0 {
		return ctx.faint().Render(left + strings.Repeat(" ", pad) + right)
	}
	return ctx.faint().Render(left)
}

// trendChart renders a weekly column chart (or a 1-line sparkline when
// there's no vertical room) with the axis underneath.
func trendChart(ctx trendCtx, vals []float64, w, h int, mark lipgloss.Style) []string {
	var max float64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	if h <= 2 {
		return []string{components.Sparkrow(vals, max, mark, ctx.faint())}
	}
	chartW := min(w, 3*len(vals)) // cap column width at 2+gap so bars stay dense
	rows := components.ColumnChart(vals, max, chartW, h-1, mark, ctx.faint())
	return append(rows, trendAxis(ctx, chartW))
}

// latestWoW formats "latest  delta-vs-last-week" for a count series.
func latestWoW(ctx trendCtx, vals []int) string {
	n := len(vals)
	prev := 0
	if n >= 2 {
		prev = vals[n-2]
	}
	d, st := deltaCount(ctx.th, vals[n-1], prev, n >= 2)
	return lipgloss.NewStyle().Bold(true).Foreground(ctx.th.Primary).Render(fmt.Sprintf("%d", vals[n-1])) +
		" this wk " + st.Render(d)
}

// ---- count metric card ----

type trendCountCard struct {
	k, name string
	slot    int
	metric  metrics.Metric
}

func (c trendCountCard) key() string   { return c.k }
func (c trendCountCard) title() string { return c.name }

func (c trendCountCard) headline(ctx trendCtx) string {
	return latestWoW(ctx, c.metric.OfTeam(ctx.d.Team))
}

func (c trendCountCard) body(ctx trendCtx, w, h int, full bool) string {
	vals := toFloats(c.metric.OfTeam(ctx.d.Team))
	mark := ctx.mark(c.slot)
	if !full {
		return strings.Join(trendChart(ctx, vals, w, h, mark), "\n")
	}

	half := len(ctx.d.Weeks) / 2
	if half > 4 {
		half = 4
	}
	lines := trendChart(ctx, vals, w, 7, mark)
	label := "per member · latest week"
	if half > 0 {
		label = fmt.Sprintf("per member · last %dw and change vs the prior %dw", half, half)
	}
	lines = append(lines, "", ctx.label().Render(label))
	lines = append(lines, c.memberRows(ctx, w, half, mark)...)
	return strings.Join(lines, "\n")
}

// memberRows renders one sparkline row per member, busiest first, with the
// same recent-half vs prior-half comparison the movers use (week-over-week
// would flag every quiet week as ▼100%). Members with no activity across
// the series are summarized, not listed.
func (c trendCountCard) memberRows(ctx trendCtx, w, half int, mark lipgloss.Style) []string {
	type row struct {
		login string
		vals  []int
		total int
	}
	var rows []row
	quiet := 0
	loginW := 6
	for _, m := range ctx.d.Members {
		vals := c.metric.Of(m)
		total := 0
		for _, v := range vals {
			total += v
		}
		if total == 0 {
			quiet++
			continue
		}
		rows = append(rows, row{m.Login, vals, total})
		if len(m.Login) > loginW {
			loginW = len(m.Login)
		}
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].total != rows[j].total {
			return rows[i].total > rows[j].total
		}
		return rows[i].login < rows[j].login
	})

	n := len(ctx.d.Weeks)
	cellW := 1
	if w >= loginW+2+2*n+14 {
		cellW = 2
	}
	out := make([]string, 0, len(rows)+1)
	for _, r := range rows {
		vals := toFloats(r.vals)
		if cellW == 2 {
			vals = doubleCells(vals)
		}
		var max float64
		for _, v := range vals {
			if v > max {
				max = v
			}
		}
		recent, prior := 0, 0
		for i := n - half; i < n; i++ {
			recent += r.vals[i]
		}
		for i := n - 2*half; i < n-half; i++ {
			prior += r.vals[i]
		}
		display, hasPrior := recent, half > 0 && n >= 2*half
		if half == 0 {
			display = r.vals[n-1]
		}
		d, st := deltaCount(ctx.th, recent, prior, hasPrior)
		out = append(out, ctx.label().Render(components.Pad(r.login, loginW+2))+
			components.Sparkrow(vals, max, mark, ctx.faint())+
			ctx.value().Render(fmt.Sprintf("%4d", display))+
			"  "+st.Render(d))
	}
	if quiet > 0 {
		out = append(out, ctx.faint().Render(fmt.Sprintf("(%d members with no %s)", quiet, strings.ToLower(c.name))))
	}
	return out
}

func (c trendCountCard) export(ctx trendCtx) ([]string, [][]string) {
	headers := []string{"Week", c.name}
	vals := c.metric.OfTeam(ctx.d.Team)
	rows := make([][]string, len(vals))
	for i, v := range vals {
		rows[i] = []string{ctx.d.Weeks[i].Format("2006-01-02"), fmt.Sprintf("%d", v)}
	}
	return headers, rows
}

// ---- duration metric card ----

type trendDurCard struct {
	k, name string
	slot    int
	team    func(metrics.TeamTrend) []time.Duration
}

func (c trendDurCard) key() string   { return c.k }
func (c trendDurCard) title() string { return c.name }

func (c trendDurCard) headline(ctx trendCtx) string {
	vals := c.team(ctx.d.Team)
	n := len(vals)
	var prev time.Duration
	if n >= 2 {
		prev = vals[n-2]
	}
	latest := "–"
	if vals[n-1] > 0 {
		latest = metrics.FmtDur(vals[n-1])
	}
	d, st := deltaDur(ctx.th, vals[n-1], prev, n >= 2)
	return lipgloss.NewStyle().Bold(true).Foreground(ctx.th.Primary).Render(latest) +
		" this wk " + st.Render(d)
}

func (c trendDurCard) body(ctx trendCtx, w, h int, full bool) string {
	durs := c.team(ctx.d.Team)
	vals := make([]float64, len(durs))
	for i, d := range durs {
		vals[i] = d.Seconds()
	}
	mark := ctx.mark(c.slot)
	if !full {
		return strings.Join(trendChart(ctx, vals, w, h, mark), "\n")
	}

	// Fullscreen: one labeled bar per week — exact values, oldest first.
	var max float64
	for _, v := range vals {
		if v > max {
			max = v
		}
	}
	const labelW = 6
	barW := w - labelW - 9
	if barW < 8 {
		barW = 8
	}
	lines := make([]string, 0, len(durs))
	for i, d := range durs {
		display := "–"
		if d > 0 {
			display = metrics.FmtDur(d)
		}
		lines = append(lines, components.BarRow(ctx.d.Weeks[i].Format("Jan 02"), labelW,
			vals[i], max, barW, display, mark, ctx.label(), ctx.value()))
	}
	lines = append(lines, "", ctx.faint().Render("weekly p50 · missing weeks had no samples"))
	return strings.Join(lines, "\n")
}

func (c trendDurCard) export(ctx trendCtx) ([]string, [][]string) {
	headers := []string{"Week", c.name}
	vals := c.team(ctx.d.Team)
	rows := make([][]string, len(vals))
	for i, v := range vals {
		display := "–"
		if v > 0 {
			display = metrics.FmtDur(v)
		}
		rows[i] = []string{ctx.d.Weeks[i].Format("2006-01-02"), display}
	}
	return headers, rows
}

// ---- movers card ----

type trendMoversCard struct{}

func (trendMoversCard) key() string   { return "movers" }
func (trendMoversCard) title() string { return "Movers" }

func (trendMoversCard) headline(ctx trendCtx) string {
	weeks := len(ctx.d.Weeks)
	if weeks < 4 {
		return ctx.th.HelpDesc.Render("unlock at 4 weeks of history")
	}
	h := weeks / 2
	if h > 4 {
		h = 4
	}
	return ctx.th.HelpDesc.Render(fmt.Sprintf("last %dw vs prior %dw · risers and fallers", h, h))
}

// moverLine formats one mover; withSpark adds the member's weekly series
// for the moved metric (fullscreen only).
func moverLine(ctx trendCtx, m metrics.Mover, loginW int, withSpark bool) string {
	st := ctx.good()
	if !m.Rising() {
		st = ctx.bad()
	}
	badge := ""
	if s := m.StreakLabel(); s != "" {
		badge = "  " + s
	}
	spark := ""
	if withSpark {
		if vals := memberMetricSeries(ctx, m); vals != nil {
			var max float64
			for _, v := range vals {
				if v > max {
					max = v
				}
			}
			spark = components.Sparkrow(vals, max, st, ctx.faint()) + "  "
		}
	}
	return st.Render(m.Arrow()) + " " +
		ctx.value().Render(components.Pad(m.Login, loginW+2)) +
		ctx.label().Render(components.Pad(m.Metric.String(), 12)) +
		spark +
		ctx.value().Render(fmt.Sprintf("%3d → %-3d", m.Prior, m.Recent)) +
		" " + st.Render(fmt.Sprintf("%5s", m.ChangeLabel())) +
		ctx.th.HelpDesc.Render(badge)
}

// memberMetricSeries finds the weekly series behind a mover's metric.
func memberMetricSeries(ctx trendCtx, m metrics.Mover) []float64 {
	for _, mt := range ctx.d.Members {
		if mt.Login == m.Login {
			return toFloats(m.Metric.Of(mt))
		}
	}
	return nil
}

func (trendMoversCard) body(ctx trendCtx, w, h int, full bool) string {
	if len(ctx.d.Weeks) < 4 {
		return ctx.faint().Render("movers unlock at 4 weeks of history")
	}

	risers, fallers := ctx.risers, ctx.fallers
	if full {
		risers, fallers = metrics.Movers(ctx.d.Members, 0)
	}
	if len(risers)+len(fallers) == 0 {
		return ctx.faint().Render("no significant movers (volume floors not met)")
	}
	loginW := 6
	for _, m := range append(append([]metrics.Mover{}, risers...), fallers...) {
		if len(m.Login) > loginW {
			loginW = len(m.Login)
		}
	}

	if full {
		lines := []string{ctx.good().Render("▲ Risers")}
		for _, m := range risers {
			lines = append(lines, moverLine(ctx, m, loginW, true))
		}
		if len(risers) == 0 {
			lines = append(lines, ctx.faint().Render("none"))
		}
		lines = append(lines, "", ctx.bad().Render("▼ Fallers"))
		for _, m := range fallers {
			lines = append(lines, moverLine(ctx, m, loginW, true))
		}
		if len(fallers) == 0 {
			lines = append(lines, ctx.faint().Render("none"))
		}
		lines = append(lines, "", ctx.faint().Render(
			"ranked by percent change, new activity first, with per-metric volume floors · one metric per member per direction"))
		return strings.Join(lines, "\n")
	}

	// Grid: risers left, fallers right, side by side.
	colW := w / 2
	rows := max(len(risers), len(fallers))
	if rows > h {
		rows = h
	}
	lines := make([]string, 0, rows)
	for i := 0; i < rows; i++ {
		left, right := "", ""
		if i < len(risers) {
			left = moverLine(ctx, risers[i], loginW, false)
		}
		if i < len(fallers) {
			right = moverLine(ctx, fallers[i], loginW, false)
		}
		pad := colW - lipgloss.Width(left)
		if pad < 2 {
			pad = 2
		}
		lines = append(lines, left+strings.Repeat(" ", pad)+right)
	}
	return strings.Join(lines, "\n")
}

func (trendMoversCard) export(ctx trendCtx) ([]string, [][]string) {
	headers := []string{"", "Member", "Metric", "Prior", "Recent", "Change", "Streak"}
	risers, fallers := metrics.Movers(ctx.d.Members, 0)
	var rows [][]string
	for _, list := range [][]metrics.Mover{risers, fallers} {
		for _, m := range list {
			rows = append(rows, []string{m.Arrow(), m.Login, m.Metric.String(),
				fmt.Sprintf("%d", m.Prior), fmt.Sprintf("%d", m.Recent), m.ChangeLabel(), m.StreakLabel()})
		}
	}
	return headers, rows
}

func toFloats(vals []int) []float64 {
	out := make([]float64, len(vals))
	for i, v := range vals {
		out[i] = float64(v)
	}
	return out
}

func doubleCells(vals []float64) []float64 {
	out := make([]float64, 0, 2*len(vals))
	for _, v := range vals {
		out = append(out, v, v)
	}
	return out
}
