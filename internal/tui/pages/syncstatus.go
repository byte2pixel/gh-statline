package pages

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// SyncStatus answers "can I trust these numbers": one row per configured
// repo with the last clean sync, how deep the cache covers, and the error
// from the last failed walk spelled out underneath. It is not a tab —
// nothing here is a statistic — so it renders into a plain viewport
// instead of the card grid, which also lets a failure carry as many lines
// as its error needs while the rows above stay aligned.
type SyncStatus struct {
	theme *theme.Theme
	km    keys.KeyMap

	rep     doctor.Report
	hasData bool

	vp            viewport.Model
	width, height int
}

func NewSyncStatus(th *theme.Theme, km keys.KeyMap) *SyncStatus {
	return &SyncStatus{theme: th, km: km, vp: viewport.New()}
}

func (s *SyncStatus) SetTheme(th *theme.Theme) {
	s.theme = th
	s.rebuild()
}

func (s *SyncStatus) SetSize(w, h int) {
	s.width, s.height = w, h
	s.rebuild()
}

// SetData installs a fresh report. The scroll offset is left alone so a
// sync completing while the view is open doesn't jump the reader back to
// the top of a list they were part-way down.
func (s *SyncStatus) SetData(rep doctor.Report) {
	s.rep = rep
	s.hasData = true
	s.rebuild()
}

// Column widths, in cells. Everything but the repo name is fixed: the
// values are bounded ("never synced" is the widest status, "2026-01-12"
// the widest date), so only the name has to flex.
const (
	colAge    = 10 // "just now", "3.2d ago"
	colDate   = 12 // "2026-01-12"
	colStatus = 12 // "never synced"
	colGap    = 2
	minRepoW  = 14
	// maxRepoW stops a wide terminal from stretching the name column until
	// the values sit half a screen away from the repo they describe.
	maxRepoW = 34
)

// layout picks the columns that fit. The two least useful go first: on a
// narrow terminal the repo name and whether it is broken are what matter,
// and dropping a column beats wrapping a row into unreadability.
func (s *SyncStatus) layout() (repoW int, showNewest, showCovers bool) {
	// A line is " " + repo, then a gap before every following column, so a
	// column costs its width plus one gap. Miss the margin and the status
	// of the one repo wide enough to need it — "never synced" — is the
	// thing the viewport clips off the right edge.
	avail := s.width - 1 - (colGap + colAge) - (colGap + colStatus)
	if w := avail - (colGap + colDate) - (colGap + colAge); w >= minRepoW {
		return min(w, maxRepoW), true, true
	}
	if w := avail - (colGap + colDate); w >= minRepoW {
		return min(w, maxRepoW), false, true
	}
	return min(max(avail, minRepoW), maxRepoW), false, false
}

// Failing is the count behind the app's warning badge. The page owns the
// report, so the model does not keep a second copy of it to count.
func (s *SyncStatus) Failing() int { return s.rep.Failing }

func (s *SyncStatus) rebuild() {
	if s.width <= 0 || !s.hasData {
		return
	}
	s.vp.SetWidth(s.width)
	s.vp.SetHeight(max(s.height-2, 1)) // summary line + blank
	s.vp.SetContent(s.body())
}

func (s *SyncStatus) body() string {
	repoW, showNewest, showCovers := s.layout()
	pad := strings.Repeat(" ", colGap)

	head := lipgloss.NewStyle().Bold(true).Foreground(s.theme.Primary)
	line := cellFit("REPO", repoW) + pad + cellFit("LAST SYNC", colAge)
	if showCovers {
		line += pad + cellFit("COVERS SINCE", colDate)
	}
	if showNewest {
		line += pad + cellFit("NEWEST PR", colAge)
	}
	// The status column is fitted like the others even though it is last.
	// layout reserves colStatus for it, and a status wider than that would
	// push the line past the page and lose its tail to the viewport, which
	// clips rather than complains.
	lines := []string{head.Render(line + pad + cellFit("STATUS", colStatus))}

	for _, r := range s.rep.Rows {
		row := lipgloss.NewStyle().Foreground(s.theme.Text).Render(cellFit(r.Repo, repoW)) +
			pad + s.theme.HelpDesc.Render(cellFit(r.LastSynced, colAge))
		if showCovers {
			row += pad + s.theme.HelpDesc.Render(cellFit(r.Covers, colDate))
		}
		if showNewest {
			row += pad + s.theme.HelpDesc.Render(cellFit(r.NewestPR, colAge))
		}
		lines = append(lines, row+pad+s.statusStyle(r.Status).Render(cellFit(r.Status.String(), colStatus)))
		lines = append(lines, s.detail(r)...)
	}
	// One column of left margin, matching the summary line above and the
	// padding the other pages' tables get from their cell style.
	for i, l := range lines {
		lines[i] = " " + l
	}
	return strings.Join(lines, "\n")
}

// detail is the wrapped error and hint under a failing row. Errors are
// GitHub's words at GitHub's length, so they wrap rather than truncate —
// the tail of "Could not resolve to a Repository with the name X" is the
// half naming the repo — and continuation lines are indented under the
// text rather than under the marker, so a wrapped error still reads as
// one block belonging to the row above it.
func (s *SyncStatus) detail(r doctor.Row) []string {
	if r.Error == "" {
		return nil
	}
	out := wrapIndent(r.Error, "  ⚠ ", "    ", s.width,
		lipgloss.NewStyle().Foreground(s.theme.Bad))
	if r.Hint != "" {
		out = append(out, wrapIndent(r.Hint, "    → ", "      ", s.width,
			lipgloss.NewStyle().Foreground(s.theme.Warn))...)
	}
	return out
}

// wrapIndent wraps s to the page width and returns the styled lines, the
// first behind first and the rest behind cont.
func wrapIndent(s, first, cont string, width int, style lipgloss.Style) []string {
	body := lipgloss.NewStyle().Width(max(width-text.Width(cont)-1, 20)).Render(s)
	var out []string
	for i, l := range strings.Split(body, "\n") {
		prefix := cont
		if i == 0 {
			prefix = first
		}
		out = append(out, style.Render(prefix+strings.TrimRight(l, " ")))
	}
	return out
}

// statusStyle keeps "ok" quiet: a column of green would compete with the
// two rows that need reading.
func (s *SyncStatus) statusStyle(st doctor.Status) lipgloss.Style {
	switch st {
	case doctor.StatusFailing:
		return lipgloss.NewStyle().Bold(true).Foreground(s.theme.Bad)
	case doctor.StatusNeverSynced:
		return lipgloss.NewStyle().Foreground(s.theme.Warn)
	default:
		return lipgloss.NewStyle().Foreground(s.theme.Subtle)
	}
}

// cellFit pads or truncates to exactly w display cells, so a CJK repo name
// cannot shear the columns to its right.
func cellFit(s string, w int) string {
	s = text.Truncate(s, w)
	if pad := w - text.Width(s); pad > 0 {
		return s + strings.Repeat(" ", pad)
	}
	return s
}

func (s *SyncStatus) Scroll(delta int) {
	if delta < 0 {
		s.vp.ScrollUp(-delta)
		return
	}
	s.vp.ScrollDown(delta)
}

// HandleKey claims the scrolling keys. Unlike the other pages it cannot
// leave them to the residual Update path, because there is no bubbles
// component here to hand the message to.
func (s *SyncStatus) HandleKey(msg tea.KeyPressMsg) bool {
	km := s.km
	switch {
	case key.Matches(msg, km.Down):
		s.vp.ScrollDown(1)
	case key.Matches(msg, km.Up):
		s.vp.ScrollUp(1)
	case key.Matches(msg, km.PageDown):
		s.vp.PageDown()
	case key.Matches(msg, km.PageUp):
		s.vp.PageUp()
	case key.Matches(msg, km.HalfDown):
		s.vp.HalfPageDown()
	case key.Matches(msg, km.HalfUp):
		s.vp.HalfPageUp()
	case key.Matches(msg, km.Top):
		s.vp.SetYOffset(0)
	case key.Matches(msg, km.Bottom):
		s.vp.GotoBottom()
	default:
		return false
	}
	return true
}

// HandleClick is a no-op: the page marks no click zones.
func (s *SyncStatus) HandleClick(tea.MouseClickMsg) tea.Cmd { return nil }

// Update receives messages no global handler claimed; the page holds no
// component that needs them.
func (s *SyncStatus) Update(tea.Msg) tea.Cmd { return nil }

// Export renders the report as Markdown. The team name and window come
// from the app and go unused: this view is about the cache, not about a
// team's numbers over a period.
func (s *SyncStatus) Export(_ string, _ metrics.Window) string {
	return export.SyncStatus(s.rep)
}

func (s *SyncStatus) View() string {
	if !s.hasData {
		return ""
	}
	summary := s.theme.Header.Render(" " + s.rep.Summary())
	return lipgloss.JoinVertical(lipgloss.Left, summary, "", s.vp.View())
}
