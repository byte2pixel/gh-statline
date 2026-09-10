// Package pages contains Statline's full-screen views.
package pages

import (
	"fmt"
	"sort"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/table"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
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
	// noData marks rows this column has nothing to show for. They sort last
	// whichever way the column is sorted, so flipping direction never
	// promotes a screen of dashes over the members who have numbers.
	noData func(r metrics.Row) bool
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
		{key: "dismissed", title: "Dism", width: 5, priority: 4,
			value: func(r metrics.Row) string { return num(r.Dismissed) },
			less:  func(a, b metrics.Row) bool { return a.Dismissed < b.Dismissed }},
		{key: "cycle", title: "Cycle", width: 6, priority: 2,
			value:  func(r metrics.Row) string { return metrics.FmtDur(r.CycleTimeP50) },
			less:   func(a, b metrics.Row) bool { return a.CycleTimeP50 < b.CycleTimeP50 },
			noData: func(r metrics.Row) bool { return r.CycleTimeP50 == 0 }},
		{key: "ttfr", title: "TTFR", width: 6, priority: 3,
			value:  func(r metrics.Row) string { return metrics.FmtDur(r.TTFRP50) },
			less:   func(a, b metrics.Row) bool { return a.TTFRP50 < b.TTFRP50 },
			noData: func(r metrics.Row) bool { return r.TTFRP50 == 0 }},
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
			less:   func(a, b metrics.Row) bool { return a.SizeP50 < b.SizeP50 },
			noData: func(r metrics.Row) bool { return r.SizeP50 < 0 }},
	}
}

// SortChangedMsg announces a user-initiated sort-column change so the app
// can persist it. Resize-forced fallbacks in rebuild never emit it.
type SortChangedMsg struct{ Key string }

// MemberChosenMsg announces a click on a member row so the app can open
// that member's drill-down.
type MemberChosenMsg struct{ Login string }

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
	noSync   bool
	width    int
	height   int
	// query narrows the table to logins containing it, case-insensitive;
	// shown indexes rows through it, in sort order, and is what the table
	// renders. filtering is set while the query is being typed: entered
	// with /, left with enter (keeping the query) or esc (clearing it), and
	// while it is set every key is text. A thirty-eight member team needs
	// this to reach a row without paging; the numbers are untouched.
	query     string
	filtering bool
	shown     []int
}

func NewTeamStats(th *theme.Theme, km keys.KeyMap, sortKey string) *TeamStats {
	l := &TeamStats{
		Keys:    km,
		theme:   th,
		all:     columns(),
		sortKey: sortKey,
	}
	if !l.validSortKey(sortKey) {
		l.sortKey = "prs_merged"
	}
	// Same default direction moveSort applies: names ascend, numbers descend.
	l.sortDesc = l.sortKey != "member"
	l.tbl = table.New(table.WithFocused(true))
	l.tbl.SetStyles(tableStyles(l.theme))
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
	l.tbl.SetStyles(tableStyles(l.theme))
}

// tableStyles is the one bubbles table skin: the team and person tables
// wear the same header, cell, and selection tokens.
func tableStyles(th *theme.Theme) table.Styles {
	s := table.DefaultStyles()
	s.Header = th.TableHeader
	s.Cell = th.TableCell
	s.Selected = th.Selected
	return s
}

func (l *TeamStats) SetSize(w, h int) {
	l.width, l.height = w, h
	l.tbl.SetWidth(w)
	l.rebuild() // sets the height too, which depends on the header
}

func (l *TeamStats) SetData(rows []metrics.Row) {
	l.rows = rows
	l.rebuild()
}

// SetNoSync marks the active team local-only, which changes what the empty
// state tells the user to do next.
func (l *TeamStats) SetNoSync(v bool) { l.noSync = v }

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

// rebuild recomputes visible columns for the width, re-sorts, applies the
// login filter, and refills the table while keeping the cursor on the same
// row index.
func (l *TeamStats) rebuild() {
	if l.width <= 0 {
		l.shown = nil // the table has no rows yet either
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
		a, b := l.rows[i], l.rows[j]
		if col.noData != nil {
			switch na, nb := col.noData(a), col.noData(b); {
			case na != nb:
				return nb // rows with a value always precede the dashes
			case na:
				return false // neither has one: leave them in stable order
			}
		}
		if l.sortDesc {
			return col.less(b, a)
		}
		return col.less(a, b)
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
	l.shown = l.matches()
	trows := make([]table.Row, len(l.shown))
	for i, idx := range l.shown {
		r := l.rows[idx]
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
	// Runs last, and on every rebuild. The table sizes its viewport as
	// height minus the rendered header, so a height set before the columns
	// existed measured against an empty header and left the page one row
	// taller than the app gave it, enough to push the help footer off
	// screen. The query line, when shown, takes its row from the table.
	l.tbl.SetHeight(max(l.height-l.queryRows(), 1))
}

// matches lists the rows the query keeps, as indices into rows.
func (l *TeamStats) matches() []int {
	q := strings.ToLower(l.query)
	out := make([]int, 0, len(l.rows))
	for i, r := range l.rows {
		if q == "" || strings.Contains(strings.ToLower(r.Login), q) {
			out = append(out, i)
		}
	}
	return out
}

// shownRows is what the table displays, in order: every row, or the ones
// the query keeps.
func (l *TeamStats) shownRows() []metrics.Row {
	if l.query == "" {
		return l.rows
	}
	out := make([]metrics.Row, 0, len(l.shown))
	for _, i := range l.shown {
		out = append(out, l.rows[i])
	}
	return out
}

func (l *TeamStats) showQuery() bool { return l.filtering || l.query != "" }

// queryRows is the height the query line takes from the table.
func (l *TeamStats) queryRows() int {
	if l.showQuery() {
		return 1
	}
	return 0
}

// Query is the login filter in force; empty when the table is unfiltered.
func (l *TeamStats) Query() string { return l.query }

// setQuery replaces the query and puts the cursor on the first match: the
// row it sat on may not be in the new list at all.
func (l *TeamStats) setQuery(q string) {
	l.query = q
	l.rebuild()
	if len(l.shown) > 0 {
		l.tbl.SetCursor(0)
	}
}

// ClearFilter drops the login filter. The app calls it on a team switch: a
// query typed against one team's logins means nothing against another's.
func (l *TeamStats) ClearFilter() {
	l.filtering = false
	l.setQuery("")
}

// typeQuery edits the query while filtering. Every printable key goes into
// it, the sort and quit keys included, so none of them can fire while a
// login is being typed. Only the arrows still move the cursor.
func (l *TeamStats) typeQuery(msg tea.KeyPressMsg) {
	switch msg.String() {
	case "esc":
		l.filtering = false
		l.setQuery("")
	case "enter":
		l.filtering = false // keep the narrowed table; the cursor keys take over
		l.rebuild()         // the caret leaves the query line
	case "backspace":
		if r := []rune(l.query); len(r) > 0 {
			l.setQuery(string(r[:len(r)-1]))
		}
	case "up":
		l.tbl.MoveUp(1)
	case "down":
		l.tbl.MoveDown(1)
	default:
		if msg.Text != "" && !text.HasControls(msg.Text) {
			l.setQuery(l.query + msg.Text)
		}
	}
}

// queryLine echoes the query with a caret while it is being typed, and
// says how much of the table it keeps.
func (l *TeamStats) queryLine() string {
	q := "/" + text.Sanitize(l.query)
	if l.filtering {
		q += "▌"
	}
	tail := " · type a login · enter keeps · esc clears"
	if l.query != "" {
		tail = fmt.Sprintf(" · %d of %d match(es)", len(l.shown), len(l.rows))
	}
	line := lipgloss.NewStyle().Foreground(l.theme.Accent).Render(q) + l.theme.HelpDesc.Render(tail)
	return lipgloss.NewStyle().MaxWidth(l.width).Render(line)
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

// RowFor returns login's stat line, or a no-data sentinel row when the
// login isn't in the current window's data.
func (l *TeamStats) RowFor(login string) metrics.Row {
	for _, r := range l.rows {
		if r.Login == login {
			return r
		}
	}
	return metrics.Row{Login: login, SizeP50: -1}
}

// Export renders the stat lines on screen as Markdown. A filtered table
// exports the rows it shows: the logins name the subset, so a pasted table
// of three people cannot pass for the team.
func (l *TeamStats) Export(team string, w metrics.Window) string {
	return export.TeamStats(team, w, l.shownRows())
}

// SelectedLogin returns the login of the highlighted row, if any. It reads
// from the metric rows, not the rendered cell, which may carry zone markers.
func (l *TeamStats) SelectedLogin() string {
	if i := l.tbl.Cursor(); i >= 0 && i < len(l.shown) {
		return l.rows[l.shown[i]].Login
	}
	return ""
}

// HandleKey claims the keys the login filter owns: / to start typing,
// every key while typing, and esc while a query is narrowing the table.
// The sort and cursor keys deliberately ride the residual Update path
// after the global keymap instead.
func (l *TeamStats) HandleKey(msg tea.KeyPressMsg) bool {
	switch {
	case l.filtering:
		l.typeQuery(msg)
	case key.Matches(msg, l.Keys.Filter):
		if len(l.rows) == 0 {
			return false // nothing to narrow, and the empty state hides the query line
		}
		l.filtering = true
		l.rebuild()
	case l.query != "" && key.Matches(msg, l.Keys.Back):
		// A narrowed table is the first thing esc undoes, as in the repo
		// picker; unfiltered, esc stays the global back key.
		l.setQuery("")
	default:
		return false
	}
	return true
}

// HandleClick resolves a click against the member-row zones; a hit asks
// the app to open that member, like the enter key on their row. Only the
// rows on screen have zones.
func (l *TeamStats) HandleClick(msg tea.MouseClickMsg) tea.Cmd {
	if l.Zones == nil {
		return nil
	}
	for _, r := range l.shownRows() {
		if l.Zones.Get("row:" + r.Login).InBounds(msg) {
			return func() tea.Msg { return MemberChosenMsg{Login: r.Login} }
		}
	}
	return nil
}

func (l *TeamStats) Update(msg tea.Msg) tea.Cmd {
	if msg, ok := msg.(tea.KeyPressMsg); ok {
		switch {
		case key.Matches(msg, l.Keys.Left):
			l.moveSort(-1)
			return l.emitSortChanged()
		case key.Matches(msg, l.Keys.Right):
			l.moveSort(1)
			return l.emitSortChanged()
		case key.Matches(msg, l.Keys.FlipSort):
			l.sortDesc = !l.sortDesc
			l.rebuild()
			return nil
		}
	}
	var cmd tea.Cmd
	l.tbl, cmd = l.tbl.Update(msg)
	return cmd
}

func (l *TeamStats) emitSortChanged() tea.Cmd {
	k := l.sortKey
	return func() tea.Msg { return SortChangedMsg{Key: k} }
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

func (l *TeamStats) View() string {
	if len(l.rows) == 0 {
		return l.theme.Header.Render(noDataHint(l.noSync))
	}
	if !l.showQuery() {
		return l.tbl.View()
	}
	return lipgloss.JoinVertical(lipgloss.Left, l.queryLine(), l.tbl.View())
}
