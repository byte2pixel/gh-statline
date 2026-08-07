package pages

import (
	"fmt"
	"math"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/charmbracelet/harmonica"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// ChartData is every dataset the charts page renders, loaded in one shot.
type ChartData struct {
	Rows    []metrics.Row
	Buckets []metrics.Bucket
	Weekly  bool // Buckets are week-sized (long windows)
	Trend   []metrics.TrendPoint
	TTFR    metrics.Dist
	Sizes   metrics.Dist
	Matrix  metrics.Matrix
	Punch   metrics.Punch
	Aging   metrics.Aging
}

// ChartTickMsg drives the bar-growth spring animation; the app forwards it
// here while the charts page reports Animating().
type ChartTickMsg time.Time

const chartFPS = 30

func chartTick() tea.Cmd {
	return tea.Tick(time.Second/chartFPS, func(t time.Time) tea.Msg { return ChartTickMsg(t) })
}

// Charts is the dashboard page: stat tiles above a scrollable 2-wide grid
// of chart cards, any of which can be expanded to fullscreen.
type Charts struct {
	theme *theme.Theme
	Zones *zone.Manager

	data    ChartData
	hasData bool
	cards   []card
	focus   int
	full    string // fullscreen card key, "" = grid

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
		cards: []card{
			throughputCard{}, outcomesCard{}, cycleCard{}, matrixCard{},
			ttfrCard{}, sizeCard{}, commentsCard{}, agingCard{}, punchCard{},
		},
	}
}

func (c *Charts) SetTheme(th *theme.Theme) { c.theme = th }

func (c *Charts) SetSize(w, h int) { c.width, c.height = w, h }

// SetData installs fresh metrics and restarts the grow-in animation.
func (c *Charts) SetData(d ChartData) tea.Cmd {
	c.data = d
	c.hasData = true
	c.grow, c.vel = 0, 0
	c.animating = true
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
			c.full = key
		} else {
			c.focus = i
		}
		return
	}
}

func (c Charts) Update(msg tea.Msg) (Charts, tea.Cmd) {
	if _, ok := msg.(ChartTickMsg); ok && c.animating {
		c.grow, c.vel = c.spring.Update(c.grow, c.vel, 1)
		if math.Abs(c.grow-1) < 0.005 && math.Abs(c.vel) < 0.005 {
			c.grow, c.animating = 1, false
		}
		if c.animating {
			return c, chartTick()
		}
	}
	return c, nil
}

// HandleKey processes grid navigation. handled=false lets the app's global
// keymap take the key instead.
func (c *Charts) HandleKey(k string) (handled bool) {
	if c.full != "" {
		switch k {
		case "esc", "enter", "f":
			c.full = ""
			return true
		}
		return false
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
		if c.focus >= 2 {
			c.focus -= 2
		}
	case "down", "j":
		if c.focus < len(c.cards)-2 {
			c.focus += 2
		}
	case "enter", "f":
		c.full = c.cards[c.focus].key()
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
	var active card
	for _, cd := range c.cards {
		if cd.key() == c.full {
			active = cd
			break
		}
	}
	if active == nil {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(active.title()) +
		"  " + c.theme.Header.Render(active.headline(ctx)) +
		"   " + c.theme.HelpDesc.Render("esc back · y copy")
	body := active.body(ctx, c.width-2, c.height-2, true)
	return lipgloss.JoinVertical(lipgloss.Left, title, body)
}

const tilesH = 4

func (c Charts) viewGrid() string {
	ctx := c.ctx()

	var opened, merged, reviews, comments int
	for _, r := range c.data.Rows {
		opened += r.PRsOpened
		merged += r.PRsMerged
		reviews += r.ReviewsGiven
		comments += r.CommentsGiven
	}
	tiles := lipgloss.JoinHorizontal(lipgloss.Top,
		c.tile("PRs opened", opened),
		c.tile("Merged", merged),
		c.tile("Reviews", reviews),
		c.tile("Comments", comments),
	)

	avail := c.height - tilesH
	cardOuterH := avail / 2
	if cardOuterH < 8 {
		cardOuterH = avail // very short terminal: one row of cards
	}
	if cardOuterH > 14 {
		cardOuterH = 14
	}
	visibleRows := avail / cardOuterH
	if visibleRows < 1 {
		visibleRows = 1
	}

	totalRows := (len(c.cards) + 1) / 2
	focusRow := c.focus / 2
	firstRow := 0
	if focusRow >= visibleRows {
		firstRow = focusRow - visibleRows + 1
	}
	lastRow := firstRow + visibleRows
	if lastRow > totalRows {
		lastRow = totalRows
	}

	cardOuterW := c.width/2 - 1
	innerW := cardOuterW - 4 // border + padding
	innerH := cardOuterH - 3 // border + title line

	var rows []string
	for row := firstRow; row < lastRow; row++ {
		var cells []string
		for col := 0; col < 2; col++ {
			i := row*2 + col
			if i >= len(c.cards) {
				break
			}
			cells = append(cells, c.renderCard(ctx, i, innerW, innerH))
		}
		rows = append(rows, lipgloss.JoinHorizontal(lipgloss.Top, cells...))
	}
	scrollHint := ""
	if lastRow < totalRows {
		scrollHint = c.theme.HelpDesc.Render(fmt.Sprintf("  ↓ %d more chart(s)", (totalRows-lastRow)*2))
	}

	parts := append([]string{tiles}, rows...)
	if scrollHint != "" {
		parts = append(parts, scrollHint)
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
	body := cd.body(ctx, innerW, innerH, false)
	// Pad the body to exactly innerH lines so cards in a row box equally.
	if n := strings.Count(body, "\n") + 1; n < innerH {
		body += strings.Repeat("\n", innerH-n)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, title+head, body)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(innerW + 2).
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

func (c Charts) tile(label string, v int) string {
	num := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(fmt.Sprintf("%d", v))
	lbl := c.theme.HelpDesc.Render(label)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(c.theme.Faint).
		Padding(0, 2).Margin(0, 1, 0, 0)
	return box.Render(lipgloss.JoinVertical(lipgloss.Center, num, lbl))
}
