package pages

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// TestTeamStatsResizeSweep: shrinking drops columns and widening restores
// them. Before the fix, restoring a column rendered the new (wider) column
// set against the old shorter rows inside bubbles/table and panicked with
// "index out of range".
func TestTeamStatsResizeSweep(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")
	l.SetData([]metrics.Row{
		{Login: "alice", PRsMerged: 3, SizeP50: -1},
		{Login: "bob", PRsMerged: 7, SizeP50: -1},
	})
	for w := 120; w >= 10; w -= 3 {
		l.SetSize(w, 20)
		_ = l.View()
	}
	for w := 10; w <= 120; w += 3 {
		l.SetSize(w, 20)
		_ = l.View()
	}
}

// Sort-column keys must announce the change so the app can persist it;
// flipping direction must not (direction is not persisted).
func TestSortKeysEmitSortChanged(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")
	l.SetSize(120, 20)

	cmd := l.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if cmd == nil {
		t.Fatal("sort-right returned no command")
	}
	if got, ok := cmd().(SortChangedMsg); !ok || got.Key != "reviews" {
		t.Errorf("sort-right emitted %#v, want SortChangedMsg{reviews}", cmd())
	}

	if cmd := l.Update(tea.KeyPressMsg{Code: '-', Text: "-"}); cmd != nil {
		t.Error("flip sort must not emit a command")
	}
}

// A restored "member" sort must start ascending, matching what moveSort
// would have set when the user picked the column in the previous session.
func TestNewTeamStatsMemberSortAscends(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "member")
	if got := l.SortLabel(); got != "Member↑" {
		t.Errorf("SortLabel = %q, want Member↑", got)
	}
	l = NewTeamStats(&th, keys.Default(), "prs_merged")
	if got := l.SortLabel(); got != "Merged↓" {
		t.Errorf("SortLabel = %q, want Merged↓", got)
	}
}

// Members a column has nothing to show for sort last in both directions.
// The duration sort key pushed them to the top of a descending sort — the
// default — so a team with sparse data opened on a screen of dashes, and
// the size column's sentinel behaved the opposite way from the durations.
func TestNoDataRowsSortLastEitherDirection(t *testing.T) {
	rows := []metrics.Row{
		{Login: "nodata", SizeP50: -1},
		{Login: "fast", CycleTimeP50: time.Hour, TTFRP50: time.Hour, SizeP50: 5},
		{Login: "slow", CycleTimeP50: 48 * time.Hour, TTFRP50: 48 * time.Hour, SizeP50: 500},
	}
	for _, key := range []string{"cycle", "ttfr", "size"} {
		th := theme.New(true)
		l := NewTeamStats(&th, keys.Default(), key)
		l.SetSize(120, 20)
		l.SetData(append([]metrics.Row(nil), rows...))

		if got := l.rows[len(l.rows)-1].Login; got != "nodata" {
			t.Errorf("%s descending: last row = %q, want nodata", key, got)
		}
		if got := l.rows[0].Login; got != "slow" {
			t.Errorf("%s descending: first row = %q, want slow", key, got)
		}

		l.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
		if got := l.rows[len(l.rows)-1].Login; got != "nodata" {
			t.Errorf("%s ascending: last row = %q, want nodata", key, got)
		}
		if got := l.rows[0].Login; got != "fast" {
			t.Errorf("%s ascending: first row = %q, want fast", key, got)
		}
	}
}

func TestRowFor(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")
	l.SetSize(120, 20)
	l.SetData([]metrics.Row{{Login: "alice", PRsMerged: 3, SizeP50: 40}})

	if got := l.RowFor("alice"); got.PRsMerged != 3 {
		t.Errorf("RowFor(alice).PRsMerged = %d, want 3", got.PRsMerged)
	}
	// Unknown logins get the no-data sentinel, not a zero row that would
	// render size 0 instead of a dash.
	if got := l.RowFor("ghost"); got.Login != "ghost" || got.SizeP50 != -1 {
		t.Errorf("RowFor(ghost) = %+v, want the SizeP50 -1 sentinel", got)
	}
}

func TestTeamStatsSelection(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")
	l.SetSize(100, 20)
	l.SetData([]metrics.Row{
		{Login: "alice", PRsMerged: 3, SizeP50: -1},
		{Login: "bob", PRsMerged: 7, SizeP50: -1},
	})

	t.Logf("cursor=%d rows=%d", l.tbl.Cursor(), len(l.rows))
	if got := l.SelectedLogin(); got != "bob" { // bob sorts first on merged desc
		t.Errorf("SelectedLogin = %q, want bob (cursor=%d)", got, l.tbl.Cursor())
	}
	l.Scroll(1)
	if got := l.SelectedLogin(); got != "alice" {
		t.Errorf("after scroll SelectedLogin = %q, want alice (cursor=%d)", got, l.tbl.Cursor())
	}
}

func keyText(s string) tea.KeyPressMsg { return tea.KeyPressMsg{Code: []rune(s)[0], Text: s} }

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
)

func threeMembers() *TeamStats {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "member")
	l.SetSize(110, 20)
	l.SetData([]metrics.Row{
		{Login: "alice", SizeP50: -1},
		{Login: "bob", SizeP50: -1},
		{Login: "carol", SizeP50: -1},
	})
	return l
}

// / narrows the table by login as you type. Every key is text while
// typing, so q cannot quit and l cannot change the sort. enter keeps the
// narrowed table and hands the keys back; esc clears it. The selection
// follows the rows on screen, so enter on a row opens the member you see.
func TestFilterNarrowsAndSelects(t *testing.T) {
	l := threeMembers()
	if !l.HandleKey(keyText("/")) {
		t.Fatal("/ was not claimed")
	}
	for _, k := range []string{"o", "q"} { // q is text now, not quit
		if !l.HandleKey(keyText(k)) {
			t.Fatalf("%q was not claimed while typing", k)
		}
	}
	if l.Query() != "oq" {
		t.Fatalf("query = %q, want oq", l.Query())
	}
	l.HandleKey(tea.KeyPressMsg{Code: tea.KeyBackspace})
	if got := plain(l.View()); !strings.Contains(got, "/o▌") || !strings.Contains(got, "2 of 3 match") {
		t.Errorf("query line missing while typing:\n%s", got)
	}
	if got := l.SelectedLogin(); got != "bob" { // first match, in sort order
		t.Errorf("SelectedLogin = %q, want bob", got)
	}

	l.HandleKey(keyEnter)
	if l.HandleKey(keyText("j")) {
		t.Fatal("j was claimed after enter; the cursor keys should be back")
	}
	if got := plain(l.View()); strings.Contains(got, "▌") || !strings.Contains(got, "2 of 3 match") {
		t.Errorf("after enter the query should stay without the caret:\n%s", got)
	}
	l.Update(keyText("j"))
	if got := l.SelectedLogin(); got != "carol" {
		t.Errorf("SelectedLogin after j = %q, want carol", got)
	}
	if got := plain(l.Export("team", metrics.Window{})); strings.Contains(got, "alice") || !strings.Contains(got, "carol") {
		t.Errorf("export should follow the filter:\n%s", got)
	}

	if !l.HandleKey(keyEsc) {
		t.Fatal("esc with a query active was not claimed")
	}
	if l.Query() != "" || l.SelectedLogin() != "alice" {
		t.Errorf("after esc: query %q, selected %q; want cleared and back on the first row", l.Query(), l.SelectedLogin())
	}
	if l.HandleKey(keyEsc) {
		t.Error("esc with no query was claimed; it belongs to the global keymap")
	}
}

// A data reload keeps the filter: sync completion refreshes the rows every
// few minutes and must not undo what was typed.
func TestFilterSurvivesReload(t *testing.T) {
	l := threeMembers()
	l.HandleKey(keyText("/"))
	l.HandleKey(keyText("b"))
	l.HandleKey(keyEnter)
	l.SetData([]metrics.Row{
		{Login: "alice", SizeP50: -1},
		{Login: "bob", PRsMerged: 4, SizeP50: -1},
		{Login: "carol", SizeP50: -1},
	})
	if got := l.SelectedLogin(); got != "bob" {
		t.Errorf("after reload SelectedLogin = %q, want bob", got)
	}
	if got := l.RowFor("bob").PRsMerged; got != 4 {
		t.Errorf("RowFor(bob).PRsMerged = %d, want the reloaded 4", got)
	}
	l.ClearFilter()
	if l.Query() != "" || len(l.shown) != 3 {
		t.Errorf("ClearFilter left query %q and %d rows shown", l.Query(), len(l.shown))
	}
}

// The query line takes its row from the table, so the page stays exactly
// the height it was given with the filter open, typed, or cleared.
func TestFilterKeepsHeightExact(t *testing.T) {
	l := threeMembers()
	for _, h := range []int{8, 16, 30} {
		l.SetSize(110, h)
		for name, step := range map[string]func(){
			"typing":  func() { l.HandleKey(keyText("/")) },
			"query":   func() { l.HandleKey(keyText("a")); l.HandleKey(keyEnter) },
			"cleared": func() { l.HandleKey(keyEsc) },
		} {
			step()
			if got := lipgloss.Height(l.View()); got != h {
				t.Errorf("%s at height %d: page rendered %d rows", name, h, got)
			}
		}
	}
}

// The table must fill the height it was given, exactly, even when the data
// lands after the resize, which is the app's startup order. The table sizes
// its viewport as height minus the rendered header, so setting the height
// before any column existed measured against an empty header and left the
// page a row too tall (#54).
//
// Exact rather than "at most", unlike the card grids: the app gives this
// page a fixed slot and joins the status bar and help below it, so a short
// page floats the footer up off the bottom of the screen. The grids get an
// upper bound instead because they cap card rows at maxCardOuterH and
// legitimately leave space on a tall terminal.
func TestTeamStatsFitsHeightWhenDataLandsLast(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")

	rows := make([]metrics.Row, 0, 40)
	for i := 0; i < 40; i++ {
		rows = append(rows, metrics.Row{Login: fmt.Sprintf("m%02d", i), PRsMerged: i, SizeP50: -1})
	}
	// More rows than fit, then fewer: the table pads the short case, and the
	// footer stays pinned only as long as it does.
	for _, n := range []int{40, 2} {
		for _, h := range []int{8, 16, 21, 30} {
			l.SetSize(110, h)
			l.SetData(rows[:n])
			if got := lipgloss.Height(l.View()); got != h {
				t.Errorf("%d rows at height %d: table rendered %d rows", n, h, got)
			}
		}
	}
}
