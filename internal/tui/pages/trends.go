package pages

import (
	"fmt"
	"image/color"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/components"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// trendCtx carries what every trend card needs to draw itself.
type trendCtx struct {
	d               *metrics.TrendData
	risers, fallers []metrics.Mover // top movers for the grid card
	th              *theme.Theme
}

func (ctx trendCtx) label() lipgloss.Style { return ctx.th.Header }
func (ctx trendCtx) value() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Text)
}
func (ctx trendCtx) faint() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Faint)
}
func (ctx trendCtx) good() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Good)
}
func (ctx trendCtx) bad() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Bad)
}

// markColor maps a metric card's palette slot to a color: the four count
// metrics wear their series-role hues, the latency metrics chrome colors
// (Series has exactly four fixed roles).
func (ctx trendCtx) markColor(slot int) color.Color {
	if slot < len(ctx.th.Series) {
		return ctx.th.Series[slot]
	}
	if slot == len(ctx.th.Series) {
		return ctx.th.Primary
	}
	return ctx.th.Accent
}

// trendCard is one card in the trends grid. Bodies must fit within (w, h).
type trendCard interface {
	key() string
	title() string
	headline(ctx trendCtx) string // pre-styled: carries its own delta colors
	body(ctx trendCtx, w, h int, full bool) string
	export(ctx trendCtx) (headers []string, rows [][]string)
}

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

	cards []trendCard
	focus int
	full  string         // fullscreen card key, "" = grid
	vp    viewport.Model // scrolls the fullscreen card body

	width, height int
}

func NewTrends(th *theme.Theme) *Trends {
	return &Trends{
		theme: th,
		vp:    viewport.New(),
		cards: []trendCard{
			trendCountCard{k: "opened", name: "PRs opened", slot: 0,
				team:   func(t metrics.TeamTrend) []int { return t.Opened },
				member: func(m metrics.MemberTrend) []int { return m.Opened }},
			trendCountCard{k: "merged", name: "Merged", slot: 1,
				team:   func(t metrics.TeamTrend) []int { return t.Merged },
				member: func(m metrics.MemberTrend) []int { return m.Merged }},
			trendCountCard{k: "reviews", name: "Reviews", slot: 2,
				team:   func(t metrics.TeamTrend) []int { return t.Reviews },
				member: func(m metrics.MemberTrend) []int { return m.Reviews }},
			trendCountCard{k: "comments", name: "Comments", slot: 3,
				team:   func(t metrics.TeamTrend) []int { return t.Comments },
				member: func(m metrics.MemberTrend) []int { return m.Comments }},
			trendDurCard{k: "cycle", name: "Cycle p50", slot: 4,
				team: func(t metrics.TeamTrend) []time.Duration { return t.Cycle }},
			trendDurCard{k: "ttfr", name: "TTFR p50", slot: 5,
				team: func(t metrics.TeamTrend) []time.Duration { return t.TTFR }},
			trendMoversCard{},
		},
	}
}

func (t *Trends) SetTheme(th *theme.Theme) { t.theme = th; t.refreshFull() }

func (t *Trends) SetSize(w, h int) {
	t.width, t.height = w, h
	t.refreshFull()
}

// SetData installs a fresh series and derives the top movers from it.
func (t *Trends) SetData(d metrics.TrendData) {
	t.data = d
	t.risers, t.fallers = metrics.Movers(d.Members, 3)
	t.hasData = true
	t.refreshFull()
}

// Reset clears the page while a new team's data loads.
func (t *Trends) Reset() {
	t.data = metrics.TrendData{}
	t.risers, t.fallers = nil, nil
	t.hasData = false
	t.full = ""
}

func (t Trends) Fullscreen() bool { return t.full != "" }

// Export renders the fullscreen card's table or, from the grid, the
// weekly summary series plus the movers. The window is label-only here:
// the trends page ignores it by design.
func (t *Trends) Export(team string, _ metrics.Window) string {
	if title, headers, rows, ok := t.exportTable(); ok {
		return export.Table(title+" — weekly trend", headers, rows)
	}
	return export.Trends(team, t.data, t.risers, t.fallers)
}

// exportTable returns the fullscreen card's data for Markdown export.
func (t Trends) exportTable() (title string, headers []string, rows [][]string, ok bool) {
	if t.full == "" {
		return "", nil, nil, false
	}
	for _, cd := range t.cards {
		if cd.key() == t.full {
			h, r := cd.export(t.ctx())
			return cd.title(), h, r, true
		}
	}
	return "", nil, nil, false
}

// HandleClick resolves a click against the card zones. Only the grid has
// card zones on screen, so fullscreen clicks fall through.
func (t *Trends) HandleClick(msg tea.MouseClickMsg) tea.Cmd {
	if t.Zones == nil || t.Fullscreen() {
		return nil
	}
	for _, cd := range t.cards {
		if t.Zones.Get("trend:" + cd.key()).InBounds(msg) {
			t.clickCard(cd.key())
			break
		}
	}
	return nil
}

// clickCard focuses the clicked card; clicking the focused card expands it.
func (t *Trends) clickCard(key string) {
	for i, cd := range t.cards {
		if cd.key() != key {
			continue
		}
		if t.focus == i && t.full == "" {
			t.openFull(key)
		} else {
			t.focus = i
		}
		return
	}
}

func (t Trends) activeCard() trendCard {
	for _, cd := range t.cards {
		if cd.key() == t.full {
			return cd
		}
	}
	return nil
}

func (t *Trends) openFull(key string) {
	t.full = key
	t.vp.SetYOffset(0)
	t.vp.SetXOffset(0)
	t.refreshFull()
}

// refreshFull re-renders the fullscreen card into the viewport, preserving
// the scroll offset (SetContent clamps it if the content shrank).
func (t *Trends) refreshFull() {
	if t.full == "" || len(t.data.Weeks) == 0 {
		return
	}
	active := t.activeCard()
	if active == nil {
		return
	}
	h := t.height - 2 // title line + scroll indicator line
	if h < 1 {
		h = 1
	}
	t.vp.SetWidth(t.width)
	t.vp.SetHeight(h)
	t.vp.SetContent(active.body(t.ctx(), t.width-2, h, true))
}

// Scroll moves the fullscreen view; wired to the mouse wheel.
func (t *Trends) Scroll(delta int) {
	if t.full == "" {
		return
	}
	if delta < 0 {
		t.vp.ScrollUp(-delta)
	} else {
		t.vp.ScrollDown(delta)
	}
}

// Update receives messages no global handler claimed; the trends page has
// no residual message handling — HandleKey claims everything it reacts to.
func (t *Trends) Update(tea.Msg) tea.Cmd { return nil }

// HandleKey processes grid navigation and fullscreen scrolling.
// handled=false lets the app's global keymap take the key instead.
func (t *Trends) HandleKey(k string) (handled bool) {
	if t.full != "" {
		switch k {
		case "esc", "enter", "f":
			t.full = ""
		case "down", "j":
			t.vp.ScrollDown(1)
		case "up", "k":
			t.vp.ScrollUp(1)
		case "pgdown", "space":
			t.vp.PageDown()
		case "pgup":
			t.vp.PageUp()
		case "d":
			t.vp.HalfPageDown()
		case "u":
			t.vp.HalfPageUp()
		case "g", "home":
			t.vp.SetYOffset(0)
		case "G", "shift+g", "end":
			t.vp.GotoBottom()
		default:
			return false
		}
		return true
	}
	// Grid: two 3-card rows of metrics above the full-width movers card (6).
	switch k {
	case "left", "h":
		if t.focus < 6 && t.focus%3 > 0 {
			t.focus--
		}
	case "right", "l":
		if t.focus < 6 && t.focus%3 < 2 {
			t.focus++
		}
	case "up", "k":
		switch {
		case t.focus == 6:
			t.focus = 4
		case t.focus >= 3:
			t.focus -= 3
		}
	case "down", "j":
		switch {
		case t.focus < 3:
			t.focus += 3
		case t.focus < 6:
			t.focus = 6
		}
	case "enter", "f":
		t.openFull(t.cards[t.focus].key())
	default:
		return false
	}
	return true
}

func (t *Trends) ctx() trendCtx {
	return trendCtx{d: &t.data, risers: t.risers, fallers: t.fallers, th: t.theme}
}

func (t Trends) View() string {
	switch {
	case !t.hasData:
		return t.theme.Header.Render("\n  No data yet — press s to sync.")
	case len(t.data.Weeks) == 0:
		return t.theme.Header.Render("\n  Trends unlock after the first full sync completes.")
	}
	if t.full != "" {
		return t.viewFull()
	}
	return t.viewGrid()
}

func (t Trends) viewFull() string {
	active := t.activeCard()
	if active == nil {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(t.theme.Primary).Render(active.title()) +
		"  " + active.headline(t.ctx()) +
		"   " + t.theme.HelpDesc.Render("esc back · j/k scroll · y copy")
	title = lipgloss.NewStyle().MaxWidth(t.width).Render(title)

	indicator := ""
	if total := t.vp.TotalLineCount(); total > t.vp.Height() {
		top := t.vp.YOffset() + 1
		bottom := min(top+t.vp.VisibleLineCount()-1, total)
		indicator = t.theme.HelpDesc.Render(fmt.Sprintf(
			"  rows %d–%d of %d · %d%%", top, bottom, total, int(t.vp.ScrollPercent()*100)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, t.vp.View(), indicator)
}

const minTrendsH = 12

// gridGeometry fits the coverage line, two metric-card rows, and the movers
// row into exactly t.height. ok=false means the terminal is too short.
func (t Trends) gridGeometry() (rowHs [3]int, ok bool) {
	avail := t.height - 1 // coverage line
	if avail < minTrendsH {
		return rowHs, false
	}
	base, rem := avail/3, avail%3
	for i := range rowHs {
		rowHs[i] = base
		if i < rem {
			rowHs[i]++
		}
		if rowHs[i] > maxCardOuterH {
			rowHs[i] = maxCardOuterH
		}
	}
	return rowHs, true
}

func (t Trends) coverageLine() string {
	n := len(t.data.Weeks)
	s := fmt.Sprintf(" %d weeks · %s → now", n, t.data.Weeks[0].Format("Jan 2"))
	if n < metrics.TrendWeeks {
		s += " · history starts " + t.data.Weeks[0].Format("Jan 2") + ", deepening with each sync"
	}
	return t.theme.HelpDesc.Render(s)
}

func (t Trends) viewGrid() string {
	rowHs, ok := t.gridGeometry()
	if !ok {
		return lipgloss.Place(t.width, t.height, lipgloss.Center, lipgloss.Center,
			t.theme.HelpDesc.Render("terminal too short for the trends grid (need ≥ 13 rows)\nresize, or press 1 for the team view"))
	}
	ctx := t.ctx()

	parts := []string{t.coverageLine()}
	cardOuterW := t.width / 3
	innerW := cardOuterW - 4 // border + padding
	for row := 0; row < 2; row++ {
		innerH := rowHs[row] - 3 // border + title line
		var cells []string
		for col := 0; col < 3; col++ {
			cells = append(cells, t.renderCard(ctx, row*3+col, innerW, innerH))
		}
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	// The movers card takes the whole bottom row: its rows are wide.
	parts = append(parts, t.renderCard(ctx, 6, t.width-4, rowHs[2]-3))
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (t Trends) renderCard(ctx trendCtx, i, innerW, innerH int) string {
	cd := t.cards[i]
	border := t.theme.Faint
	titleStyle := t.theme.Header
	if i == t.focus {
		border = t.theme.Accent
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(t.theme.Accent)
	}
	title := titleStyle.Render(cd.title())
	head := " " + cd.headline(ctx)
	guard := lipgloss.NewStyle().MaxWidth(innerW)
	body := guard.MaxHeight(innerH).Render(cd.body(ctx, innerW, innerH, false))
	if n := strings.Count(body, "\n") + 1; n < innerH {
		body += strings.Repeat("\n", innerH-n)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, guard.Render(title+head), body)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(innerW + 4).
		Render(content)
	if t.Zones != nil {
		box = t.Zones.Mark("trend:"+cd.key(), box)
	}
	return box
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
	team    func(metrics.TeamTrend) []int
	member  func(metrics.MemberTrend) []int
}

func (c trendCountCard) key() string   { return c.k }
func (c trendCountCard) title() string { return c.name }

func (c trendCountCard) headline(ctx trendCtx) string {
	return latestWoW(ctx, c.team(ctx.d.Team))
}

func (c trendCountCard) body(ctx trendCtx, w, h int, full bool) string {
	vals := toFloats(c.team(ctx.d.Team))
	mark := lipgloss.NewStyle().Foreground(ctx.markColor(c.slot))
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
		vals := c.member(m)
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
	vals := c.team(ctx.d.Team)
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
	mark := lipgloss.NewStyle().Foreground(ctx.markColor(c.slot))
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
		ctx.label().Render(components.Pad(m.Metric, 12)) +
		spark +
		ctx.value().Render(fmt.Sprintf("%3d → %-3d", m.Prior, m.Recent)) +
		" " + st.Render(fmt.Sprintf("%5s", m.ChangeLabel())) +
		ctx.th.HelpDesc.Render(badge)
}

// memberMetricSeries finds the weekly series behind a mover's metric.
func memberMetricSeries(ctx trendCtx, m metrics.Mover) []float64 {
	for _, mt := range ctx.d.Members {
		if mt.Login != m.Login {
			continue
		}
		switch m.Metric {
		case "PRs opened":
			return toFloats(mt.Opened)
		case "PRs merged":
			return toFloats(mt.Merged)
		case "reviews":
			return toFloats(mt.Reviews)
		case "comments":
			return toFloats(mt.Comments)
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
			rows = append(rows, []string{m.Arrow(), m.Login, m.Metric,
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
