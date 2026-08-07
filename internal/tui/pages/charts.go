package pages

import (
	"fmt"
	"math"
	"sort"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/barchart"
	"github.com/NimbleMarkets/ntcharts/v2/linechart/timeserieslinechart"
	"github.com/charmbracelet/harmonica"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// ChartTickMsg drives the bar-growth spring animation; the app forwards it
// here while the charts page reports Animating().
type ChartTickMsg time.Time

const chartFPS = 30

func chartTick() tea.Cmd {
	return tea.Tick(time.Second/chartFPS, func(t time.Time) tea.Msg { return ChartTickMsg(t) })
}

// barAnim is one bar's spring state.
type barAnim struct {
	login    string
	pos, vel float64
	target   float64
}

// Charts is the dashboard page: stat tiles, PR throughput over time, and an
// animated review-load bar chart.
type Charts struct {
	theme *theme.Theme

	rows    []metrics.Row
	buckets []metrics.Bucket

	tslc timeserieslinechart.Model
	bars barchart.Model

	spring    harmonica.Spring
	anims     []barAnim
	animating bool

	width, height int
	ready         bool
}

func NewCharts(th *theme.Theme) Charts {
	return Charts{
		theme:  th,
		spring: harmonica.NewSpring(harmonica.FPS(chartFPS), 7.0, 0.65),
	}
}

func (c *Charts) SetTheme(th *theme.Theme) {
	c.theme = th
	c.rebuild()
}

func (c *Charts) SetSize(w, h int) {
	c.width, c.height = w, h
	c.rebuild()
}

// SetData installs fresh metrics and restarts the bar animation. Only
// members who reviewed appear as bars, busiest first, capped to what the
// chart height can hold.
func (c *Charts) SetData(rows []metrics.Row, buckets []metrics.Bucket) tea.Cmd {
	c.rows = rows
	c.buckets = buckets

	reviewers := make([]metrics.Row, 0, len(rows))
	for _, r := range rows {
		if r.ReviewsGiven > 0 {
			reviewers = append(reviewers, r)
		}
	}
	sort.Slice(reviewers, func(i, j int) bool { return reviewers[i].ReviewsGiven > reviewers[j].ReviewsGiven })

	c.anims = c.anims[:0]
	for _, r := range reviewers {
		c.anims = append(c.anims, barAnim{login: r.Login, target: float64(r.ReviewsGiven)})
	}
	c.animating = len(c.anims) > 0
	c.rebuild()
	if !c.animating {
		return nil
	}
	return chartTick()
}

func (c *Charts) Animating() bool { return c.animating }

func (c Charts) Update(msg tea.Msg) (Charts, tea.Cmd) {
	if _, ok := msg.(ChartTickMsg); ok && c.animating {
		settled := true
		for i := range c.anims {
			a := &c.anims[i]
			a.pos, a.vel = c.spring.Update(a.pos, a.vel, a.target)
			if math.Abs(a.pos-a.target) > 0.01 || math.Abs(a.vel) > 0.01 {
				settled = false
			}
		}
		if settled {
			c.animating = false
			for i := range c.anims {
				c.anims[i].pos = c.anims[i].target
			}
		}
		c.redrawBars()
		if c.animating {
			return c, chartTick()
		}
	}
	return c, nil
}

// rebuild recreates both chart models for the current size and data.
func (c *Charts) rebuild() {
	lw, bw, ch := c.chartBoxes()
	if lw <= 0 || ch <= 0 {
		c.ready = false
		return
	}
	c.ready = true

	axis := lipgloss.NewStyle().Foreground(c.theme.Faint)
	label := lipgloss.NewStyle().Foreground(c.theme.Subtle)

	// Throughput line chart: opened vs merged per day.
	c.tslc = timeserieslinechart.New(lw, ch,
		timeserieslinechart.WithAxesStyles(axis, label))
	openedStyle := lipgloss.NewStyle().Foreground(c.theme.Series[0])
	mergedStyle := lipgloss.NewStyle().Foreground(c.theme.Series[2])
	c.tslc.SetDataSetStyle("opened", openedStyle)
	c.tslc.SetDataSetStyle("merged", mergedStyle)

	maxY := 1.0
	for _, b := range c.buckets {
		c.tslc.PushDataSet("opened", timeserieslinechart.TimePoint{Time: b.Day, Value: float64(b.Opened)})
		c.tslc.PushDataSet("merged", timeserieslinechart.TimePoint{Time: b.Day, Value: float64(b.Merged)})
		maxY = math.Max(maxY, math.Max(float64(b.Opened), float64(b.Merged)))
	}
	if n := len(c.buckets); n > 1 {
		c.tslc.SetViewTimeAndYRange(c.buckets[0].Day, c.buckets[n-1].Day, 0, maxY)
	}
	c.tslc.DrawBrailleAll()

	// Review-load bars, animated via redrawBars.
	c.bars = barchart.New(bw, ch,
		barchart.WithHorizontalBars(),
		barchart.WithNoAutoBarWidth(),
		barchart.WithBarWidth(1),
		barchart.WithBarGap(1),
		barchart.WithStyles(axis, label))
	c.redrawBars()
}

func (c *Charts) redrawBars() {
	if !c.ready {
		return
	}
	barStyle := lipgloss.NewStyle().Foreground(c.theme.Series[1])
	max := 1.0
	for _, a := range c.anims {
		max = math.Max(max, a.target)
	}
	_, _, ch := c.chartBoxes()
	limit := ch / 2 // each horizontal bar plus gap takes ~2 rows
	if limit < 1 {
		limit = 1
	}
	c.bars.Clear()
	c.bars.SetMax(max)
	for i, a := range c.anims {
		if i >= limit {
			break
		}
		c.bars.Push(barchart.BarData{
			Label:  a.login,
			Values: []barchart.BarValue{{Name: "reviews", Value: a.pos, Style: barStyle}},
		})
	}
	// ntcharts only recomputes the label origin and scale on Resize, not on
	// Push/Clear — force it so horizontal labels get their gutter.
	c.bars.Resize(c.bars.Width(), c.bars.Height())
	c.bars.Draw()
}

// chartBoxes computes the line chart width, bar chart width, and shared
// chart height from the page size.
func (c *Charts) chartBoxes() (lw, bw, ch int) {
	tilesH := 4
	titleH := 1
	ch = c.height - tilesH - titleH
	if ch < 4 {
		ch = 4
	}
	lw = c.width * 3 / 5
	bw = c.width - lw - 2
	return lw, bw, ch
}

func (c Charts) View() string {
	if !c.ready || len(c.rows) == 0 {
		return c.theme.Header.Render("\n  No data yet — press s to sync.")
	}

	var opened, merged, reviews, comments int
	for _, r := range c.rows {
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

	openedLbl := lipgloss.NewStyle().Foreground(c.theme.Series[0]).Render("── opened")
	mergedLbl := lipgloss.NewStyle().Foreground(c.theme.Series[2]).Render("── merged")
	leftTitle := c.theme.Header.Render(" PR throughput  ") + openedLbl + " " + mergedLbl
	rightTitle := c.theme.Header.Render(" Review load")

	lw, bw, chH := c.chartBoxes()
	titles := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(lw+2).Render(leftTitle), rightTitle)
	right := c.bars.View()
	if len(c.anims) == 0 {
		right = lipgloss.Place(bw, chH, lipgloss.Center, lipgloss.Center,
			c.theme.HelpDesc.Render("no reviews in this window"))
	}
	charts := lipgloss.JoinHorizontal(lipgloss.Top, c.tslc.View(), "  ", right)

	return lipgloss.JoinVertical(lipgloss.Left, tiles, titles, charts)
}

func (c Charts) tile(label string, v int) string {
	num := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(fmt.Sprintf("%d", v))
	lbl := c.theme.HelpDesc.Render(label)
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(c.theme.Faint).
		Padding(0, 2).Margin(0, 1, 0, 0)
	return box.Render(lipgloss.JoinVertical(lipgloss.Center, num, lbl))
}
