package pages

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Every page column names the export column that defines it, and that
// column has a definition to show (#121). A page column without one would
// put an empty status line behind the sort key.
func TestEveryColumnHasAnExportDoc(t *testing.T) {
	for _, c := range columns() {
		if c.export == "" {
			t.Errorf("column %s names no export column", c.key)
			continue
		}
		if export.TeamColumnDoc(c.export) == "" {
			t.Errorf("column %s maps to export column %s, which has no Doc", c.key, c.export)
		}
	}
}

// SortDoc follows the sort column, including across the keys whose page
// and export names differ.
func TestSortDocFollowsTheSortColumn(t *testing.T) {
	th := theme.New(true)
	l := NewTeamStats(&th, keys.Default(), "prs_merged")
	l.SetSize(120, 20)

	if got, want := l.SortDoc(), export.TeamColumnDoc("prs_merged"); got != want || got == "" {
		t.Errorf("SortDoc = %q, want %q", got, want)
	}
	l.Update(tea.KeyPressMsg{Code: 'l', Text: "l"}) // prs_merged → abandoned
	if got, want := l.SortDoc(), export.TeamColumnDoc("prs_closed_unmerged"); got != want || got == "" {
		t.Errorf("SortDoc after sort-right = %q, want %q", got, want)
	}
}
