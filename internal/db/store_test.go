package db

import (
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return NewStore(sqldb)
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// Regression for gh issue #38: a failure write must not clobber the
// incremental bookkeeping, and the success path must still clear last_error.
func TestSyncStateRoundTripAndErrorIsolation(t *testing.T) {
	s := testStore(t)
	repoID, err := s.UpsertRepo("acme", "api")
	if err != nil {
		t.Fatal(err)
	}

	wm, bf, synced := int64(1000), int64(500), int64(1100)
	full := SyncState{RepoID: repoID, WatermarkUpdated: &wm, BackfillUntil: &bf, LastSyncedAt: &synced}
	if err := s.SetSyncState(full); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSyncState(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WatermarkUpdated == nil || *got.WatermarkUpdated != wm ||
		got.BackfillUntil == nil || *got.BackfillUntil != bf ||
		got.LastSyncedAt == nil || *got.LastSyncedAt != synced ||
		got.LastError != nil {
		t.Fatalf("round trip mismatch: %+v", got)
	}

	if err := s.SetSyncError(repoID, "boom"); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSyncState(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.WatermarkUpdated == nil || *got.WatermarkUpdated != wm ||
		got.BackfillUntil == nil || *got.BackfillUntil != bf ||
		got.LastSyncedAt == nil || *got.LastSyncedAt != synced {
		t.Fatalf("SetSyncError clobbered bookkeeping: %+v", got)
	}
	if got.LastError == nil || *got.LastError != "boom" {
		t.Fatalf("LastError = %v, want \"boom\"", got.LastError)
	}

	// A clean walk writes the full state back with LastError nil — the clear
	// must stick.
	if err := s.SetSyncState(full); err != nil {
		t.Fatal(err)
	}
	got, err = s.GetSyncState(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastError != nil {
		t.Fatalf("LastError = %q, want nil after clean SetSyncState", *got.LastError)
	}
}

func TestSetSyncErrorInsertsFreshRow(t *testing.T) {
	s := testStore(t)
	repoID, err := s.UpsertRepo("acme", "api")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetSyncError(repoID, "first walk failed"); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetSyncState(repoID)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastError == nil || *got.LastError != "first walk failed" {
		t.Fatalf("LastError = %v, want the recorded message", got.LastError)
	}
	if got.WatermarkUpdated != nil || got.BackfillUntil != nil || got.LastSyncedAt != nil {
		t.Fatalf("never-synced repo grew bookkeeping from a failure: %+v", got)
	}
}

// Regression for gh issue #39: a new node id arriving for an existing
// (repo_id, number) used to violate the UNIQUE constraint and abort the
// transaction, failing every future sync of that repo. The stale row must
// be evicted, its reviews and comments with it.
func TestSavePullRequestsEvictsStaleNodeID(t *testing.T) {
	s := testStore(t)
	repoID, err := s.UpsertRepo("acme", "api")
	if err != nil {
		t.Fatal(err)
	}

	old := PullRequest{
		ID: "PR_old", RepoID: repoID, Number: 7, Author: "alice",
		Title: "original", State: "OPEN", CreatedAt: 100, UpdatedAt: 200,
		Reviews:  []Review{{ID: "REV_old", Author: "bob", State: "APPROVED", SubmittedAt: 150}},
		Comments: []IssueComment{{ID: "IC_old", Author: "bob", CreatedAt: 160}},
	}
	if err := s.SavePullRequests([]PullRequest{old}); err != nil {
		t.Fatal(err)
	}

	// Same repo and number, different node id: the repo was deleted and
	// recreated under the same owner/name, so UpsertRepo reused the row.
	replacement := old
	replacement.ID = "PR_new"
	replacement.Title = "recreated"
	replacement.Reviews = []Review{{ID: "REV_new", Author: "carol", State: "COMMENTED", SubmittedAt: 300}}
	replacement.Comments = []IssueComment{{ID: "IC_new", Author: "carol", CreatedAt: 310}}
	if err := s.SavePullRequests([]PullRequest{replacement}); err != nil {
		t.Fatal("second save with new node id:", err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM pull_requests WHERE repo_id = ? AND number = 7`, repoID); n != 1 {
		t.Fatalf("pull_requests rows for the slot: got %d, want 1", n)
	}
	var id, title string
	if err := s.DB.QueryRow(`SELECT id, title FROM pull_requests WHERE repo_id = ? AND number = 7`,
		repoID).Scan(&id, &title); err != nil {
		t.Fatal(err)
	}
	if id != "PR_new" || title != "recreated" {
		t.Errorf("surviving row = (%s, %q), want (PR_new, \"recreated\")", id, title)
	}
	for _, table := range []string{"reviews", "issue_comments"} {
		if n := count(t, s, `SELECT COUNT(*) FROM `+table+` WHERE pr_id = 'PR_old'`); n != 0 {
			t.Errorf("%s: %d orphaned rows left for the evicted PR", table, n)
		}
		if n := count(t, s, `SELECT COUNT(*) FROM `+table+` WHERE pr_id = 'PR_new'`); n != 1 {
			t.Errorf("%s: got %d rows for the new PR, want 1", table, n)
		}
	}

	// Re-saving the survivor must not trip over its own slot.
	if err := s.SavePullRequests([]PullRequest{replacement}); err != nil {
		t.Fatal("idempotent re-save:", err)
	}
}

// Delete-and-replace is why SavePullRequests exists in this shape: a review
// dismissed or deleted upstream must disappear locally on the next sync,
// with no diffing. Pin that, plus the PR-row upsert on a same-id re-save.
func TestSavePullRequestsReplacesChildren(t *testing.T) {
	s := testStore(t)
	repoID, err := s.UpsertRepo("acme", "api")
	if err != nil {
		t.Fatal(err)
	}

	pr := PullRequest{
		ID: "PR_1", RepoID: repoID, Number: 1, Author: "alice",
		Title: "first", State: "OPEN", CreatedAt: 100, UpdatedAt: 200,
		Reviews: []Review{
			{ID: "REV_1", Author: "bob", State: "APPROVED", SubmittedAt: 150},
			{ID: "REV_2", Author: "carol", State: "COMMENTED", SubmittedAt: 160},
		},
		Comments: []IssueComment{{ID: "IC_1", Author: "bob", CreatedAt: 170}},
	}
	if err := s.SavePullRequests([]PullRequest{pr}); err != nil {
		t.Fatal(err)
	}

	// Upstream, REV_2 was deleted, the comment vanished, REV_1 was
	// dismissed, and the PR merged.
	merged := int64(400)
	pr.Title = "first (merged)"
	pr.State = "MERGED"
	pr.MergedAt = &merged
	pr.UpdatedAt = 400
	pr.Reviews = []Review{{ID: "REV_1", Author: "bob", State: "DISMISSED", SubmittedAt: 150}}
	pr.Comments = nil
	if err := s.SavePullRequests([]PullRequest{pr}); err != nil {
		t.Fatal(err)
	}

	if n := count(t, s, `SELECT COUNT(*) FROM pull_requests`); n != 1 {
		t.Fatalf("pull_requests: got %d rows, want 1", n)
	}
	var title, state string
	if err := s.DB.QueryRow(`SELECT title, state FROM pull_requests WHERE id = 'PR_1'`).
		Scan(&title, &state); err != nil {
		t.Fatal(err)
	}
	if title != "first (merged)" || state != "MERGED" {
		t.Errorf("PR row = (%q, %s), want the re-saved values", title, state)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM issue_comments`); n != 0 {
		t.Errorf("issue_comments: %d rows survived, want 0", n)
	}
	var revState string
	if err := s.DB.QueryRow(`SELECT state FROM reviews WHERE id = 'REV_1'`).Scan(&revState); err != nil {
		t.Fatal(err)
	}
	if revState != "DISMISSED" {
		t.Errorf("REV_1 state = %s, want DISMISSED", revState)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM reviews`); n != 1 {
		t.Errorf("reviews: got %d rows, want 1 (REV_2 deleted upstream)", n)
	}
}

func TestDeleteTeamCascades(t *testing.T) {
	s := testStore(t)
	_, _, err := s.MirrorTeam(config.Team{
		Name:    "testers",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTeam("testers"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"teams", "team_members", "team_repos"} {
		if n := count(t, s, "SELECT COUNT(*) FROM "+table); n != 0 {
			t.Errorf("%s: %d rows left after delete", table, n)
		}
	}
	// The repos row is shared cache and must survive.
	if n := count(t, s, "SELECT COUNT(*) FROM repos"); n != 1 {
		t.Errorf("repos: got %d rows, want 1", n)
	}
}

func TestDeleteTeamLeavesOtherTeams(t *testing.T) {
	s := testStore(t)
	shared := []config.Repo{{Owner: "acme", Name: "api"}}
	for _, name := range []string{"one", "two"} {
		_, _, err := s.MirrorTeam(config.Team{
			Name:    name,
			Members: []config.Member{{Login: "alice"}},
			Repos:   shared,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteTeam("one"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM teams WHERE name = 'two'`).Scan(&id); err != nil {
		t.Fatal("surviving team missing:", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM team_members WHERE team_id = ?", id); n != 1 {
		t.Errorf("surviving team_members: got %d, want 1", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM team_repos WHERE team_id = ?", id); n != 1 {
		t.Errorf("surviving team_repos: got %d, want 1", n)
	}
}
