package syncer

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// pagedDoer serves a two-page PR walk plus an overflow follow-up, keyed by
// the query document and cursor variable.
type pagedDoer struct {
	calls atomic.Int32
	now   time.Time
}

func (d *pagedDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	d.calls.Add(1)
	iso := func(daysAgo int) string { return d.now.AddDate(0, 0, -daysAgo).Format(time.RFC3339) }

	var payload string
	switch {
	case strings.Contains(query, "PRReviews"):
		// Overflow continuation for PR_A: one more review page.
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
			"node": {"reviews": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": [
				{"id": "RV_extra", "author": {"login": "zoe", "__typename": "User"},
				 "state": "APPROVED", "submittedAt": %q, "comments": {"totalCount": 1}}
			]}}}`, iso(0), iso(1))
	case vars["cursor"] == nil:
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
			"repository": {"pullRequests": {
				"pageInfo": {"hasNextPage": true, "endCursor": "c1"},
				"nodes": [{
					"id": "PR_A", "number": 1, "title": "newest", "state": "OPEN", "isDraft": false,
					"author": {"login": "alice", "__typename": "User"},
					"createdAt": %q, "updatedAt": %q,
					"additions": 5, "deletions": 1, "changedFiles": 1,
					"reviews": {"totalCount": 51,
						"pageInfo": {"hasNextPage": true, "endCursor": "rc1"},
						"nodes": [{"id": "RV_1", "author": {"login": "bob", "__typename": "User"},
						           "state": "COMMENTED", "submittedAt": %q, "comments": {"totalCount": 2}}]},
					"comments": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
				}]}}}`, iso(0), iso(2), iso(1), iso(1))
	default: // page 2
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
			"repository": {"pullRequests": {
				"pageInfo": {"hasNextPage": false, "endCursor": ""},
				"nodes": [{
					"id": "PR_B", "number": 2, "title": "older", "state": "MERGED", "isDraft": false,
					"author": {"login": "bob", "__typename": "User"},
					"createdAt": %q, "updatedAt": %q, "mergedAt": %q,
					"additions": 9, "deletions": 2, "changedFiles": 2,
					"reviews": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []},
					"comments": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
				}]}}}`, iso(0), iso(10), iso(8), iso(8))
	}
	return json.Unmarshal([]byte(payload), resp)
}

func newTestStore(t *testing.T) (*db.Store, Target) {
	t.Helper()
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)
	_, repoIDs, err := store.MirrorTeam(config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{{Login: "alice"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, Target{Owner: "acme", Name: "api", RepoID: repoIDs["acme/api"]}
}

func counts(t *testing.T, store *db.Store) (prs, reviews int) {
	t.Helper()
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM pull_requests`).Scan(&prs); err != nil {
		t.Fatal(err)
	}
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM reviews`).Scan(&reviews); err != nil {
		t.Fatal(err)
	}
	return prs, reviews
}

func TestSyncIdempotentAndWatermarked(t *testing.T) {
	store, target := newTestStore(t)
	doer := &pagedDoer{now: time.Now().UTC()}
	engine := New(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	prs, reviews := counts(t, store)
	if prs != 2 {
		t.Fatalf("PRs = %d, want 2", prs)
	}
	if reviews != 2 { // RV_1 + RV_extra from the overflow follow-up
		t.Fatalf("reviews = %d, want 2 (overflow follow-up)", reviews)
	}
	firstCalls := doer.calls.Load()
	if firstCalls != 3 { // page1 + overflow + page2
		t.Fatalf("calls = %d, want 3", firstCalls)
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil || st.BackfillUntil == nil || st.LastError != nil {
		t.Fatalf("sync state incomplete: %+v", st)
	}

	// Second run: PR_A (updated 1d ago) is newer than watermark−overlap so
	// it re-syncs; PR_B on page 2 is older and hits the stop condition
	// before being re-saved. Re-saving PR_A must not duplicate anything.
	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	prs, reviews = counts(t, store)
	if prs != 2 || reviews != 2 {
		t.Fatalf("after re-sync PRs/reviews = %d/%d, want 2/2", prs, reviews)
	}
	secondCalls := doer.calls.Load() - firstCalls
	if secondCalls != 3 { // page1 + overflow + page2 (needed to find the stop)
		t.Fatalf("second run calls = %d, want 3", secondCalls)
	}
}

func TestSyncEmitsEvents(t *testing.T) {
	store, target := newTestStore(t)
	doer := &pagedDoer{now: time.Now().UTC()}
	engine := New(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	events := make(chan Event, 32)
	go func() { _ = engine.SyncAll(context.Background(), []Target{target}, events) }()

	var started, done, complete bool
	for ev := range events {
		switch ev.(type) {
		case RepoStarted:
			started = true
		case RepoDone:
			done = true
		case Complete:
			complete = true
		}
	}
	if !started || !done || !complete {
		t.Fatalf("missing events: started=%v done=%v complete=%v", started, done, complete)
	}
}
