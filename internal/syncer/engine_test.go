package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/gh"
)

// fixedNow pins the engine clock and every fake's timestamps to one
// instant, so horizon and watermark arithmetic can be asserted exactly.
var fixedNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// newEngine is New with the clock pinned to fixedNow and a sleep that
// returns at once; tests that care what would have been slept install a
// sleepRecorder instead.
func newEngine(store *db.Store, doer gh.Doer, opts Options) *Engine {
	e := New(store, doer, opts)
	e.now = func() time.Time { return fixedNow }
	e.sleep = func(context.Context, time.Duration) error { return nil }
	return e
}

// sleepRecorder stands in for Engine.sleep: it never waits, only logs.
type sleepRecorder struct {
	mu   sync.Mutex
	durs []time.Duration
}

func (r *sleepRecorder) sleep(_ context.Context, d time.Duration) error {
	r.mu.Lock()
	r.durs = append(r.durs, d)
	r.mu.Unlock()
	return nil
}

// pagedDoer serves a two-page PR walk plus an overflow follow-up and the
// post-walk verify probe, keyed by the query document and cursor variable.
type pagedDoer struct {
	calls atomic.Int32
	now   time.Time
}

func (d *pagedDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	d.calls.Add(1)
	iso := func(daysAgo int) string { return d.now.AddDate(0, 0, -daysAgo).Format(time.RFC3339) }

	var payload string
	switch {
	case strings.Contains(query, "PRProbe"):
		// Verify probe: matches page 1's first node and totalCount → clean.
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
			"repository": {"pullRequests": {"totalCount": 2, "nodes": [
				{"id": "PR_A", "updatedAt": %q}
			]}}}`, iso(0), iso(1))
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
				"totalCount": 2,
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
				"totalCount": 2,
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
	doer := &pagedDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

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
	if firstCalls != 4 { // page1 + overflow + page2 + clean verify probe
		t.Fatalf("calls = %d, want 4", firstCalls)
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
	if secondCalls != 4 { // page1 + overflow + page2 (finds the stop) + probe
		t.Fatalf("second run calls = %d, want 4", secondCalls)
	}
}

// deepDoer serves one page with a recent PR and one whose last activity is
// 100 days old, to exercise the backfill horizon.
type deepDoer struct{ now time.Time }

func (d *deepDoer) DoWithContext(_ context.Context, _ string, _ map[string]interface{}, resp interface{}) error {
	iso := func(daysAgo int) string { return d.now.AddDate(0, 0, -daysAgo).Format(time.RFC3339) }
	empty := `{"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}`
	payload := fmt.Sprintf(`{
		"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
		"repository": {"pullRequests": {
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [
				{"id": "PR_NEW", "number": 1, "title": "recent", "state": "OPEN", "isDraft": false,
				 "author": {"login": "alice", "__typename": "User"},
				 "createdAt": %q, "updatedAt": %q,
				 "additions": 1, "deletions": 1, "changedFiles": 1,
				 "reviews": %s, "comments": %s},
				{"id": "PR_OLD", "number": 2, "title": "ancient", "state": "MERGED", "isDraft": false,
				 "author": {"login": "alice", "__typename": "User"},
				 "createdAt": %q, "updatedAt": %q, "mergedAt": %q,
				 "additions": 1, "deletions": 1, "changedFiles": 1,
				 "reviews": %s, "comments": %s}
			]}}}`,
		iso(0), iso(35), iso(30), empty, empty, iso(105), iso(100), iso(100), empty, empty)
	return json.Unmarshal([]byte(payload), resp)
}

// TestBackfillDeepensWhenHorizonExtends: raising backfill_days on an existing
// cache must re-walk past the old horizon and store older PRs — this is what
// fills the left edge of the 90d charts for existing users.
func TestBackfillDeepensWhenHorizonExtends(t *testing.T) {
	store, target := newTestStore(t)
	doer := &deepDoer{now: fixedNow}

	shallow := newEngine(store, doer, Options{BackfillDays: 90, PageSize: 25, Concurrency: 1})
	if err := shallow.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	if prs, _ := counts(t, store); prs != 1 {
		t.Fatalf("after 90d sync PRs = %d, want 1 (the 100d-old PR is beyond the horizon)", prs)
	}

	deep := newEngine(store, doer, Options{BackfillDays: 120, PageSize: 25, Concurrency: 1})
	if err := deep.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	if prs, _ := counts(t, store); prs != 2 {
		t.Fatalf("after 120d re-sync PRs = %d, want 2 (backfill did not deepen)", prs)
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	wantHorizon := fixedNow.AddDate(0, 0, -120).Unix()
	if st.BackfillUntil == nil {
		t.Fatal("backfill_until not recorded")
	}
	if *st.BackfillUntil != wantHorizon {
		t.Fatalf("backfill_until = %d, want %d (now−120d)", *st.BackfillUntil, wantHorizon)
	}
}

func TestSyncEmitsEvents(t *testing.T) {
	store, target := newTestStore(t)
	doer := &pagedDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

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

// Regression for gh issue #36: a cancelled run whose consumer stopped
// draining must still finish. Both emit and the semaphore acquire select on
// ctx.Done, or a worker wedges holding its slot forever.
func TestSyncAllCancelUnblocksUndrainedRun(t *testing.T) {
	store, target := newTestStore(t)
	doer := &pagedDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	// Two targets on one worker slot: after the read below, worker one
	// blocks sending RepoPage on the unread channel while the dispatch loop
	// blocks acquiring the semaphore for target two.
	targets := []Target{target, {Owner: "acme", Name: "web", RepoID: target.RepoID + 1}}
	events := make(chan Event)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() { done <- engine.SyncAll(ctx, targets, events) }()

	if _, ok := (<-events).(RepoStarted); !ok {
		t.Fatal("first event was not RepoStarted")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("SyncAll returned %v, want context.Canceled", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("SyncAll did not return after cancellation: a worker or the dispatch loop is wedged")
	}
	if _, ok := <-events; ok {
		t.Fatal("events was not closed after SyncAll returned")
	}
}

// minNode renders a minimal PR node with empty reviews/comments.
func minNode(id string, number int, created, updated string) string {
	const empty = `{"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}`
	return fmt.Sprintf(`{"id": %q, "number": %d, "title": "t", "state": "OPEN", "isDraft": false,
		"author": {"login": "alice", "__typename": "User"},
		"createdAt": %q, "updatedAt": %q,
		"additions": 1, "deletions": 1, "changedFiles": 1,
		"reviews": %s, "comments": %s}`, id, number, created, updated, empty, empty)
}

func rlBlock(resetAt string) string {
	return fmt.Sprintf(`"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q}`, resetAt)
}

// mutationDoer simulates the pagination race from gh issue #37: PR_2 is
// re-updated while walk one is between pages, so PR_3 slides across the
// page boundary and is never served. The verify probe exposes the
// mutation; the retry walk serves a consistent view that includes PR_3.
type mutationDoer struct {
	now     time.Time
	prPages atomic.Int32
	probes  atomic.Int32
}

func (d *mutationDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	iso := func(hoursAgo int) string { return d.now.Add(-time.Duration(hoursAgo) * time.Hour).Format(time.RFC3339) }

	var payload string
	if strings.Contains(query, "PRProbe") {
		d.probes.Add(1)
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {"totalCount": 4, "nodes": [
			{"id": "PR_2", "updatedAt": %q}
		]}}}`, rlBlock(iso(0)), iso(0))
		return json.Unmarshal([]byte(payload), resp)
	}
	switch d.prPages.Add(1) {
	case 1: // walk 1, page 1
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 4,
			"pageInfo": {"hasNextPage": true, "endCursor": "p1"},
			"nodes": [%s, %s]}}}`, rlBlock(iso(0)),
			minNode("PR_1", 1, iso(2), iso(1)), minNode("PR_2", 2, iso(6), iso(5)))
	case 2: // walk 1, page 2 — PR_3 slid above the cursor; only PR_4 served
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 4,
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [%s]}}}`, rlBlock(iso(0)),
			minNode("PR_4", 4, iso(21), iso(20)))
	case 3: // walk 2, page 1 — consistent view after PR_2's re-update
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 4,
			"pageInfo": {"hasNextPage": true, "endCursor": "p2"},
			"nodes": [%s, %s]}}}`, rlBlock(iso(0)),
			minNode("PR_2", 2, iso(6), iso(0)), minNode("PR_1", 1, iso(2), iso(1)))
	case 4: // walk 2, page 2 — PR_3 back in view
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 4,
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [%s, %s]}}}`, rlBlock(iso(0)),
			minNode("PR_3", 3, iso(11), iso(10)), minNode("PR_4", 4, iso(21), iso(20)))
	default:
		return fmt.Errorf("unexpected PR page fetch %d", d.prPages.Load())
	}
	return json.Unmarshal([]byte(payload), resp)
}

// Regression for gh issue #37 defect 1: a PR skipped by a mid-walk reorder
// must be recovered by the verify-retry pass, and the committed watermark
// must come from the final attempt.
func TestDirtyWalkRetriesAndRecoversSkippedPR(t *testing.T) {
	store, target := newTestStore(t)
	doer := &mutationDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 2, Concurrency: 1})

	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	prs, _ := counts(t, store)
	if prs != 4 {
		t.Fatalf("PRs = %d, want 4 (retry must recover the skipped PR)", prs)
	}
	var got int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM pull_requests WHERE id = 'PR_3'`).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != 1 {
		t.Fatal("PR_3 (skipped in walk 1) was not recovered by the retry")
	}
	if pages, probes := doer.prPages.Load(), doer.probes.Load(); pages != 4 || probes != 2 {
		t.Fatalf("prPages/probes = %d/%d, want 4/2 (dirty probe → one re-walk → clean probe)", pages, probes)
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil || *st.WatermarkUpdated != doer.now.Unix() {
		t.Fatalf("watermark = %v, want %d (final attempt's max)", st.WatermarkUpdated, doer.now.Unix())
	}
	if st.LastError != nil {
		t.Fatalf("LastError = %q, want nil", *st.LastError)
	}
}

// soloDoer serves a one-page walk (and counts any probe, which must not
// happen for a single-fetch walk).
type soloDoer struct {
	now     time.Time
	prPages atomic.Int32
	probes  atomic.Int32
}

func (d *soloDoer) DoWithContext(_ context.Context, query string, _ map[string]interface{}, resp interface{}) error {
	iso := func(hoursAgo int) string { return d.now.Add(-time.Duration(hoursAgo) * time.Hour).Format(time.RFC3339) }
	var payload string
	if strings.Contains(query, "PRProbe") {
		d.probes.Add(1)
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {"totalCount": 1, "nodes": [
			{"id": "PR_S", "updatedAt": %q}
		]}}}`, rlBlock(iso(0)), iso(1))
		return json.Unmarshal([]byte(payload), resp)
	}
	d.prPages.Add(1)
	payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
		"totalCount": 1,
		"pageInfo": {"hasNextPage": false, "endCursor": ""},
		"nodes": [%s]}}}`, rlBlock(iso(0)), minNode("PR_S", 1, iso(2), iso(1)))
	return json.Unmarshal([]byte(payload), resp)
}

// A walk that fits in one fetch is a single atomic snapshot: no probe.
func TestSinglePageWalkSkipsProbe(t *testing.T) {
	store, target := newTestStore(t)
	doer := &soloDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	if pages, probes := doer.prPages.Load(), doer.probes.Load(); pages != 1 || probes != 0 {
		t.Fatalf("prPages/probes = %d/%d, want 1/0", pages, probes)
	}
	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil {
		t.Fatal("watermark not committed after single-page walk")
	}
}

// alwaysDirtyDoer serves a stable two-page walk but a probe whose
// totalCount never matches, so every verify pass reports a mutation.
type alwaysDirtyDoer struct {
	now     time.Time
	prPages atomic.Int32
	probes  atomic.Int32
}

func (d *alwaysDirtyDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	iso := func(hoursAgo int) string { return d.now.Add(-time.Duration(hoursAgo) * time.Hour).Format(time.RFC3339) }
	var payload string
	switch {
	case strings.Contains(query, "PRProbe"):
		d.probes.Add(1)
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {"totalCount": 99, "nodes": [
			{"id": "PR_X", "updatedAt": %q}
		]}}}`, rlBlock(iso(0)), iso(1))
	case vars["cursor"] == nil:
		d.prPages.Add(1)
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 3,
			"pageInfo": {"hasNextPage": true, "endCursor": "c"},
			"nodes": [%s]}}}`, rlBlock(iso(0)), minNode("PR_X", 1, iso(2), iso(1)))
	default:
		d.prPages.Add(1)
		payload = fmt.Sprintf(`{%s, "repository": {"pullRequests": {
			"totalCount": 3,
			"pageInfo": {"hasNextPage": false, "endCursor": ""},
			"nodes": [%s]}}}`, rlBlock(iso(0)), minNode("PR_Y", 2, iso(3), iso(2)))
	}
	return json.Unmarshal([]byte(payload), resp)
}

// A permanently dirty repo must stop at maxWalkAttempts and still commit:
// three independent walks each covered the whole range, and refusing to
// advance the watermark would re-walk a growing range forever.
func TestRetryCapCommitsAnyway(t *testing.T) {
	store, target := newTestStore(t)
	doer := &alwaysDirtyDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 1, Concurrency: 1})

	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}
	// Cap check precedes the probe, so the final attempt never probes.
	if pages, probes := doer.prPages.Load(), doer.probes.Load(); pages != 6 || probes != 2 {
		t.Fatalf("prPages/probes = %d/%d, want 6/2 (3 walks × 2 pages, probes only between)", pages, probes)
	}
	if prs, _ := counts(t, store); prs != 2 {
		t.Fatalf("PRs = %d, want 2", prs)
	}
	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil || *st.WatermarkUpdated != doer.now.Add(-time.Hour).Unix() {
		t.Fatalf("watermark = %v, want %d", st.WatermarkUpdated, doer.now.Add(-time.Hour).Unix())
	}
	if st.LastError != nil {
		t.Fatalf("LastError = %q, want nil", *st.LastError)
	}
}

// badTimestampDoer serves one PR whose updatedAt is absent, which decodes
// to the zero time.Time.
type badTimestampDoer struct{ now time.Time }

func (d *badTimestampDoer) DoWithContext(_ context.Context, _ string, _ map[string]interface{}, resp interface{}) error {
	iso := d.now.Add(-2 * time.Hour).Format(time.RFC3339)
	empty := `{"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}`
	payload := fmt.Sprintf(`{%s, "repository": {"pullRequests": {
		"totalCount": 1,
		"pageInfo": {"hasNextPage": false, "endCursor": ""},
		"nodes": [{"id": "PR_BAD", "number": 7, "title": "t", "state": "OPEN", "isDraft": false,
			"author": {"login": "alice", "__typename": "User"},
			"createdAt": %q,
			"additions": 1, "deletions": 1, "changedFiles": 1,
			"reviews": %s, "comments": %s}]}}}`, rlBlock(iso), iso, empty, empty)
	return json.Unmarshal([]byte(payload), resp)
}

// Regression for gh issue #37 defect 2: a missing/zero updatedAt used to
// satisfy the stop condition and commit a "clean" watermark, permanently
// truncating the repo. It must fail the repo instead.
func TestInvalidUpdatedAtFailsRepo(t *testing.T) {
	store, target := newTestStore(t)
	doer := &badTimestampDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	events := make(chan Event, 32)
	if err := engine.SyncAll(context.Background(), []Target{target}, events); err != nil {
		t.Fatal(err) // per-repo failures don't surface here by design
	}
	var repoErr error
	for ev := range events {
		if d, ok := ev.(RepoDone); ok {
			repoErr = d.Err
		}
	}
	if repoErr == nil || !strings.Contains(repoErr.Error(), "acme/api#7") {
		t.Fatalf("RepoDone.Err = %v, want error naming acme/api#7", repoErr)
	}
	if prs, _ := counts(t, store); prs != 0 {
		t.Fatalf("PRs = %d, want 0 (malformed page must not be saved)", prs)
	}
	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated != nil || st.BackfillUntil != nil {
		t.Fatalf("bookkeeping committed despite malformed data: %+v", st)
	}
	if st.LastError == nil {
		t.Fatal("LastError not recorded for the failed repo")
	}
}

// Regression for gh issue #37 defect 3: when a walk stops at the watermark
// (BackfillUntil nil but a watermark set), backfill_until must record the
// depth actually reached, not the configured horizon.
func TestBackfillRecordsActualStop(t *testing.T) {
	store, target := newTestStore(t)
	now := fixedNow
	wm := now.Add(-48 * time.Hour).Unix()
	if err := store.SetSyncState(db.SyncState{RepoID: target.RepoID, WatermarkUpdated: &wm}); err != nil {
		t.Fatal(err)
	}

	doer := &soloDoer{now: now}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})
	if err := engine.SyncAll(context.Background(), []Target{target}, nil); err != nil {
		t.Fatal(err)
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	// stopAt was watermark − 1h overlap; the old code stamped now−30d here.
	if st.BackfillUntil == nil || *st.BackfillUntil != wm-3600 {
		t.Fatalf("BackfillUntil = %v, want %d (watermark − overlap)", st.BackfillUntil, wm-3600)
	}
	if st.WatermarkUpdated == nil || *st.WatermarkUpdated != now.Add(-time.Hour).Unix() {
		t.Fatalf("watermark = %v, want the walked PR's updatedAt", st.WatermarkUpdated)
	}
}

// Regression for gh issue #38: a failed walk must record only last_error.
// The old handler read state with the error ignored and wrote the full row
// back, which could NULL the watermark and backfill floor, and it bumped
// last_synced_at even though nothing synced.
func TestFailurePreservesBookkeeping(t *testing.T) {
	store, target := newTestStore(t)
	now := fixedNow
	wm := now.Add(-48 * time.Hour).Unix()
	bf := now.AddDate(0, 0, -30).Unix()
	synced := now.Add(-24 * time.Hour).Unix()
	seed := db.SyncState{RepoID: target.RepoID, WatermarkUpdated: &wm, BackfillUntil: &bf, LastSyncedAt: &synced}
	if err := store.SetSyncState(seed); err != nil {
		t.Fatal(err)
	}

	doer := &badTimestampDoer{now: now}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})
	events := make(chan Event, 32)
	if err := engine.SyncAll(context.Background(), []Target{target}, events); err != nil {
		t.Fatal(err)
	}
	for ev := range events {
		if d, ok := ev.(RepoDone); ok && d.Err == nil {
			t.Fatal("walk unexpectedly succeeded; the fixture should fail it")
		}
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil || *st.WatermarkUpdated != wm ||
		st.BackfillUntil == nil || *st.BackfillUntil != bf {
		t.Fatalf("failure clobbered watermark/backfill: %+v", st)
	}
	if st.LastSyncedAt == nil || *st.LastSyncedAt != synced {
		t.Fatalf("LastSyncedAt = %v, want %d (must mean last successful walk)", st.LastSyncedAt, synced)
	}
	if st.LastError == nil {
		t.Fatal("LastError not recorded for the failed repo")
	}
}

// cancellingDoer cancels the run's context from inside the first fetch,
// simulating a quit or team switch mid-walk.
type cancellingDoer struct{ cancel context.CancelFunc }

func (d *cancellingDoer) DoWithContext(ctx context.Context, _ string, _ map[string]interface{}, _ interface{}) error {
	d.cancel()
	return ctx.Err()
}

// Regression for gh issue #38: cancellation is not a repo failure and must
// not write sync_state at all — the old handler persisted
// last_error = "context canceled" until the next clean sync.
func TestCancellationLeavesSyncStateUntouched(t *testing.T) {
	store, target := newTestStore(t)
	now := fixedNow
	wm := now.Add(-48 * time.Hour).Unix()
	bf := now.AddDate(0, 0, -30).Unix()
	synced := now.Add(-24 * time.Hour).Unix()
	seed := db.SyncState{RepoID: target.RepoID, WatermarkUpdated: &wm, BackfillUntil: &bf, LastSyncedAt: &synced}
	if err := store.SetSyncState(seed); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	engine := newEngine(store, &cancellingDoer{cancel: cancel}, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})
	if err := engine.SyncAll(ctx, []Target{target}, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("SyncAll = %v, want context.Canceled", err)
	}

	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.WatermarkUpdated == nil || *st.WatermarkUpdated != wm ||
		st.BackfillUntil == nil || *st.BackfillUntil != bf ||
		st.LastSyncedAt == nil || *st.LastSyncedAt != synced {
		t.Fatalf("cancellation modified bookkeeping: %+v", st)
	}
	if st.LastError != nil {
		t.Fatalf("LastError = %q, want nil after cancellation", *st.LastError)
	}
}
