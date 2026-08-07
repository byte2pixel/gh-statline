package pages

import (
	"testing"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

func TestLeaderboardSelection(t *testing.T) {
	th := theme.New(true)
	l := NewLeaderboard(&th, keys.Default(), "prs_merged")
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
