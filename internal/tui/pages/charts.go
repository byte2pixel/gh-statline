package pages

import (
	"fmt"
	"math"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

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

	data    metrics.Dashboard
	hasData bool
	grid    cardGrid[renderCtx]
	// The matrix pins its header row and label column instead of using the
	// viewport; these offsets select the visible cell window.
	mRow, mCol int

	spring    harmonica.Spring
	grow, vel float64 // one spring scales all bars in together
	animating bool
}

func NewCharts(th *theme.Theme, km keys.KeyMap) *Charts {
	c := &Charts{
		theme:  th,
		spring: harmonica.NewSpring(harmonica.FPS(chartFPS), 7.0, 0.7),
		grow:   1,
	}
	c.grid = newCardGrid([]card[renderCtx]{
		throughputCard{}, outcomesCard{}, cycleCard{}, matrixCard{},
		ttfrCard{}, sizeCard{}, commentsCard{}, agingCard{}, punchCard{},
	}, [][]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}}, "card:", km, th)
	c.grid.ctx = c.ctx
	// Chart headlines are plain text: a caption in the grid, header text
	// beside the fullscreen title.
	c.grid.styleHead = func(s string, full bool) string {
		if full {
			return c.theme.Header.Render(s)
		}
		return c.theme.HelpDesc.Render(s)
	}
	return c
}

func (c *Charts) SetTheme(th *theme.Theme) {
	c.theme = th
	c.grid.setTheme(th)
}

func (c *Charts) SetSize(w, h int) { c.grid.setSize(w, h) }

// SetData installs fresh metrics and restarts the grow-in animation.
func (c *Charts) SetData(d metrics.Dashboard) tea.Cmd {
	c.data = d
	c.hasData = true
	c.grow, c.vel = 0, 0
	c.animating = true
	c.grid.refreshFull()
	return chartTick()
}

func (c *Charts) Animating() bool  { return c.animating }
func (c *Charts) Fullscreen() bool { return c.grid.fullscreen() }
func (c *Charts) Focus() int       { return c.grid.focus }

func (c *Charts) HandleClick(msg tea.MouseClickMsg) tea.Cmd {
	return c.grid.handleClick(msg, c.Zones)
}

// matrixFull reports whether the review matrix is the fullscreen card. It
// renders with pinned labels instead of the viewport, so the page handles
// its keys, wheel, and view itself.
func (c *Charts) matrixFull() bool { return c.grid.full == "matrix" }

// matrixGeom reports the matrix dimensions and how many cell rows/columns
// fit beside the pinned reviewer labels and under the pinned header.
func (c *Charts) matrixGeom() (rows, cols, visRows, visCols int) {
	mx := c.data.Matrix
	labelW := memberLabelW(rowsFromLogins(mx.Logins))
	visCols = (c.grid.width - labelW - 1) / 4 // cellW
	visRows = c.grid.height - 3               // title + header + indicator
	visRows = max(visRows, 1)
	visCols = max(visCols, 1)
	return len(mx.Logins), len(mx.Authors), visRows, visCols
}

// Scroll moves the fullscreen view; wired to the mouse wheel.
func (c *Charts) Scroll(delta int) {
	if !c.matrixFull() {
		c.grid.scroll(delta)
		return
	}
	rows, _, visRows, _ := c.matrixGeom()
	c.mRow = min(max(c.mRow+delta, 0), max(rows-visRows, 0))
}

// handleMatrixKey scrolls the pinned-header matrix: up/down move rows,
// left/right move author columns; the labels never leave the screen.
func (c *Charts) handleMatrixKey(msg tea.KeyPressMsg) bool {
	rows, cols, visRows, visCols := c.matrixGeom()
	maxRow := max(rows-visRows, 0)
	maxCol := max(cols-visCols, 0)
	km := c.grid.km
	switch {
	case key.Matches(msg, km.Back, km.Expand, km.Drill):
		c.grid.closeFull()
		c.mRow, c.mCol = 0, 0
	case key.Matches(msg, km.Down):
		c.mRow = min(c.mRow+1, maxRow)
	case key.Matches(msg, km.Up):
		c.mRow = max(c.mRow-1, 0)
	case key.Matches(msg, km.Right):
		c.mCol = min(c.mCol+1, maxCol)
	case key.Matches(msg, km.Left):
		c.mCol = max(c.mCol-1, 0)
	case key.Matches(msg, km.PageDown):
		c.mRow = min(c.mRow+visRows, maxRow)
	case key.Matches(msg, km.PageUp):
		c.mRow = max(c.mRow-visRows, 0)
	case key.Matches(msg, km.HalfDown):
		c.mRow = min(c.mRow+visRows/2, maxRow)
	case key.Matches(msg, km.HalfUp):
		c.mRow = max(c.mRow-visRows/2, 0)
	case key.Matches(msg, km.Top):
		c.mRow, c.mCol = 0, 0
	case key.Matches(msg, km.Bottom):
		c.mRow = maxRow
	default:
		return false
	}
	return true
}

func (c *Charts) Update(msg tea.Msg) tea.Cmd {
	if _, ok := msg.(ChartTickMsg); ok && c.animating {
		c.grow, c.vel = c.spring.Update(c.grow, c.vel, 1)
		if math.Abs(c.grow-1) < 0.005 && math.Abs(c.vel) < 0.005 {
			c.grow, c.animating = 1, false
		}
		if !c.matrixFull() {
			c.grid.refreshFull() // keep the fullscreen body in step with the spring
		}
		if c.animating {
			return chartTick()
		}
	}
	return nil
}

// HandleKey processes grid navigation and fullscreen scrolling.
// handled=false lets the app's global keymap take the key instead.
func (c *Charts) HandleKey(msg tea.KeyPressMsg) bool {
	if c.matrixFull() {
		return c.handleMatrixKey(msg)
	}
	return c.grid.handleKey(msg)
}

func (c *Charts) ctx() renderCtx {
	return renderCtx{styleCtx: styleCtx{c.theme}, d: &c.data, grow: c.grow}
}

func (c *Charts) View() string {
	if !c.hasData || len(c.data.Rows) == 0 {
		return c.theme.Header.Render("\n  No data yet — press s to sync.")
	}
	switch {
	case c.matrixFull():
		return c.viewFullMatrix()
	case c.grid.fullscreen():
		return c.grid.viewFull(c.ctx())
	}
	return c.viewGrid()
}

// viewFullMatrix renders the review matrix with pinned labels: the author
// header row and the reviewer name column stay on screen while mRow/mCol
// select which cells are visible.
func (c *Charts) viewFullMatrix() string {
	ctx := c.ctx()
	active := c.grid.activeCard()
	if active == nil {
		return ""
	}
	rows, cols, visRows, visCols := c.matrixGeom()
	rowOff := min(c.mRow, max(rows-visRows, 0))
	colOff := min(c.mCol, max(cols-visCols, 0))

	title := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(active.title()) +
		"  " + c.theme.Header.Render(active.headline(ctx)) +
		"   " + c.theme.HelpDesc.Render("esc back · j/k rows · h/l columns · y copy")
	title = lipgloss.NewStyle().MaxWidth(c.grid.width).Render(title)

	body := matrixCard{}.pinnedGrid(ctx, rowOff, colOff, visRows, visCols)
	// Pad so the indicator always sits on the last line: title + header +
	// visRows + indicator = height.
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
	tilesH   = 4
	minGridH = 15 // 3 rows × the 5-line minimum card
)

// gridGeometry fits three card rows (plus the stat tiles when there's room)
// into exactly the page height. ok=false means the terminal is too short.
func (c *Charts) gridGeometry() (rowHs []int, showTiles, ok bool) {
	w, h := c.grid.width, c.grid.height
	showTiles = w >= 64 && h-tilesH >= minGridH
	avail := h
	if showTiles {
		avail -= tilesH
	}
	if avail < minGridH {
		return nil, false, false
	}
	return splitRows(avail, 3), showTiles, true
}

func (c *Charts) viewGrid() string {
	rowHs, showTiles, ok := c.gridGeometry()
	if !ok {
		return lipgloss.Place(c.grid.width, c.grid.height, lipgloss.Center, lipgloss.Center,
			c.theme.HelpDesc.Render("terminal too short for the charts grid (need ≥ 18 rows)\nresize, or press 1 for the team view"))
	}
	var parts []string
	if showTiles {
		parts = append(parts, c.tilesRow())
	}
	parts = append(parts, c.grid.gridRows(c.ctx(), rowHs, c.Zones)...)
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

// Export renders the fullscreen card's table or, from the grid, the team
// stat lines that underlie every chart.
func (c *Charts) Export(team string, w metrics.Window) string {
	if title, headers, rows, ok := c.grid.exportTable(c.ctx()); ok {
		return export.Table(title+" — "+w.Label, headers, rows)
	}
	return export.TeamStats(team, w, c.data.Rows)
}

// tilesRow renders the stat tiles with window-over-window deltas, dropping
// trailing tiles when the terminal can't fit them all.
func (c *Charts) tilesRow() string {
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
	for len(tiles) > 1 && lipgloss.Width(row) > c.grid.width {
		tiles = tiles[:len(tiles)-1]
		row = lipgloss.JoinHorizontal(lipgloss.Top, tiles...)
	}
	return row
}

func (c *Charts) tile(label, value, delta string, deltaStyle lipgloss.Style) string {
	num := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(value) +
		" " + deltaStyle.Render(delta)
	lbl := c.theme.HelpDesc.Render(label)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(c.theme.Faint).
		Padding(0, 2).Margin(0, 1, 0, 0)
	return box.Render(lipgloss.JoinVertical(lipgloss.Center, num, lbl))
}
