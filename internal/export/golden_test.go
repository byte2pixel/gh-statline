package export

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/metrics"
)

// The Markdown these three produce is what `y` puts on the clipboard, and
// it now renders from the same Doc the CSV and JSON exports use. Pin it
// whole so a change made for a machine format cannot reword the one format
// a human reads. Trends is pinned by TestTrendsGolden.

func goldenRows() []metrics.Row {
	return []metrics.Row{
		{Login: "alice", PRsOpened: 9, PRsMerged: 7, Approved: 5, Commented: 3, ChangesReq: 1,
			Dismissed: 1, ReviewsGiven: 10, CommentsGiven: 12, CommentsRecv: 4,
			CycleTimeP50: 26 * time.Hour, TTFRP50: 45 * time.Minute, SizeP50: 120},
		// No activity at all: every median is a sentinel.
		{Login: "bob", SizeP50: -1},
	}
}

func TestTeamStatsGolden(t *testing.T) {
	want := `## platform — Last 30 days

| Member | PRs | Merged | Reviews | Approved | Commented | Changes req. | Dismissed | Comments given | Comments recv. | Cycle p50 | First review p50 | Size p50 |
|---|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|---:|
| alice | 9 | 7 | 10 | 5 | 3 | 1 | 1 | 12 | 4 | 26.0h | 45m | 120 |
| bob | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | 0 | – | – | – |
`
	if got := TeamStats("platform", metrics.Window{Label: "Last 30 days"}, goldenRows()); got != want {
		t.Errorf("TeamStats export drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestPersonGolden(t *testing.T) {
	want := `## alice — Last 30 days

PRs opened 9 · merged 7 · reviews given 10 (5 approved, 3 commented, 1 changes requested) · comments given 12 / received 4

| Repo | PRs | Merged | Reviews | Comments |
|---|---:|---:|---:|---:|
| acme/api | 6 | 5 | 7 | 9 |
| acme/web | 3 | 2 | 3 | 3 |
`
	got := Person("alice", metrics.Window{Label: "Last 30 days"}, goldenRows()[0],
		[]metrics.RepoBreakdown{
			{Repo: "acme/api", PRsOpened: 6, PRsMerged: 5, ReviewsGiven: 7, CommentsGiven: 9},
			{Repo: "acme/web", PRsOpened: 3, PRsMerged: 2, ReviewsGiven: 3, CommentsGiven: 3},
		})
	if got != want {
		t.Errorf("Person export drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// goldenReport is one repo of each status, which also exercises the
// failures section and the "never" / "—" spellings.
func goldenReport() doctor.Report {
	now := time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)
	synced := now.Add(-4 * time.Minute).Unix()
	back := time.Date(2026, 1, 12, 0, 0, 0, 0, time.UTC).Unix()
	boom := "fetching acme/web: GraphQL: Could not resolve to a Repository with the name 'acme/web'. (repository)"
	states := []db.RepoSyncState{
		{SyncState: db.SyncState{RepoID: 1, LastSyncedAt: &synced, WatermarkUpdated: &synced, BackfillUntil: &back},
			Owner: "acme", Name: "api"},
		{SyncState: db.SyncState{RepoID: 2, LastSyncedAt: &synced, LastError: &boom}, Owner: "acme", Name: "web"},
		{SyncState: db.SyncState{RepoID: 3}, Owner: "acme", Name: "cli"},
	}
	return doctor.Build(config.Team{Name: "platform"}, states, back, true, now)
}

func TestSyncStatusGolden(t *testing.T) {
	want := `## Sync status — platform

3 repos · 1 failing · 1 never synced · cache covers since 2026-01-12

| Repo | Last sync | Covers since | Newest PR | Status |
|---|---|---|---|---|
| acme/web | 4m ago | — | — | failing |
| acme/cli | never | — | — | never synced |
| acme/api | 4m ago | 2026-01-12 | 4m ago | ok |

### Failures

- **acme/web** — fetching acme/web: GraphQL: Could not resolve to a Repository with the name 'acme/web'. (repository)
  - renamed, private, or deleted — fix the repo entry in config.yml
`
	if got := SyncStatus(goldenReport()); got != want {
		t.Errorf("SyncStatus export drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
