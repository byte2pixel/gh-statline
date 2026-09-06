package pages

import (
	"strings"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// emptyViews renders every page's empty state for one kind of team. Trends
// has two of them: nothing loaded yet, and loaded with no week the cache
// can back.
func emptyViews(noSync bool) map[string]string {
	th := theme.New(true)
	km := keys.Default()

	ts := NewTeamStats(&th, km, "prs_merged")
	ts.SetNoSync(noSync)
	ts.SetSize(100, 20)

	ch := NewCharts(&th, km)
	ch.SetNoSync(noSync)
	ch.SetSize(100, 20)

	tr := NewTrends(&th, km)
	tr.SetNoSync(noSync)
	tr.SetSize(100, 20)

	weeks := NewTrends(&th, km)
	weeks.SetNoSync(noSync)
	weeks.SetSize(100, 20)
	weeks.SetData(metrics.TrendData{})

	return map[string]string{
		"team stats":  plain(ts.View()),
		"charts":      plain(ch.View()),
		"trends":      plain(tr.View()),
		"trend weeks": plain(weeks.View()),
	}
}

// A local-only team has no sync to press s for. That key answers "sync
// disabled for this team", so no empty state may send its user there (#54).
func TestEmptyStatesFollowNoSync(t *testing.T) {
	for name, got := range emptyViews(true) {
		if !strings.Contains(got, "local-only") || !strings.Contains(got, "seed or import") {
			t.Errorf("%s (no_sync): %q offers no route to data", name, got)
		}
		if strings.Contains(got, "press s") || strings.Contains(got, "sync completes") {
			t.Errorf("%s (no_sync): %q still points at a sync that never runs", name, got)
		}
	}
	for name, got := range emptyViews(false) {
		if strings.Contains(got, "local-only") {
			t.Errorf("%s (synced team): %q calls the team local-only", name, got)
		}
	}
}
