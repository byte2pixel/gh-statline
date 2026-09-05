package pages

import (
	"fmt"

	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	"github.com/NimbleMarkets/ntcharts/v2/sparkline"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Person is the drill-down view for one member: headline stats, an activity
// sparkline, and a per-repo breakdown.
type Person struct {
	theme *theme.Theme

	Login    string
	row      metrics.Row
	repos    []metrics.RepoBreakdown
	activity []float64

	spark sparkline.Model
	tbl   table.Model

	width, height int
	ready         bool
}

func NewPerson(th *theme.Theme) *Person {
	p := &Person{theme: th}
	p.tbl = table.New(table.WithFocused(true))
	p.tbl.SetStyles(tableStyles(p.theme))
	return p
}

func (p *Person) SetTheme(th *theme.Theme) {
	p.theme = th
	p.tbl.SetStyles(tableStyles(p.theme))
	p.rebuild()
}

func (p *Person) SetSize(w, h int) {
	p.width, p.height = w, h
	p.rebuild()
}

func (p *Person) SetData(login string, row metrics.Row, repos []metrics.RepoBreakdown, activity []float64) {
	p.Login = login
	p.row = row
	p.repos = repos
	p.activity = activity
	p.rebuild()
}

const personSparkH = 4

func (p *Person) rebuild() {
	if p.width <= 0 || p.Login == "" {
		p.ready = false
		return
	}
	p.ready = true

	sw := p.width - 4
	if sw > len(p.activity)*2 {
		sw = len(p.activity) * 2 // wider bars read better on short windows
	}
	if sw < 10 {
		sw = 10
	}
	p.spark = sparkline.New(sw, personSparkH,
		sparkline.WithStyle(lipgloss.NewStyle().Foreground(p.theme.Primary)))
	p.spark.PushAll(p.activity)
	p.spark.Draw()

	cols := []table.Column{
		{Title: "Repo", Width: 32},
		{Title: "PRs", Width: 5},
		{Title: "Merged", Width: 7},
		{Title: "Reviews", Width: 8},
		{Title: "Comments", Width: 9},
	}
	rows := make([]table.Row, len(p.repos))
	for i, r := range p.repos {
		rows[i] = table.Row{
			r.Repo,
			fmt.Sprintf("%d", r.PRsOpened),
			fmt.Sprintf("%d", r.PRsMerged),
			fmt.Sprintf("%d", r.ReviewsGiven),
			fmt.Sprintf("%d", r.CommentsGiven),
		}
	}
	p.tbl.SetColumns(cols)
	p.tbl.SetRows(rows)
	p.tbl.SetWidth(p.width)
	// stats(2) + sparkline + gap(1)
	th := p.height - 2 - personSparkH - 1
	if th < 3 {
		th = 3
	}
	p.tbl.SetHeight(th)
}

// Scroll moves the repo table cursor by delta rows (mouse wheel).
func (p *Person) Scroll(delta int) {
	if delta < 0 {
		p.tbl.MoveUp(-delta)
	} else {
		p.tbl.MoveDown(delta)
	}
}

// HandleKey claims no keys ahead of the global keymap; the repo table's
// cursor keys ride the residual Update path after it instead.
func (p *Person) HandleKey(string) bool { return false }

// HandleClick is a no-op: the person page marks no click zones.
func (p *Person) HandleClick(tea.MouseClickMsg) tea.Cmd { return nil }

// Export renders the member's stats and repo breakdown as Markdown. The
// team name goes unused: the heading is the member's login.
func (p *Person) Export(_ string, w metrics.Window) string {
	return export.Person(p.Login, w, p.row, p.repos)
}

func (p *Person) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.tbl, cmd = p.tbl.Update(msg)
	return cmd
}

func (p *Person) View() string {
	if !p.ready {
		return ""
	}
	r := p.row
	name := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Accent).Render(p.Login)
	stats := fmt.Sprintf("PRs %d opened · %d merged   reviews %d (%d✓ %d💬 %d±)   comments %d given / %d received",
		r.PRsOpened, r.PRsMerged, r.ReviewsGiven, r.Approved, r.Commented, r.ChangesReq,
		r.CommentsGiven, r.CommentsRecv)
	statsLine := p.theme.Header.Render(stats)

	sparkTitle := p.theme.HelpDesc.Render("activity (PRs + reviews per day)")
	return lipgloss.JoinVertical(lipgloss.Left,
		name,
		statsLine,
		sparkTitle,
		p.spark.View(),
		p.tbl.View(),
	)
}
