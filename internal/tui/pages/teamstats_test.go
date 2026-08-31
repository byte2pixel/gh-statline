package pages

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

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

	l2, cmd := l.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	if cmd == nil {
		t.Fatal("sort-right returned no command")
	}
	if got, ok := cmd().(SortChangedMsg); !ok || got.Key != "reviews" {
		t.Errorf("sort-right emitted %#v, want SortChangedMsg{reviews}", cmd())
	}

	if _, cmd := l2.Update(tea.KeyPressMsg{Code: '-', Text: "-"}); cmd != nil {
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

		flipped, _ := l.Update(tea.KeyPressMsg{Code: '-', Text: "-"})
		if got := flipped.rows[len(flipped.rows)-1].Login; got != "nodata" {
			t.Errorf("%s ascending: last row = %q, want nodata", key, got)
		}
		if got := flipped.rows[0].Login; got != "fast" {
			t.Errorf("%s ascending: first row = %q, want fast", key, got)
		}
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
