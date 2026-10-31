package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// factsDoer serves a one-page walk of one PR carrying every kind of review
// the classification pass must tell apart, then answers the ReviewFacts
// lookup for the candidates among them and records which ids were asked.
// factsErr, when set, fails the lookup instead.
type factsDoer struct {
	now      time.Time
	mu       sync.Mutex
	asked    [][]string
	factsErr error
}

func (d *factsDoer) DoWithContext(_ context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	iso := func(daysAgo int, plus time.Duration) string {
		return d.now.AddDate(0, 0, -daysAgo).Add(plus).Format(time.RFC3339)
	}
	var payload string
	switch {
	case strings.Contains(query, "ReviewFacts"):
		ids, _ := vars["ids"].([]string)
		d.mu.Lock()
		d.asked = append(d.asked, append([]string(nil), ids...))
		err := d.factsErr
		d.mu.Unlock()
		if err != nil {
			return err
		}
		var nodes []string
		for _, id := range ids {
			switch id {
			case "RV_reply":
				nodes = append(nodes, `{"id": "RV_reply", "body": "", "comments": {"nodes": [{"replyTo": {"id": "PRRC_9"}}]}}`)
			case "RV_body":
				nodes = append(nodes, `{"id": "RV_body", "body": "nit", "comments": {"nodes": [{"replyTo": null}]}}`)
			default:
				nodes = append(nodes, "null")
			}
		}
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 2, "remaining": 5000, "resetAt": %q},
			"nodes": [%s]}`, iso(0, 0), strings.Join(nodes, ","))
	default:
		// One page: no probe, no overflow. Four candidates by submission:
		// RV_gone (newest), RV_reply, RV_body; RV_multi and RV_appr never
		// qualify; RV_self is alice replying on her own PR, which is a
		// candidate like any other (the metrics exclude it as self).
		payload = fmt.Sprintf(`{
			"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": %q},
			"repository": {"pullRequests": {
				"totalCount": 1,
				"pageInfo": {"hasNextPage": false, "endCursor": ""},
				"nodes": [{
					"id": "PR_A", "number": 1, "title": "one", "state": "OPEN", "isDraft": false,
					"author": {"login": "alice", "__typename": "User"},
					"createdAt": %q, "updatedAt": %q,
					"additions": 5, "deletions": 1, "changedFiles": 1,
					"reviews": {"totalCount": 5, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": [
						{"id": "RV_body", "author": {"login": "bob", "__typename": "User"},
						 "state": "COMMENTED", "submittedAt": %q, "comments": {"totalCount": 1}},
						{"id": "RV_reply", "author": {"login": "bob", "__typename": "User"},
						 "state": "COMMENTED", "submittedAt": %q, "comments": {"totalCount": 1}},
						{"id": "RV_gone", "author": {"login": "carol", "__typename": "User"},
						 "state": "COMMENTED", "submittedAt": %q, "comments": {"totalCount": 1}},
						{"id": "RV_multi", "author": {"login": "bob", "__typename": "User"},
						 "state": "COMMENTED", "submittedAt": %q, "comments": {"totalCount": 3}},
						{"id": "RV_appr", "author": {"login": "bob", "__typename": "User"},
						 "state": "APPROVED", "submittedAt": %q, "comments": {"totalCount": 1}}
					]},
					"comments": {"totalCount": 0, "pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}
				}]}}}`, iso(0, 0), iso(2, 0), iso(1, 0),
			iso(2, time.Hour), iso(2, 2*time.Hour), iso(2, 3*time.Hour), iso(2, 4*time.Hour), iso(2, 5*time.Hour))
	}
	return json.Unmarshal([]byte(payload), resp)
}

func (d *factsDoer) askedIDs() [][]string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.asked
}

func runSync(t *testing.T, e *Engine, target Target) []Event {
	t.Helper()
	events := make(chan Event, 64)
	go func() { _ = e.SyncAll(context.Background(), []Target{target}, events) }()
	var got []Event
	for ev := range events {
		got = append(got, ev)
	}
	return got
}

func classifyEvents(evs []Event) (progress []Classifying, complete Complete) {
	for _, ev := range evs {
		switch ev := ev.(type) {
		case Classifying:
			progress = append(progress, ev)
		case Complete:
			complete = ev
		}
	}
	return progress, complete
}

func threadReplies(t *testing.T, e *Engine) []string {
	t.Helper()
	rs, err := e.Store.DB.Query(`SELECT id FROM thread_replies ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	return out
}

// After the walk the pass asks for exactly the candidates, newest first,
// writes a facts row for each including the one the API no longer knows,
// reports its progress, and has nothing left to ask on the next run even
// though that run replaced the PR's reviews.
func TestClassifyFetchesCandidatesOnce(t *testing.T) {
	store, target := newTestStore(t)
	doer := &factsDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	progress, complete := classifyEvents(runSync(t, engine, target))

	if want := [][]string{{"RV_gone", "RV_reply", "RV_body"}}; !reflect.DeepEqual(doer.askedIDs(), want) {
		t.Errorf("asked = %v, want %v", doer.askedIDs(), want)
	}
	if want := []Classifying{{Done: 0, Total: 3}, {Done: 3, Total: 3}}; !reflect.DeepEqual(progress, want) {
		t.Errorf("progress = %+v, want %+v", progress, want)
	}
	if complete.Unclassified != 0 {
		t.Errorf("Complete.Unclassified = %d, want 0", complete.Unclassified)
	}
	if got := threadReplies(t, engine); !reflect.DeepEqual(got, []string{"RV_reply"}) {
		t.Errorf("thread_replies = %v, want [RV_reply]", got)
	}
	var facts int
	if err := store.DB.QueryRow(`SELECT COUNT(*) FROM review_facts`).Scan(&facts); err != nil {
		t.Fatal(err)
	}
	if facts != 3 {
		t.Errorf("review_facts rows = %d, want 3 (the unknown review included)", facts)
	}

	progress, complete = classifyEvents(runSync(t, engine, target))
	if len(doer.askedIDs()) != 1 {
		t.Errorf("second run asked again: %v", doer.askedIDs())
	}
	if len(progress) != 0 || complete.Unclassified != 0 {
		t.Errorf("second run: progress %+v, unclassified %d; want none", progress, complete.Unclassified)
	}
}

// A failed lookup is reported, not fatal: the walk's bookkeeping stands,
// nothing is written, the remainder is counted, and the next run resumes.
func TestClassifyFailureIsReportedAndResumed(t *testing.T) {
	store, target := newTestStore(t)
	doer := &factsDoer{now: fixedNow, factsErr: errors.New("boom")}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	evs := runSync(t, engine, target)
	progress, complete := classifyEvents(evs)
	for _, ev := range evs {
		if done, ok := ev.(RepoDone); ok && done.Err != nil {
			t.Errorf("the repo failed over the classification pass: %v", done.Err)
		}
	}
	last := progress[len(progress)-1]
	if last.Err == nil || !strings.Contains(last.Err.Error(), "boom") {
		t.Errorf("last progress event = %+v, want the lookup error", last)
	}
	if complete.Unclassified != 3 || complete.Failed != 0 {
		t.Errorf("Complete = %+v, want 3 unclassified and no failed repo", complete)
	}
	if st, _ := store.GetSyncState(target.RepoID); st.LastSyncedAt == nil {
		t.Error("the walk's bookkeeping was not committed")
	}
	if got := threadReplies(t, engine); len(got) != 0 {
		t.Errorf("thread_replies = %v after a failed lookup", got)
	}

	doer.mu.Lock()
	doer.factsErr = nil
	doer.mu.Unlock()
	_, complete = classifyEvents(runSync(t, engine, target))
	if complete.Unclassified != 0 {
		t.Errorf("Complete.Unclassified after resume = %d, want 0", complete.Unclassified)
	}
	if got := threadReplies(t, engine); !reflect.DeepEqual(got, []string{"RV_reply"}) {
		t.Errorf("thread_replies after resume = %v, want [RV_reply]", got)
	}
}

// A cancelled run never starts the pass; the next run does.
func TestClassifySkippedOnCancel(t *testing.T) {
	store, target := newTestStore(t)
	doer := &factsDoer{now: fixedNow}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	events := make(chan Event, 64)
	_ = engine.SyncAll(ctx, []Target{target}, events)
	for range events {
	}
	if len(doer.askedIDs()) != 0 {
		t.Errorf("a cancelled run looked up facts: %v", doer.askedIDs())
	}
}
