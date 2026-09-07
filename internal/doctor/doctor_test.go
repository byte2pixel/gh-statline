package doctor

import (
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// fixedNow pins the clock so every age in these tests is exact rather than
// "about right".
var fixedNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func ago(d time.Duration) *int64 {
	v := fixedNow.Add(-d).Unix()
	return &v
}

func at(y int, m time.Month, d int) *int64 {
	v := time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix()
	return &v
}

func str(s string) *string { return &s }

func state(owner, name string, st db.SyncState) db.RepoSyncState {
	return db.RepoSyncState{SyncState: st, Owner: owner, Name: name}
}

func team(name string) config.Team { return config.Team{Name: name} }

// rowFor finds a row by repo name; the report reorders by severity.
func rowFor(t *testing.T, r Report, repo string) Row {
	t.Helper()
	for _, row := range r.Rows {
		if row.Repo == repo {
			return row
		}
	}
	t.Fatalf("no row for %s in %+v", repo, r.Rows)
	return Row{}
}

func TestBuildClassifiesAndFormats(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "api", db.SyncState{
			WatermarkUpdated: ago(90 * time.Minute),
			BackfillUntil:    at(2026, time.January, 12),
			LastSyncedAt:     ago(4 * time.Minute),
		}),
		// Failing, but it did sync cleanly three days ago: since #38 the
		// failure path leaves last_synced_at alone, so the row can say both
		// "broken" and "the numbers are three days stale".
		state("acme", "web", db.SyncState{
			WatermarkUpdated: ago(77 * time.Hour),
			BackfillUntil:    at(2026, time.January, 12),
			LastSyncedAt:     ago(77 * time.Hour),
			LastError:        str("fetching acme/web: Could not resolve to a Repository with the name 'acme/web'"),
		}),
		state("acme", "zebra", db.SyncState{}),
	}
	rep := Build(team("platform"), states, *at(2026, time.January, 12), true, fixedNow)

	if rep.Failing != 1 || rep.NeverSynced != 1 {
		t.Errorf("counts = %d failing, %d never; want 1, 1", rep.Failing, rep.NeverSynced)
	}
	if rep.Healthy() {
		t.Error("Healthy() = true with a failing repo")
	}
	if rep.Covers != "2026-01-12" {
		t.Errorf("Covers = %q, want 2026-01-12", rep.Covers)
	}

	ok := rowFor(t, rep, "acme/api")
	if ok.Status != StatusOK || ok.Error != "" || ok.Hint != "" {
		t.Errorf("healthy row = %+v", ok)
	}
	if ok.LastSynced != "4m ago" || ok.NewestPR != "1.5h ago" || ok.Covers != "2026-01-12" {
		t.Errorf("healthy row formatting = %+v", ok)
	}

	bad := rowFor(t, rep, "acme/web")
	if bad.Status != StatusFailing {
		t.Errorf("Status = %v, want failing", bad.Status)
	}
	if bad.LastSynced != "3.2d ago" {
		t.Errorf("LastSynced = %q, want the last *successful* walk (3.2d ago)", bad.LastSynced)
	}
	if !strings.Contains(bad.Error, "Could not resolve") {
		t.Errorf("Error = %q, want the recorded message", bad.Error)
	}
	if !strings.Contains(bad.Hint, "config.yml") {
		t.Errorf("Hint = %q, want the config.yml hint for a repo that no longer resolves", bad.Hint)
	}

	never := rowFor(t, rep, "acme/zebra")
	if never.Status != StatusNeverSynced {
		t.Errorf("Status = %v, want never synced", never.Status)
	}
	if never.LastSynced != "never" || never.Covers != "—" || never.NewestPR != "—" {
		t.Errorf("never-synced row = %+v", never)
	}
}

// Worst first: a failure at the bottom of an alphabetical list is the whole
// point of the view and must not need scrolling to find.
func TestBuildSortsWorstFirst(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "alpha", db.SyncState{LastSyncedAt: ago(time.Hour), WatermarkUpdated: ago(time.Hour)}),
		state("acme", "bravo", db.SyncState{}),
		state("acme", "charlie", db.SyncState{LastSyncedAt: ago(time.Hour), LastError: str("boom")}),
		state("acme", "delta", db.SyncState{LastSyncedAt: ago(time.Hour), WatermarkUpdated: ago(time.Hour)}),
		state("acme", "echo", db.SyncState{LastSyncedAt: ago(time.Hour), LastError: str("bang")}),
	}
	rep := Build(team("platform"), states, 0, false, fixedNow)

	var got []string
	for _, r := range rep.Rows {
		got = append(got, r.Repo)
	}
	want := []string{"acme/charlie", "acme/echo", "acme/bravo", "acme/alpha", "acme/delta"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("order = %v, want failing then never-synced then ok, alphabetical inside each: %v", got, want)
	}
}

// last_error is written by GitHub, so it reaches the terminal and the
// clipboard only through Sanitize.
func TestBuildSanitizesUntrustedText(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "api\x1b]0;pwned\a", db.SyncState{
			LastSyncedAt: ago(time.Hour),
			LastError:    str("boom \x1b]0;pwned\a and \x07more"),
		}),
	}
	rep := Build(config.Team{Name: "team\x1b[31m"}, states, 0, false, fixedNow)
	row := rep.Rows[0]
	if strings.Contains(row.Error, "pwned") || strings.ContainsAny(row.Error, "\x1b\x07") {
		t.Errorf("Error leaked an escape sequence: %q", row.Error)
	}
	if !strings.Contains(row.Error, "boom") {
		t.Errorf("Error = %q, want the message text kept", row.Error)
	}
	if strings.ContainsAny(row.Repo, "\x1b\x07") {
		t.Errorf("Repo leaked an escape sequence: %q", row.Repo)
	}
	if strings.ContainsAny(rep.Team, "\x1b") {
		t.Errorf("Team leaked an escape sequence: %q", rep.Team)
	}
}

// The hint is prose-matched against GitHub's message, so it must stay off
// every other failure rather than guess.
func TestBuildHintOnlyForUnresolvableRepo(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "api", db.SyncState{LastSyncedAt: ago(time.Hour), LastError: str("dial tcp: i/o timeout")}),
	}
	rep := Build(team("platform"), states, 0, false, fixedNow)
	if rep.Rows[0].Hint != "" {
		t.Errorf("Hint = %q, want none for a transport error", rep.Rows[0].Hint)
	}
	if rep.Rows[0].Status != StatusFailing {
		t.Errorf("Status = %v, want failing", rep.Rows[0].Status)
	}
}

// A sync that just finished must not read as the no-data dash: FmtDur
// renders a zero duration as "–", which is why age() has a floor.
func TestBuildFreshSyncReadsAsJustNow(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "api", db.SyncState{LastSyncedAt: ago(2 * time.Second), WatermarkUpdated: ago(0)}),
	}
	rep := Build(team("platform"), states, 0, false, fixedNow)
	if got := rep.Rows[0].LastSynced; got != "just now" {
		t.Errorf("LastSynced = %q, want \"just now\"", got)
	}
}

func TestSummary(t *testing.T) {
	healthy := []db.RepoSyncState{
		state("acme", "api", db.SyncState{LastSyncedAt: ago(time.Hour), WatermarkUpdated: ago(time.Hour)}),
		state("acme", "web", db.SyncState{LastSyncedAt: ago(time.Hour), WatermarkUpdated: ago(time.Hour)}),
	}
	broken := []db.RepoSyncState{
		state("acme", "api", db.SyncState{LastSyncedAt: ago(time.Hour), LastError: str("boom")}),
		state("acme", "web", db.SyncState{}),
	}

	tests := []struct {
		name   string
		team   config.Team
		states []db.RepoSyncState
		floor  bool
		want   []string
	}{
		{"healthy", team("platform"), healthy, true,
			[]string{"2 repos", "all synced cleanly", "cache covers since 2026-01-12"}},
		{"failing and never", team("platform"), broken, false,
			[]string{"2 repos", "1 failing", "1 never synced", "coverage pending a full sync"}},
		{"local only", config.Team{Name: "demo", NoSync: true}, healthy, true,
			[]string{"local-only (no_sync)"}},
		{"no repos", team("empty"), nil, false,
			[]string{"no repos configured"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rep := Build(tc.team, tc.states, *at(2026, time.January, 12), tc.floor, fixedNow)
			got := rep.Summary()
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("Summary() = %q, missing %q", got, want)
				}
			}
		})
	}
}

// A team with no repos at all still reports healthy: there is nothing
// failing, and the badge must not cry wolf over an empty profile.
func TestEmptyTeamIsHealthy(t *testing.T) {
	rep := Build(team("empty"), nil, 0, false, fixedNow)
	if !rep.Healthy() || len(rep.Rows) != 0 {
		t.Errorf("empty report = %+v, want healthy with no rows", rep)
	}
}

// Never synced is not a failure, and Healthy has to say so: it drives the
// exit status of `gh statline doctor`, and a fresh install that exits
// non-zero teaches a cron job to ignore the command. The state is still
// counted and still shown, it just isn't an alarm.
func TestNeverSyncedIsNotAFailure(t *testing.T) {
	rep := Build(team("platform"), []db.RepoSyncState{
		state("acme", "api", db.SyncState{}),
	}, 0, false, fixedNow)

	if !rep.Healthy() {
		t.Error("Healthy() = false for a repo that has simply never synced")
	}
	if rep.NeverSynced != 1 || rep.Failing != 0 {
		t.Errorf("counts = %d never, %d failing; want 1, 0", rep.NeverSynced, rep.Failing)
	}
	if !strings.Contains(rep.Summary(), "1 never synced") {
		t.Errorf("Summary() = %q, want the never-synced count still reported", rep.Summary())
	}
}
