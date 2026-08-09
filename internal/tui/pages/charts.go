package pages

import (
	"fmt"
	"math"
	"strings"
	"time"

	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// TileTrend compares the current window's headline stats to the previous
// equal-length window for the stat tiles. HasPrev is false when the cache
// doesn't reach back far enough for an honest comparison.
type TileTrend struct {
	HasPrev                   bool
	PrevOpened, PrevMerged    int
	PrevReviews, PrevComments int
	Cycle, PrevCycle          time.Duration // team p50 open→merge
	TTFR, PrevTTFR            time.Duration // team p50 time to first review
}

// ChartData is every dataset the charts page renders, loaded in one shot.
type ChartData struct {
	Rows      []metrics.Row
	Buckets   []metrics.Bucket
	BucketDur time.Duration // width of one throughput bucket
	Tiles     TileTrend
	Trend     []metrics.TrendPoint
	TTFR      metrics.Dist
	Sizes     metrics.Dist
	Matrix    metrics.Matrix
	Punch     metrics.Punch
	Aging     metrics.Aging
}

// ChartTickMsg drives the bar-growth spring animation; the app forwards it
// here while the charts page reports Animating().
type ChartTickMsg time.Time

const chartFPS = 30

func chartTick() tea.Cmd {
	return tea.Tick(time.Second/chartFPS, func(t time.Time) tea.Msg { return ChartTickMsg(t) })
}

// Charts is the dashboard page: stat tiles above a fixed 3×3 grid of chart
// cards — always all on one screen — any of which can be expanded to a
// scrollable fullscreen view.
type Charts struct {
	theme *theme.Theme
	Zones *zone.Manager

	data    ChartData
	hasData bool
	cards   []card
	focus   int
	full    string         // fullscreen card key, "" = grid
	vp      viewport.Model // scrolls the fullscreen card body
	// The matrix pins its header row and label column instead of using the
	// viewport; these offsets select the visible cell window.
	mRow, mCol int

	spring    harmonica.Spring
	grow, vel float64 // one spring scales all bars in together
	animating bool

	width, height int
}

func NewCharts(th *theme.Theme) Charts {
	return Charts{
		theme:  th,
		spring: harmonica.NewSpring(harmonica.FPS(chartFPS), 7.0, 0.7),
		grow:   1,
		vp:     viewport.New(),
		cards: []card{
			throughputCard{}, outcomesCard{}, cycleCard{}, matrixCard{},
			ttfrCard{}, sizeCard{}, commentsCard{}, agingCard{}, punchCard{},
		},
	}
}

func (c *Charts) SetTheme(th *theme.Theme) { c.theme = th }

func (c *Charts) SetSize(w, h int) {
	c.width, c.height = w, h
	c.refreshFull()
}

// SetData installs fresh metrics and restarts the grow-in animation.
func (c *Charts) SetData(d ChartData) tea.Cmd {
	c.data = d
	c.hasData = true
	c.grow, c.vel = 0, 0
	c.animating = true
	c.refreshFull()
	return chartTick()
}

func (c *Charts) Animating() bool  { return c.animating }
func (c *Charts) Fullscreen() bool { return c.full != "" }
func (c *Charts) Focus() int       { return c.focus }

// CardKeys lists card keys in grid order (for zone hit-testing).
func (c *Charts) CardKeys() []string {
	keys := make([]string, len(c.cards))
	for i, cd := range c.cards {
		keys[i] = cd.key()
	}
	return keys
}

// ClickCard focuses the clicked card; clicking the focused card expands it.
func (c *Charts) ClickCard(key string) {
	for i, cd := range c.cards {
		if cd.key() != key {
			continue
		}
		if c.focus == i && c.full == "" {
			c.openFull(key)
		} else {
			c.focus = i
		}
		return
	}
}

func (c Charts) activeCard() card {
	for _, cd := range c.cards {
		if cd.key() == c.full {
			return cd
		}
	}
	return nil
}

// openFull expands a card to fullscreen with the viewport reset to the top.
func (c *Charts) openFull(key string) {
	c.full = key
	c.mRow, c.mCol = 0, 0
	c.vp.SetYOffset(0)
	c.vp.SetXOffset(0)
	c.refreshFull()
}

// refreshFull re-renders the fullscreen card into the viewport, preserving
// the scroll offset (SetContent clamps it if the content shrank).
func (c *Charts) refreshFull() {
	if c.full == "" || c.full == "matrix" { // the matrix renders directly
		return
	}
	active := c.activeCard()
	if active == nil {
		return
	}
	h := c.height - 2 // title line + scroll indicator line
	if h < 1 {
		h = 1
	}
	c.vp.SetWidth(c.width)
	c.vp.SetHeight(h)
	c.vp.SetContent(active.body(c.ctx(), c.width-2, h, true))
}

// matrixGeom reports the matrix dimensions and how many cell rows/columns
// fit beside the pinned reviewer labels and under the pinned header.
func (c Charts) matrixGeom() (rows, cols, visRows, visCols int) {
	mx := c.data.Matrix
	labelW := memberLabelW(rowsFromLogins(mx.Logins))
	visCols = (c.width - labelW - 1) / 4 // cellW
	visRows = c.height - 3               // title + header + indicator
	visRows = max(visRows, 1)
	visCols = max(visCols, 1)
	return len(mx.Logins), len(mx.Authors), visRows, visCols
}

// Scroll moves the fullscreen view; wired to the mouse wheel.
func (c *Charts) Scroll(delta int) {
	switch {
	case c.full == "":
	case c.full == "matrix":
		rows, _, visRows, _ := c.matrixGeom()
		c.mRow = min(max(c.mRow+delta, 0), max(rows-visRows, 0))
	case delta < 0:
		c.vp.ScrollUp(-delta)
	default:
		c.vp.ScrollDown(delta)
	}
}

// handleMatrixKey scrolls the pinned-header matrix: j/k move rows, h/l move
// author columns; the labels never leave the screen.
func (c *Charts) handleMatrixKey(k string) bool {
	rows, cols, visRows, visCols := c.matrixGeom()
	maxRow := max(rows-visRows, 0)
	maxCol := max(cols-visCols, 0)
	switch k {
	case "esc", "enter", "f":
		c.full = ""
	case "down", "j":
		c.mRow = min(c.mRow+1, maxRow)
	case "up", "k":
		c.mRow = max(c.mRow-1, 0)
	case "right", "l":
		c.mCol = min(c.mCol+1, maxCol)
	case "left", "h":
		c.mCol = max(c.mCol-1, 0)
	case "pgdown", "space":
		c.mRow = min(c.mRow+visRows, maxRow)
	case "pgup":
		c.mRow = max(c.mRow-visRows, 0)
	case "d":
		c.mRow = min(c.mRow+visRows/2, maxRow)
	case "u":
		c.mRow = max(c.mRow-visRows/2, 0)
	case "g", "home":
		c.mRow, c.mCol = 0, 0
	case "G", "shift+g", "end":
		c.mRow = maxRow
	default:
		return false
	}
	return true
}

func (c Charts) Update(msg tea.Msg) (Charts, tea.Cmd) {
	if _, ok := msg.(ChartTickMsg); ok && c.animating {
		c.grow, c.vel = c.spring.Update(c.grow, c.vel, 1)
		if math.Abs(c.grow-1) < 0.005 && math.Abs(c.vel) < 0.005 {
			c.grow, c.animating = 1, false
		}
		c.refreshFull() // keep the fullscreen body in step with the spring
		if c.animating {
			return c, chartTick()
		}
	}
	return c, nil
}

// HandleKey processes grid navigation and fullscreen scrolling.
// handled=false lets the app's global keymap take the key instead.
func (c *Charts) HandleKey(k string) (handled bool) {
	if c.full == "matrix" {
		return c.handleMatrixKey(k)
	}
	if c.full != "" {
		switch k {
		case "esc", "enter", "f":
			c.full = ""
		case "down", "j":
			c.vp.ScrollDown(1)
		case "up", "k":
			c.vp.ScrollUp(1)
		case "pgdown", "space":
			c.vp.PageDown()
		case "pgup":
			c.vp.PageUp()
		case "d":
			c.vp.HalfPageDown()
		case "u":
			c.vp.HalfPageUp()
		case "g", "home":
			c.vp.SetYOffset(0)
		case "G", "shift+g", "end":
			c.vp.GotoBottom()
		case "left", "h":
			c.vp.ScrollLeft(4) // pan wide content, e.g. the review matrix
		case "right", "l":
			c.vp.ScrollRight(4)
		default:
			return false
		}
		return true
	}
	switch k {
	case "left", "h":
		if c.focus > 0 {
			c.focus--
		}
	case "right", "l":
		if c.focus < len(c.cards)-1 {
			c.focus++
		}
	case "up", "k":
		if c.focus >= 3 {
			c.focus -= 3
		}
	case "down", "j":
		if c.focus < len(c.cards)-3 {
			c.focus += 3
		}
	case "enter", "f":
		c.openFull(c.cards[c.focus].key())
	default:
		return false
	}
	return true
}

func (c *Charts) ctx() renderCtx {
	return renderCtx{d: &c.data, th: c.theme, grow: c.grow}
}

func (c Charts) View() string {
	if !c.hasData || len(c.data.Rows) == 0 {
		return c.theme.Header.Render("\n  No data yet — press s to sync.")
	}
	if c.full != "" {
		return c.viewFull()
	}
	return c.viewGrid()
}

func (c Charts) viewFull() string {
	ctx := c.ctx()
	active := c.activeCard()
	if active == nil {
		return ""
	}
	if c.full == "matrix" {
		return c.viewFullMatrix(ctx, active)
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(active.title()) +
		"  " + c.theme.Header.Render(active.headline(ctx)) +
		"   " + c.theme.HelpDesc.Render("esc back · j/k scroll · h/l pan · y copy")
	title = lipgloss.NewStyle().MaxWidth(c.width).Render(title)

	// The indicator line is always present (even when blank) so the view is
	// exactly title + viewport + indicator = c.height lines.
	indicator := ""
	if total := c.vp.TotalLineCount(); total > c.vp.Height() {
		top := c.vp.YOffset() + 1
		bottom := min(top+c.vp.VisibleLineCount()-1, total)
		indicator = c.theme.HelpDesc.Render(fmt.Sprintf(
			"  rows %d–%d of %d · %d%%", top, bottom, total, int(c.vp.ScrollPercent()*100)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, c.vp.View(), indicator)
}

// viewFullMatrix renders the review matrix with pinned labels: the author
// header row and the reviewer name column stay on screen while mRow/mCol
// select which cells are visible.
func (c Charts) viewFullMatrix(ctx renderCtx, active card) string {
	rows, cols, visRows, visCols := c.matrixGeom()
	rowOff := min(c.mRow, max(rows-visRows, 0))
	colOff := min(c.mCol, max(cols-visCols, 0))

	title := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(active.title()) +
		"  " + c.theme.Header.Render(active.headline(ctx)) +
		"   " + c.theme.HelpDesc.Render("esc back · j/k rows · h/l columns · y copy")
	title = lipgloss.NewStyle().MaxWidth(c.width).Render(title)

	body := matrixCard{}.pinnedGrid(ctx, rowOff, colOff, visRows, visCols)
	// Pad so the indicator always sits on the last line: title + header +
	// visRows + indicator = c.height.
	for len(body) < visRows+1 {
		body = append(body, "")
	}

	indicator := ""
	if rows > visRows || cols > visCols {
		indicator = c.theme.HelpDesc.Render(fmt.Sprintf("  rows %d–%d of %d · cols %d–%d of %d",
			rowOff+1, min(rowOff+visRows, rows), rows,
			colOff+1, min(colOff+visCols, cols), cols))
	}

	parts := append([]string{title}, body...)
	parts = append(parts, indicator)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

const (
	tilesH        = 4
	maxCardOuterH = 14
	minGridH      = 15 // 3 rows × the 5-line minimum card
)

// gridGeometry fits three card rows (plus the stat tiles when there's room)
// into exactly c.height. ok=false means the terminal is too short.
func (c Charts) gridGeometry() (rowHs [3]int, showTiles, ok bool) {
	showTiles = c.width >= 64 && c.height-tilesH >= minGridH
	avail := c.height
	if showTiles {
		avail -= tilesH
	}
	if avail < minGridH {
		return rowHs, false, false
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
	return rowHs, showTiles, true
}

func (c Charts) viewGrid() string {
	rowHs, showTiles, ok := c.gridGeometry()
	if !ok {
		return lipgloss.Place(c.width, c.height, lipgloss.Center, lipgloss.Center,
			c.theme.HelpDesc.Render("terminal too short for the charts grid (need ≥ 18 rows)\nresize, or press 1 for the leaderboard"))
	}
	ctx := c.ctx()

	var parts []string
	if showTiles {
		parts = append(parts, c.tilesRow())
	}

	cardOuterW := c.width / 3
	innerW := cardOuterW - 4 // border + padding
	for row := 0; row < 3; row++ {
		innerH := rowHs[row] - 3 // border + title line
		var cells []string
		for col := 0; col < 3; col++ {
			i := row*3 + col
			if i >= len(c.cards) {
				break
			}
			cells = append(cells, c.renderCard(ctx, i, innerW, innerH))
		}
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (c Charts) renderCard(ctx renderCtx, i, innerW, innerH int) string {
	cd := c.cards[i]
	border := c.theme.Faint
	titleStyle := c.theme.Header
	if i == c.focus {
		border = c.theme.Accent
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(c.theme.Accent)
	}
	title := titleStyle.Render(cd.title())
	head := c.theme.HelpDesc.Render(" " + cd.headline(ctx))
	// MaxWidth/MaxHeight guard against wrapping: a wrapped title or legend
	// line would make the card taller than budgeted and clip the footer.
	guard := lipgloss.NewStyle().MaxWidth(innerW)
	body := guard.MaxHeight(innerH).Render(cd.body(ctx, innerW, innerH, false))
	// Pad the body to exactly innerH lines so cards in a row box equally.
	if n := strings.Count(body, "\n") + 1; n < innerH {
		body += strings.Repeat("\n", innerH-n)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, guard.Render(title+head), body)
	// Width includes border and padding (lipgloss v2), so innerW+4 makes the
	// box wrap exactly at innerW — the width the body was rendered for.
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(innerW + 4).
		Render(content)
	if c.Zones != nil {
		box = c.Zones.Mark("card:"+cd.key(), box)
	}
	return box
}

// ExportTable returns the fullscreen card's data for Markdown export.
func (c Charts) ExportTable() (title string, headers []string, rows [][]string, ok bool) {
	if c.full == "" {
		return "", nil, nil, false
	}
	ctx := c.ctx()
	for _, cd := range c.cards {
		if cd.key() == c.full {
			h, r := cd.export(ctx)
			return cd.title(), h, r, true
		}
	}
	return "", nil, nil, false
}

// tilesRow renders the stat tiles with window-over-window deltas, dropping
// trailing tiles when the terminal can't fit them all.
func (c Charts) tilesRow() string {
	var opened, merged, reviews, comments int
	for _, r := range c.data.Rows {
		opened += r.PRsOpened
		merged += r.PRsMerged
		reviews += r.ReviewsGiven
		comments += r.CommentsGiven
	}
	tr := c.data.Tiles

	type spec struct {
		label, value, delta string
		style               lipgloss.Style
	}
	specs := make([]spec, 0, 6)
	count := func(label string, curr, prev int) {
		d, st := deltaCount(c.theme, curr, prev, tr.HasPrev)
		specs = append(specs, spec{label, fmt.Sprintf("%d", curr), d, st})
	}
	count("PRs opened", opened, tr.PrevOpened)
	count("Merged", merged, tr.PrevMerged)
	count("Reviews", reviews, tr.PrevReviews)
	count("Comments", comments, tr.PrevComments)
	dur := func(label string, curr, prev time.Duration) {
		d, st := deltaDur(c.theme, curr, prev, tr.HasPrev)
		specs = append(specs, spec{label, metrics.FmtDur(curr), d, st})
	}
	dur("Cycle p50", tr.Cycle, tr.PrevCycle)
	dur("TTFR p50", tr.TTFR, tr.PrevTTFR)

	tiles := make([]string, len(specs))
	for i, s := range specs {
		tiles[i] = c.tile(s.label, s.value, s.delta, s.style)
	}
	row := lipgloss.JoinHorizontal(lipgloss.Top, tiles...)
	for len(tiles) > 1 && lipgloss.Width(row) > c.width {
		tiles = tiles[:len(tiles)-1]
		row = lipgloss.JoinHorizontal(lipgloss.Top, tiles...)
	}
	return row
}

func (c Charts) tile(label, value, delta string, deltaStyle lipgloss.Style) string {
	num := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(value) +
		" " + deltaStyle.Render(delta)
	lbl := c.theme.HelpDesc.Render(label)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(c.theme.Faint).
		Padding(0, 2).Margin(0, 1, 0, 0)
	return box.Render(lipgloss.JoinVertical(lipgloss.Center, num, lbl))
}
