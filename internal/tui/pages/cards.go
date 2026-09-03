package pages

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
	"github.com/byte2pixel/gh-statline/internal/tui/components"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// renderCtx carries what every card needs to draw itself.
type renderCtx struct {
	d    *ChartData
	th   *theme.Theme
	grow float64 // 0..1 spring progress applied to bar lengths
}

// card is one chart in the grid. Bodies must fit exactly within (w, h).
type card interface {
	key() string
	title() string
	headline(ctx renderCtx) string
	body(ctx renderCtx, w, h int, full bool) string
	export(ctx renderCtx) (headers []string, rows [][]string)
}

// Style shorthands: labels/values wear text tokens, marks wear series color.
func (ctx renderCtx) mark(i int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Series[i])
}
func (ctx renderCtx) label() lipgloss.Style { return ctx.th.Header }
func (ctx renderCtx) value() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Text)
}
func (ctx renderCtx) faint() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(ctx.th.Faint)
}

func (ctx renderCtx) legend(parts ...string) string {
	var b strings.Builder
	for i := 0; i+1 < len(parts); i += 2 {
		if i > 0 {
			b.WriteString("  ")
		}
		idx, _ := strconv.Atoi(parts[i])
		b.WriteString(ctx.mark(idx).Render("█") + " " + ctx.th.HelpDesc.Render(parts[i+1]))
	}
	return b.String()
}

// memberLabelW sizes the login column for member-keyed bar cards.
func memberLabelW(rows []metrics.Row) int {
	w := 6
	for _, r := range rows {
		if len(r.Login) > w {
			w = len(r.Login)
		}
	}
	if w > 14 {
		w = 14
	}
	return w
}

func clip(lines []string, h int) string {
	if len(lines) > h {
		lines = lines[:h]
	}
	return strings.Join(lines, "\n")
}

// finish joins all lines in fullscreen — the viewport scrolls any overflow —
// and clips to the preview height in the grid.
func finish(lines []string, h int, full bool) string {
	if full {
		return strings.Join(lines, "\n")
	}
	return clip(lines, h)
}

// ── PR throughput ────────────────────────────────────────────────────────

type throughputCard struct{}

func (throughputCard) key() string   { return "throughput" }
func (throughputCard) title() string { return "PR throughput" }

func (throughputCard) headline(ctx renderCtx) string {
	var o, m int
	for _, b := range ctx.d.Buckets {
		o += b.Opened
		m += b.Merged
	}
	return fmt.Sprintf("%d opened · %d merged · per %s", o, m, bucketLabel(ctx.d.BucketDur))
}

// bucketLabel names a throughput bucket duration for chart labels.
func bucketLabel(d time.Duration) string {
	switch {
	case d <= 0 || d == 24*time.Hour:
		return "day"
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d == 7*24*time.Hour:
		return "week"
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (t throughputCard) series(ctx renderCtx) (opened, merged []float64, max float64) {
	for _, b := range ctx.d.Buckets {
		opened = append(opened, float64(b.Opened)*ctx.grow)
		merged = append(merged, float64(b.Merged)*ctx.grow)
		if f := float64(b.Opened); f > max {
			max = f
		}
		if f := float64(b.Merged); f > max {
			max = f
		}
	}
	return opened, merged, max
}

func (t throughputCard) axis(ctx renderCtx, w int) string {
	bs := ctx.d.Buckets
	if len(bs) < 2 {
		return ""
	}
	first := bs[0].Day.Format("01/02")
	last := bs[len(bs)-1].Day.Format("01/02")
	gap := w - len(first) - len(last)
	if gap < 1 {
		gap = 1
	}
	return ctx.th.HelpDesc.Render(first + strings.Repeat(" ", gap) + last)
}

func (t throughputCard) body(ctx renderCtx, w, h int, full bool) string {
	opened, merged, max := t.series(ctx)
	legend := ctx.legend("0", "opened", "1", "merged")
	if !full {
		sparkO := components.Sparkrow(opened, max, ctx.mark(0), ctx.faint())
		sparkM := components.Sparkrow(merged, max, ctx.mark(1), ctx.faint())
		// Shortest previews keep the sparklines and shed chrome first.
		var lines []string
		switch {
		case h >= 4:
			lines = []string{legend, sparkO, sparkM, t.axis(ctx, len(opened))}
		case h == 3:
			lines = []string{sparkO, sparkM, t.axis(ctx, len(opened))}
		case h == 2:
			lines = []string{sparkO, sparkM}
		default:
			lines = []string{sparkO}
		}
		return clip(lines, h)
	}
	colH := (h - 4) / 2
	if colH < 3 {
		colH = 3
	}
	maxLbl := ctx.th.HelpDesc.Render(fmt.Sprintf("max %d/%s", int(max), bucketLabel(ctx.d.BucketDur)))
	colsW := w - 2
	var lines []string
	lines = append(lines, legend+"   "+maxLbl)
	lines = append(lines, components.ColumnChart(opened, max, colsW, colH, ctx.mark(0), ctx.faint())...)
	lines = append(lines, components.ColumnChart(merged, max, colsW, colH, ctx.mark(1), ctx.faint())...)
	axisW := len(opened)
	if len(opened) > 0 && colsW/len(opened) >= 2 {
		per := colsW / len(opened)
		if per > 4 {
			per = 4
		}
		axisW = len(opened) * per
	}
	lines = append(lines, t.axis(ctx, axisW))
	return clip(lines, h)
}

func (throughputCard) export(ctx renderCtx) ([]string, [][]string) {
	rows := make([][]string, len(ctx.d.Buckets))
	for i, b := range ctx.d.Buckets {
		rows[i] = []string{b.Day.Format("2006-01-02"), strconv.Itoa(b.Opened), strconv.Itoa(b.Merged)}
	}
	return []string{"Period", "Opened", "Merged"}, rows
}

// ── Review outcomes ──────────────────────────────────────────────────────

type outcomesCard struct{}

func (outcomesCard) key() string   { return "outcomes" }
func (outcomesCard) title() string { return "Review outcomes" }

func (outcomesCard) headline(ctx renderCtx) string {
	total := 0
	for _, r := range ctx.d.Rows {
		total += r.ReviewsGiven
	}
	return fmt.Sprintf("%d reviews", total)
}

func (o outcomesCard) reviewers(ctx renderCtx) []metrics.Row {
	var rs []metrics.Row
	for _, r := range ctx.d.Rows {
		if r.ReviewsGiven > 0 {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool { return rs[i].ReviewsGiven > rs[j].ReviewsGiven })
	return rs
}

func (o outcomesCard) body(ctx renderCtx, w, h int, full bool) string {
	rs := o.reviewers(ctx)
	if len(rs) == 0 {
		return ctx.th.HelpDesc.Render("no reviews in this window")
	}
	var lines []string
	if full || h >= 3 { // tiny previews spend every line on reviewers
		lines = append(lines, ctx.legend("1", "approved", "0", "commented", "2", "changes"))
	}
	labelW := memberLabelW(rs)
	max := float64(rs[0].ReviewsGiven)
	valueW := 3
	if full {
		valueW = 14
	}
	barW := w - labelW - valueW - 2
	if barW < 5 {
		barW = 5
	}
	for _, r := range rs {
		if !full && len(lines) >= h {
			break
		}
		display := strconv.Itoa(r.ReviewsGiven)
		if full {
			display = fmt.Sprintf("%d (%d/%d/%d)", r.ReviewsGiven, r.Approved, r.Commented, r.ChangesReq)
		}
		lines = append(lines, components.StackedRow(r.Login, labelW,
			[]components.Seg{
				{Value: float64(r.Approved) * ctx.grow, Style: ctx.mark(1)},
				{Value: float64(r.Commented) * ctx.grow, Style: ctx.mark(0)},
				{Value: float64(r.ChangesReq) * ctx.grow, Style: ctx.mark(2)},
			}, max, barW, display, ctx.label(), ctx.value()))
	}
	return finish(lines, h, full)
}

func (o outcomesCard) export(ctx renderCtx) ([]string, [][]string) {
	var rows [][]string
	for _, r := range o.reviewers(ctx) {
		rows = append(rows, []string{r.Login, strconv.Itoa(r.Approved),
			strconv.Itoa(r.Commented), strconv.Itoa(r.ChangesReq), strconv.Itoa(r.ReviewsGiven)})
	}
	return []string{"Member", "Approved", "Commented", "Changes req.", "Total"}, rows
}

// ── Cycle-time trend ─────────────────────────────────────────────────────

type cycleCard struct{}

func (cycleCard) key() string   { return "cycle" }
func (cycleCard) title() string { return "Cycle time" }

func (cycleCard) headline(ctx renderCtx) string {
	for i := len(ctx.d.Trend) - 1; i >= 0; i-- {
		if p := ctx.d.Trend[i]; p.Merged > 0 {
			return "latest wk p50 " + metrics.FmtDur(p.Median)
		}
	}
	return "no merges in window"
}

func (c cycleCard) body(ctx renderCtx, w, h int, full bool) string {
	var max float64
	for _, p := range ctx.d.Trend {
		if f := p.Median.Seconds(); f > max {
			max = f
		}
	}
	if max == 0 {
		return ctx.th.HelpDesc.Render("no merges in this window")
	}
	labelW := 5 // "01/02"
	valueW := 6
	if full {
		valueW = 14
	}
	barW := w - labelW - valueW - 2
	if barW < 5 {
		barW = 5
	}
	var lines []string
	trend := ctx.d.Trend
	if !full && len(trend) > h {
		trend = trend[len(trend)-h:]
	}
	for _, p := range trend {
		if !full && len(lines) >= h {
			break
		}
		display := metrics.FmtDur(p.Median)
		if p.Merged == 0 {
			display = "–"
		}
		if full && p.Merged > 0 {
			display = fmt.Sprintf("%s (%d merged)", metrics.FmtDur(p.Median), p.Merged)
		}
		lines = append(lines, components.BarRow(p.WeekStart.Format("01/02"), labelW,
			p.Median.Seconds()*ctx.grow, max, barW, display, ctx.mark(1), ctx.label(), ctx.value()))
	}
	return finish(lines, h, full)
}

func (cycleCard) export(ctx renderCtx) ([]string, [][]string) {
	var rows [][]string
	for _, p := range ctx.d.Trend {
		rows = append(rows, []string{p.WeekStart.Format("2006-01-02"),
			metrics.FmtDur(p.Median), strconv.Itoa(p.Merged)})
	}
	return []string{"Week", "Median cycle", "Merged"}, rows
}

// ── Review matrix ────────────────────────────────────────────────────────

type matrixCard struct{}

func (matrixCard) key() string   { return "matrix" }
func (matrixCard) title() string { return "Who reviews whom" }

func (matrixCard) headline(ctx renderCtx) string {
	pairs := 0
	for _, row := range ctx.d.Matrix.Counts {
		for _, n := range row {
			if n > 0 {
				pairs++
			}
		}
	}
	return fmt.Sprintf("%d active pair(s)", pairs)
}

func (m matrixCard) body(ctx renderCtx, w, h int, full bool) string {
	mx := ctx.d.Matrix
	if len(mx.Logins) == 0 {
		return ""
	}
	labelW := 6
	if full {
		labelW = memberLabelW(rowsFromLogins(mx.Logins))
	}
	cellW := 4
	cols := len(mx.Authors)
	if !full { // fullscreen renders every column; the viewport pans h/l
		maxCols := (w - labelW - 1) / cellW
		if cols > maxCols {
			cols = maxCols
		}
	}

	// Header: author short names across the top; "oth" is the non-member column.
	head := ctx.th.HelpDesc.Render(components.Pad("rev↓", labelW))
	for a := 0; a < cols; a++ {
		name := mx.Authors[a]
		if name == "(others)" {
			name = "oth"
		}
		head += " " + ctx.th.HelpDesc.Render(components.Pad(shortLogin(name, cellW-1), cellW-1))
	}
	lines := []string{head}

	for r := 0; r < len(mx.Logins) && (full || len(lines) < h); r++ {
		line := ctx.label().Render(components.Pad(shortLogin(mx.Logins[r], labelW), labelW))
		for a := 0; a < cols; a++ {
			if r == a {
				line += " " + ctx.faint().Render(components.Pad("  ·", cellW-1))
				continue
			}
			n := mx.Counts[r][a]
			cell := components.Pad(fmt.Sprintf("%3d", n), cellW-1)
			line += " " + heatStyle(ctx.th, n, mx.Max).Render(cell)
		}
		lines = append(lines, line)
	}
	if cols < len(mx.Authors) && len(lines) < h {
		lines = append(lines, ctx.th.HelpDesc.Render(
			fmt.Sprintf("… %d more author column(s) in fullscreen", len(mx.Authors)-cols)))
	}
	return finish(lines, h, full)
}

// pinnedGrid renders the fullscreen matrix window: the header row (authors
// from colOff) plus visRows reviewer rows from rowOff. The caller keeps the
// header and label column pinned by re-rendering on every offset change.
func (m matrixCard) pinnedGrid(ctx renderCtx, rowOff, colOff, visRows, visCols int) []string {
	mx := ctx.d.Matrix
	if len(mx.Logins) == 0 {
		return nil
	}
	labelW := memberLabelW(rowsFromLogins(mx.Logins))
	cellW := 4
	endCol := min(colOff+visCols, len(mx.Authors))
	endRow := min(rowOff+visRows, len(mx.Logins))

	head := ctx.th.HelpDesc.Render(components.Pad("rev↓", labelW))
	for a := colOff; a < endCol; a++ {
		name := mx.Authors[a]
		if name == "(others)" {
			name = "oth"
		}
		head += " " + ctx.th.HelpDesc.Render(components.Pad(shortLogin(name, cellW-1), cellW-1))
	}
	lines := []string{head}

	for r := rowOff; r < endRow; r++ {
		line := ctx.label().Render(components.Pad(shortLogin(mx.Logins[r], labelW), labelW))
		for a := colOff; a < endCol; a++ {
			if r == a {
				line += " " + ctx.faint().Render(components.Pad("  ·", cellW-1))
				continue
			}
			n := mx.Counts[r][a]
			line += " " + heatStyle(ctx.th, n, mx.Max).Render(components.Pad(fmt.Sprintf("%3d", n), cellW-1))
		}
		lines = append(lines, line)
	}
	return lines
}

func (matrixCard) export(ctx renderCtx) ([]string, [][]string) {
	mx := ctx.d.Matrix
	var rows [][]string
	for r, reviewer := range mx.Logins {
		for a, author := range mx.Authors {
			if mx.Counts[r][a] > 0 {
				rows = append(rows, []string{reviewer, author, strconv.Itoa(mx.Counts[r][a])})
			}
		}
	}
	return []string{"Reviewer", "Author", "Reviews"}, rows
}

func rowsFromLogins(logins []string) []metrics.Row {
	rs := make([]metrics.Row, len(logins))
	for i, l := range logins {
		rs[i].Login = l
	}
	return rs
}

func shortLogin(l string, w int) string {
	return text.Clip(l, w)
}

// heatStyle maps a count onto the theme's sequential ramp.
func heatStyle(th *theme.Theme, n, max int) lipgloss.Style {
	if n <= 0 || max <= 0 {
		return th.Heat[0]
	}
	steps := len(th.Heat) - 1
	idx := 1 + (n-1)*steps/max
	if idx > steps {
		idx = steps
	}
	return th.Heat[idx]
}

// ── Time to first review ─────────────────────────────────────────────────

type ttfrCard struct{}

func (ttfrCard) key() string   { return "ttfr" }
func (ttfrCard) title() string { return "First review after" }

func (ttfrCard) headline(ctx renderCtx) string {
	d := ctx.d.TTFR
	total := d.Total()
	if total == 0 {
		return "no reviewed PRs"
	}
	within := d.Counts[0] + d.Counts[1] + d.Counts[2]
	return fmt.Sprintf("%d%% within a day · n=%d", within*100/total, total)
}

func (t ttfrCard) body(ctx renderCtx, w, h int, full bool) string {
	return distBars(ctx, ctx.d.TTFR, w, h, ctx.mark(0), full)
}

func (ttfrCard) export(ctx renderCtx) ([]string, [][]string) {
	return distExport(ctx.d.TTFR, "Waited")
}

// ── PR size ──────────────────────────────────────────────────────────────

type sizeCard struct{}

func (sizeCard) key() string   { return "size" }
func (sizeCard) title() string { return "PR size" }

func (sizeCard) headline(ctx renderCtx) string {
	d := ctx.d.Sizes
	if d.Total() == 0 {
		return "no PRs opened"
	}
	best, n := "", 0
	for i, c := range d.Counts {
		if c > n {
			best, n = d.Labels[i], c
		}
	}
	return fmt.Sprintf("mostly %s · n=%d", best, d.Total())
}

func (s sizeCard) body(ctx renderCtx, w, h int, full bool) string {
	return distBars(ctx, ctx.d.Sizes, w, h, ctx.mark(0), full)
}

func (sizeCard) export(ctx renderCtx) ([]string, [][]string) {
	return distExport(ctx.d.Sizes, "Size")
}

func distBars(ctx renderCtx, d metrics.Dist, w, h int, mark lipgloss.Style, full bool) string {
	max := 0.0
	for _, c := range d.Counts {
		if f := float64(c); f > max {
			max = f
		}
	}
	if max == 0 {
		return ctx.th.HelpDesc.Render("no data in this window")
	}
	labelW := 4
	barW := w - labelW - 5
	if barW < 5 {
		barW = 5
	}
	var lines []string
	for i, label := range d.Labels {
		if !full && len(lines) >= h {
			break
		}
		lines = append(lines, components.BarRow(label, labelW,
			float64(d.Counts[i])*ctx.grow, max, barW, strconv.Itoa(d.Counts[i]),
			mark, ctx.label(), ctx.value()))
	}
	return finish(lines, h, full)
}

func distExport(d metrics.Dist, name string) ([]string, [][]string) {
	rows := make([][]string, len(d.Labels))
	for i, l := range d.Labels {
		rows[i] = []string{l, strconv.Itoa(d.Counts[i])}
	}
	return []string{name, "PRs"}, rows
}

// ── Comments given vs received ───────────────────────────────────────────

type commentsCard struct{}

func (commentsCard) key() string   { return "comments" }
func (commentsCard) title() string { return "Comments" }

func (commentsCard) headline(ctx renderCtx) string {
	var g, r int
	for _, row := range ctx.d.Rows {
		g += row.CommentsGiven
		r += row.CommentsRecv
	}
	return fmt.Sprintf("%d given · %d received", g, r)
}

func (cc commentsCard) active(ctx renderCtx) []metrics.Row {
	var rs []metrics.Row
	for _, r := range ctx.d.Rows {
		if r.CommentsGiven > 0 || r.CommentsRecv > 0 {
			rs = append(rs, r)
		}
	}
	sort.Slice(rs, func(i, j int) bool {
		return rs[i].CommentsGiven+rs[i].CommentsRecv > rs[j].CommentsGiven+rs[j].CommentsRecv
	})
	return rs
}

func (cc commentsCard) body(ctx renderCtx, w, h int, full bool) string {
	rs := cc.active(ctx)
	if len(rs) == 0 {
		return ctx.th.HelpDesc.Render("no comments in this window")
	}
	var lines []string
	if full || h >= 4 { // below that the legend would crowd out every member
		lines = append(lines, ctx.legend("0", "given", "3", "received"))
	}
	labelW := memberLabelW(rs)
	max := 0.0
	for _, r := range rs {
		if f := float64(r.CommentsGiven); f > max {
			max = f
		}
		if f := float64(r.CommentsRecv); f > max {
			max = f
		}
	}
	barW := w - labelW - 5
	if barW < 5 {
		barW = 5
	}
	for _, r := range rs {
		if !full && len(lines)+2 > h {
			break
		}
		lines = append(lines,
			components.BarRow(r.Login, labelW, float64(r.CommentsGiven)*ctx.grow, max, barW,
				strconv.Itoa(r.CommentsGiven), ctx.mark(0), ctx.label(), ctx.value()),
			components.BarRow("", labelW, float64(r.CommentsRecv)*ctx.grow, max, barW,
				strconv.Itoa(r.CommentsRecv), ctx.mark(3), ctx.label(), ctx.value()))
	}
	return finish(lines, h, full)
}

func (cc commentsCard) export(ctx renderCtx) ([]string, [][]string) {
	var rows [][]string
	for _, r := range cc.active(ctx) {
		rows = append(rows, []string{r.Login, strconv.Itoa(r.CommentsGiven), strconv.Itoa(r.CommentsRecv)})
	}
	return []string{"Member", "Given", "Received"}, rows
}

// ── Open-PR aging ────────────────────────────────────────────────────────

type agingCard struct{}

func (agingCard) key() string   { return "aging" }
func (agingCard) title() string { return "Open PRs now" }

func (agingCard) headline(ctx renderCtx) string {
	a := ctx.d.Aging
	if a.Total == 0 {
		return "none open"
	}
	oldest := 0
	if len(a.Stalest) > 0 {
		oldest = a.Stalest[0].AgeDays
	}
	return fmt.Sprintf("%d open · oldest %dd", a.Total, oldest)
}

func (ac agingCard) body(ctx renderCtx, w, h int, full bool) string {
	a := ctx.d.Aging
	if a.Total == 0 {
		return ctx.th.HelpDesc.Render("no open PRs in the cache")
	}
	bars := distBars(ctx, a.Buckets, w, min(h, len(a.Buckets.Labels)), ctx.mark(2), full)
	lines := strings.Split(bars, "\n")
	stale := a.Stalest
	if !full && len(stale) > 1 {
		stale = stale[:1]
	}
	for _, s := range stale {
		if !full && len(lines) >= h {
			break
		}
		// The title is written by whoever opened the PR: sanitize before it
		// reaches the terminal, and truncate by display cells, not bytes.
		entry := text.Sanitize(fmt.Sprintf("%dd %s#%d %s", s.AgeDays, s.Repo, s.Number, s.Title))
		lines = append(lines, ctx.th.HelpDesc.Render(text.Truncate(entry, w)))
	}
	return finish(lines, h, full)
}

func (agingCard) export(ctx renderCtx) ([]string, [][]string) {
	var rows [][]string
	for _, s := range ctx.d.Aging.Stalest {
		rows = append(rows, []string{s.Repo, strconv.Itoa(s.Number), s.Title, s.Author, strconv.Itoa(s.AgeDays)})
	}
	return []string{"Repo", "PR", "Title", "Author", "Age (days)"}, rows
}

// ── Punch card ───────────────────────────────────────────────────────────

type punchCard struct{}

func (punchCard) key() string   { return "punch" }
func (punchCard) title() string { return "Activity punch card" }

func (punchCard) headline(ctx renderCtx) string {
	return fmt.Sprintf("%d events · local time", ctx.d.Punch.Total)
}

var punchDays = []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}

func (p punchCard) body(ctx renderCtx, w, h int, full bool) string {
	pc := ctx.d.Punch
	if pc.Total == 0 {
		return ctx.th.HelpDesc.Render("no activity in this window")
	}
	// Finest column granularity the width allows: hourly, 2-hour, then
	// 4-hour buckets (label column 4 + cols×cellW 4).
	hoursPer := 4
	switch {
	case w >= 24*4+5:
		hoursPer = 1
	case w >= 12*4+5:
		hoursPer = 2
	}
	cols := 24 / hoursPer
	cellW := 4

	head := "    "
	for cIdx := 0; cIdx < cols; cIdx++ {
		head += components.Pad(fmt.Sprintf("%d", cIdx*hoursPer), cellW)
	}
	lines := []string{ctx.th.HelpDesc.Render(strings.TrimRight(head, " "))}

	// Bucket max for the chosen granularity.
	max := 0
	sums := make([][]int, 7)
	for d := 0; d < 7; d++ {
		sums[d] = make([]int, cols)
		for hh := 0; hh < 24; hh++ {
			sums[d][hh/hoursPer] += pc.Counts[d][hh]
		}
		for _, v := range sums[d] {
			if v > max {
				max = v
			}
		}
	}
	cell := func(n, scale int) string {
		c := "  ·"
		if n > 0 {
			c = components.Pad(fmt.Sprintf("%3d", n), cellW-1)
		}
		return heatStyle(ctx.th, n, scale).Render(c) + " "
	}
	// A preview too short for all 7 days leads with an aggregate row so the
	// daily shape is still readable, then as many days as fit.
	if !full && h < 8 {
		all := make([]int, cols)
		allMax := 0
		for d := 0; d < 7; d++ {
			for cIdx, v := range sums[d] {
				all[cIdx] += v
				if all[cIdx] > allMax {
					allMax = all[cIdx]
				}
			}
		}
		line := ctx.th.HelpDesc.Render("All") + " "
		for cIdx := 0; cIdx < cols; cIdx++ {
			line += cell(all[cIdx], allMax)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	for d := 0; d < 7 && (full || len(lines) < h); d++ {
		line := ctx.th.HelpDesc.Render(punchDays[d]) + " "
		for cIdx := 0; cIdx < cols; cIdx++ {
			line += cell(sums[d][cIdx], max)
		}
		lines = append(lines, strings.TrimRight(line, " "))
	}
	return finish(lines, h, full)
}

func (punchCard) export(ctx renderCtx) ([]string, [][]string) {
	var rows [][]string
	for d := 0; d < 7; d++ {
		for hh := 0; hh < 24; hh++ {
			if n := ctx.d.Punch.Counts[d][hh]; n > 0 {
				rows = append(rows, []string{punchDays[d], fmt.Sprintf("%02d:00", hh), strconv.Itoa(n)})
			}
		}
	}
	return []string{"Day", "Hour", "Events"}, rows
}
