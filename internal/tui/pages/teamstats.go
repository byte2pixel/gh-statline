// Package pages contains Statline's full-screen views.
package pages

import (
	"fmt"
	"sort"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// colDef describes one team stats column: how to render a cell, how to
// sort by it, and how reluctantly it is dropped on narrow terminals.
type colDef struct {
	key      string
	title    string
	width    int
	priority int // 0 is never dropped; higher priorities disappear first
	value    func(r metrics.Row) string
	less     func(a, b metrics.Row) bool
}

func columns() []colDef {
	num := func(v int) string { return fmt.Sprintf("%d", v) }
	return []colDef{
		{key: "member", title: "Member", width: 12, priority: 0,
			value: func(r metrics.Row) string { return r.Login },
			less:  func(a, b metrics.Row) bool { return a.Login < b.Login }},
		{key: "prs_opened", title: "PRs", width: 5, priority: 1,
			value: func(r metrics.Row) string { return num(r.PRsOpened) },
			less:  func(a, b metrics.Row) bool { return a.PRsOpened < b.PRsOpened }},
		{key: "prs_merged", title: "Merged", width: 7, priority: 1,
			value: func(r metrics.Row) string { return num(r.PRsMerged) },
			less:  func(a, b metrics.Row) bool { return a.PRsMerged < b.PRsMerged }},
		{key: "reviews", title: "Rev", width: 5, priority: 2,
			value: func(r metrics.Row) string { return num(r.ReviewsGiven) },
			less:  func(a, b metrics.Row) bool { return a.ReviewsGiven < b.ReviewsGiven }},
		{key: "approved", title: "Appr", width: 5, priority: 2,
			value: func(r metrics.Row) string { return num(r.Approved) },
			less:  func(a, b metrics.Row) bool { return a.Approved < b.Approved }},
		{key: "commented", title: "Cmnt", width: 5, priority: 3,
			value: func(r metrics.Row) string { return num(r.Commented) },
			less:  func(a, b metrics.Row) bool { return a.Commented < b.Commented }},
		{key: "changes_req", title: "ChgRq", width: 6, priority: 3,
			value: func(r metrics.Row) string { return num(r.ChangesReq) },
			less:  func(a, b metrics.Row) bool { return a.ChangesReq < b.ChangesReq }},
		{key: "cycle", title: "Cycle", width: 6, priority: 2,
			value: func(r metrics.Row) string { return fmtDur(r.CycleTimeP50) },
			less:  func(a, b metrics.Row) bool { return durKey(a.CycleTimeP50) < durKey(b.CycleTimeP50) }},
		{key: "ttfr", title: "TTFR", width: 6, priority: 3,
			value: func(r metrics.Row) string { return fmtDur(r.TTFRP50) },
			less:  func(a, b metrics.Row) bool { return durKey(a.TTFRP50) < durKey(b.TTFRP50) }},
		{key: "c_given", title: "CGivn", width: 6, priority: 4,
			value: func(r metrics.Row) string { return num(r.CommentsGiven) },
			less:  func(a, b metrics.Row) bool { return a.CommentsGiven < b.CommentsGiven }},
		{key: "c_recv", title: "CRecv", width: 6, priority: 4,
			value: func(r metrics.Row) string { return num(r.CommentsRecv) },
			less:  func(a, b metrics.Row) bool { return a.CommentsRecv < b.CommentsRecv }},
		{key: "size", title: "Size", width: 6, priority: 4,
			value: func(r metrics.Row) string {
				if r.SizeP50 < 0 {
					return "–"
				}
				return num(r.SizeP50)
			},
			less: func(a, b metrics.Row) bool { return a.SizeP50 < b.SizeP50 }},
	}
}

// TeamStats is the hero view: one sortable stat line per team member.
type TeamStats struct {
	Keys keys.KeyMap
	// Zones, when set, marks each member cell so the app can resolve row
	// clicks to logins.
	Zones *zone.Manager

	theme    *theme.Theme
	tbl      table.Model
	rows     []metrics.Row
	all      []colDef
	visible  []colDef
	sortKey  string
	sortDesc bool
	width    int
	height   int
}

func NewTeamStats(th *theme.Theme, km keys.KeyMap, sortKey string) TeamStats {
	l := TeamStats{
		Keys:     km,
		theme:    th,
		all:      columns(),
		sortKey:  sortKey,
		sortDesc: true,
	}
	if !l.validSortKey(sortKey) {
		l.sortKey = "prs_merged"
	}
	l.tbl = table.New(table.WithFocused(true))
	l.applyStyles()
	return l
}

func (l *TeamStats) validSortKey(k string) bool {
	for _, c := range l.all {
		if c.key == k {
			return true
		}
	}
	return false
}

// SetTheme swaps styles when the terminal background is (re)detected.
func (l *TeamStats) SetTheme(th *theme.Theme) {
	l.theme = th
	l.applyStyles()
}

func (l *TeamStats) applyStyles() {
	s := table.DefaultStyles()
	s.Header = l.theme.TableHeader
	s.Cell = l.theme.TableCell
	s.Selected = l.theme.Selected
	l.tbl.SetStyles(s)
}

func (l *TeamStats) SetSize(w, h int) {
	l.width, l.height = w, h
	l.tbl.SetWidth(w)
	l.tbl.SetHeight(h)
	l.rebuild()
}

func (l *TeamStats) SetData(rows []metrics.Row) {
	l.rows = rows
	l.rebuild()
}

// SortLabel describes the current sort for the status bar.
func (l *TeamStats) SortLabel() string {
	dir := "↓"
	if !l.sortDesc {
		dir = "↑"
	}
	for _, c := range l.all {
		if c.key == l.sortKey {
			return c.title + dir
		}
	}
	return ""
}

// rebuild recomputes visible columns for the width, re-sorts, and refills
// the table while keeping the cursor on the same row index.
func (l *TeamStats) rebuild() {
	if l.width <= 0 {
		return
	}
	l.visible = l.fitColumns()

	sortIdx := l.sortIdx()
	if sortIdx == -1 { // active sort column was dropped by a narrow resize
		l.sortKey, l.sortDesc = "prs_merged", true
		if l.sortIdx() == -1 {
			l.sortKey = "member"
			l.sortDesc = false
		}
		sortIdx = l.sortIdx()
	}
	col := l.visible[sortIdx]
	sort.SliceStable(l.rows, func(i, j int) bool {
		if l.sortDesc {
			return col.less(l.rows[j], l.rows[i])
		}
		return col.less(l.rows[i], l.rows[j])
	})

	tcols := make([]table.Column, len(l.visible))
	for i, c := range l.visible {
		title := c.title
		if c.key == l.sortKey {
			if l.sortDesc {
				title += "▼"
			} else {
				title += "▲"
			}
		}
		tcols[i] = table.Column{Title: title, Width: c.width}
	}
	trows := make([]table.Row, len(l.rows))
	for i, r := range l.rows {
		cells := make(table.Row, len(l.visible))
		for j, c := range l.visible {
			cells[j] = c.value(r)
		}
		if l.Zones != nil {
			cells[0] = l.Zones.Mark("row:"+r.Login, cells[0])
		}
		trows[i] = cells
	}

	cursor := l.tbl.Cursor() // -1 while the table was empty (bubbles v2)
	// Each setter re-renders the table immediately, so clear the rows before
	// swapping columns: rendering a grown column set against the old shorter
	// rows indexes out of range (resize-wider panic).
	l.tbl.SetRows(nil)
	l.tbl.SetColumns(tcols)
	l.tbl.SetRows(trows)
	if cursor >= len(trows) {
		cursor = len(trows) - 1
	}
	if cursor < 0 && len(trows) > 0 {
		cursor = 0
	}
	if cursor >= 0 {
		l.tbl.SetCursor(cursor)
	}
}

// fitColumns keeps as many columns as fit the width, dropping the highest
// priority values first; the member column flexes to the longest login.
func (l *TeamStats) fitColumns() []colDef {
	cols := make([]colDef, len(l.all))
	copy(cols, l.all)

	member := 8
	for _, r := range l.rows {
		if n := len(r.Login) + 1; n > member {
			member = n
		}
	}
	if member > 20 {
		member = 20
	}
	cols[0].width = member

	// Per-cell padding (theme.TableCell has Padding(0,1)) costs 2 per column.
	total := func(cs []colDef) int {
		t := 0
		for _, c := range cs {
			t += c.width + 2
		}
		return t
	}
	for total(cols) > l.width {
		dropIdx, dropPrio := -1, -1
		for i, c := range cols {
			if c.priority > dropPrio {
				dropPrio, dropIdx = c.priority, i
			}
		}
		if dropPrio <= 0 {
			break // only the member column left
		}
		cols = append(cols[:dropIdx], cols[dropIdx+1:]...)
	}
	return cols
}

func (l *TeamStats) sortIdx() int {
	for i, c := range l.visible {
		if c.key == l.sortKey {
			return i
		}
	}
	return -1
}

// Scroll moves the table cursor by delta rows (mouse wheel).
func (l *TeamStats) Scroll(delta int) {
	if delta < 0 {
		l.tbl.MoveUp(-delta)
	} else {
		l.tbl.MoveDown(delta)
	}
}

// SelectedLogin returns the login of the highlighted row, if any. It reads
// from the metric rows, not the rendered cell, which may carry zone markers.
func (l *TeamStats) SelectedLogin() string {
	if i := l.tbl.Cursor(); i >= 0 && i < len(l.rows) {
		return l.rows[i].Login
	}
	return ""
}

func (l TeamStats) Update(msg tea.Msg) (TeamStats, tea.Cmd) {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, l.Keys.SortLeft):
			l.moveSort(-1)
			return l, nil
		case key.Matches(msg, l.Keys.SortRight):
			l.moveSort(1)
			return l, nil
		case key.Matches(msg, l.Keys.FlipSort):
			l.sortDesc = !l.sortDesc
			l.rebuild()
			return l, nil
		}
	}
	var cmd tea.Cmd
	l.tbl, cmd = l.tbl.Update(msg)
	return l, cmd
}

func (l *TeamStats) moveSort(delta int) {
	idx := l.sortIdx()
	idx += delta
	if idx < 0 {
		idx = len(l.visible) - 1
	}
	if idx >= len(l.visible) {
		idx = 0
	}
	l.sortKey = l.visible[idx].key
	// Sensible default direction: names ascend, numbers descend.
	l.sortDesc = l.sortKey != "member"
	l.rebuild()
}

func (l TeamStats) View() string {
	if len(l.rows) == 0 {
		return l.theme.Header.Render("\n  No data yet — press s to sync.")
	}
	return l.tbl.View()
}

// fmtDur renders a duration compactly: 45s, 12m, 3.5h, 2.1d. Zero means no
// data and renders as a dash.
func fmtDur(d time.Duration) string {
	switch {
	case d == 0:
		return "–"
	case d < 90*time.Second:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < 90*time.Minute:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 36*time.Hour:
		return fmt.Sprintf("%.1fh", d.Hours())
	default:
		return fmt.Sprintf("%.1fd", d.Hours()/24)
	}
}

// durKey orders durations for sorting with "no data" (0) always last.
func durKey(d time.Duration) int64 {
	if d == 0 {
		return 1<<62 - 1
	}
	return int64(d)
}
