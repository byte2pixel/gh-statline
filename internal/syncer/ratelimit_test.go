package syncer

// Regression coverage for gh issue #40: the retry loop must honour the
// server's own retry delay, fail permanent 403s at once, re-ask transient
// GraphQL faults, feed the shared limiter from error replies, announce each
// pause once, and cap the worker count.

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/gh"
)

// secondaryLimitErr is a secondary rate limit carrying the server's own
// retry delay, as GitHub sends it.
func secondaryLimitErr(retryAfter string) error {
	h := http.Header{}
	h.Set("Retry-After", retryAfter)
	return &api.HTTPError{StatusCode: 403, Message: "You have exceeded a secondary rate limit", Headers: h}
}

// samlErr is a 403 that no retry can fix: the quota is healthy and the
// message names an authorization problem.
func samlErr() error {
	h := http.Header{}
	h.Set("X-Ratelimit-Remaining", "4999")
	return &api.HTTPError{StatusCode: 403, Message: "Resource protected by organization SAML enforcement.", Headers: h}
}

// internalErr is the 200-with-errors reply GitHub returns when one nested
// field of a heavy query times out on its side.
func internalErr() error {
	return &api.GraphQLError{Errors: []api.GraphQLErrorItem{{
		Type: "INTERNAL", Message: "Something went wrong while executing your query.",
	}}}
}

func TestWithRetryClassifiesErrors(t *testing.T) {
	// Defect 2: a 403 that is not a rate limit (SAML, scope, IP allowlist)
	// used to burn the whole ladder — over a minute of sleeping — before
	// failing the repo with the same error.
	t.Run("permanent 403 fails at once", func(t *testing.T) {
		e, lim, rec := retryHarness()
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
			calls++
			return samlErr()
		})
		var httpErr *api.HTTPError
		if !errors.As(err, &httpErr) || httpErr.StatusCode != 403 {
			t.Fatalf("err = %v, want the 403 itself", err)
		}
		if calls != 1 || len(rec.durs) != 0 {
			t.Fatalf("calls = %d, sleeps = %v; want 1 and none", calls, rec.durs)
		}
	})

	// Defect 3: GitHub answers a heavy nested query with 200 + an INTERNAL
	// error when one field times out; that used to fail the whole repo for
	// the run instead of being re-asked.
	t.Run("transient GraphQL error is retried", func(t *testing.T) {
		e, lim, rec := retryHarness()
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
			calls++
			if calls == 1 {
				return internalErr()
			}
			return nil
		})
		if err != nil || calls != 2 || len(rec.durs) != 1 || !inLadderStep(rec.durs[0], 0) {
			t.Fatalf("err = %v, calls = %d, sleeps = %v; want nil, 2, one first-step backoff", err, calls, rec.durs)
		}
	})

	// Defects 1 and 4: the server's Retry-After replaces the 2s ladder step,
	// the pause is announced, and it parks the shared limiter so a second
	// worker waits it out instead of hammering.
	t.Run("honours Retry-After and parks every worker", func(t *testing.T) {
		e, lim, rec := retryHarness()
		events := make(chan Event, 4)
		calls := 0
		err := e.withRetry(context.Background(), lim, events, func() error {
			calls++
			if calls == 1 {
				return secondaryLimitErr("90")
			}
			return nil
		})
		if err != nil || calls != 2 {
			t.Fatalf("err = %v, calls = %d; want nil, 2", err, calls)
		}
		if len(rec.durs) != 1 || rec.durs[0] != 90*time.Second {
			t.Fatalf("sleeps = %v, want [90s] (the server's delay, not the ladder's 2s)", rec.durs)
		}
		until := fixedNow.Add(90 * time.Second)
		select {
		case ev := <-events:
			if rl, ok := ev.(RateLimited); !ok || !rl.Until.Equal(until) {
				t.Fatalf("event = %+v, want RateLimited until %v", ev, until)
			}
		default:
			t.Fatal("no RateLimited event for the mandated wait")
		}

		// A second worker arriving during the pause sits it out too, and
		// does not announce the same pause again.
		if err := lim.wait(context.Background(), events); err != nil {
			t.Fatal(err)
		}
		if len(rec.durs) != 2 || rec.durs[1] != 90*time.Second {
			t.Fatalf("sleeps = %v, want a second 90s wait for the other worker", rec.durs)
		}
		if len(events) != 0 {
			t.Fatalf("pause announced twice: %+v", <-events)
		}
	})

	t.Run("ladder step wins over a shorter mandated wait", func(t *testing.T) {
		e, lim, rec := retryHarness()
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
			calls++
			if calls == 1 {
				return secondaryLimitErr("1")
			}
			return nil
		})
		if err != nil || len(rec.durs) != 1 || !inLadderStep(rec.durs[0], 0) {
			t.Fatalf("err = %v, sleeps = %v; want one first-step backoff (2s beats a 1s Retry-After)", err, rec.durs)
		}
	})

	t.Run("sits out a pause another worker set", func(t *testing.T) {
		e, lim, rec := retryHarness()
		lim.pause(fixedNow.Add(5 * time.Minute))
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
			calls++
			if calls == 1 {
				return retryableErr()
			}
			return nil
		})
		// After the 502's own backoff the retry must not fire into a window
		// the limiter already knows is rate-limited.
		if err != nil || len(rec.durs) != 2 || !inLadderStep(rec.durs[0], 0) || rec.durs[1] != 5*time.Minute {
			t.Fatalf("err = %v, sleeps = %v; want [first-step backoff, 5m shared pause]", err, rec.durs)
		}
	})
}

// The limiter pauses every worker until the quota resets once any response
// reports it running low, and reports each pause as one event.
func TestLimiterPausesUntilReset(t *testing.T) {
	rec := &sleepRecorder{}
	lim := &limiter{now: func() time.Time { return fixedNow }, sleep: rec.sleep}
	events := make(chan Event, 4)
	ctx := context.Background()

	// A healthy quota, or a response without a rateLimit block, never pauses.
	lim.note(gh.RateLimit{Remaining: 5000, ResetAt: fixedNow.Add(time.Hour)})
	lim.note(gh.RateLimit{Remaining: 0}) // block absent: zero ResetAt
	if err := lim.wait(ctx, events); err != nil || len(rec.durs) != 0 {
		t.Fatalf("healthy wait: err = %v, sleeps = %v; want none", err, rec.durs)
	}

	// Below lowWater: sleep exactly until the reset, and say so.
	reset := fixedNow.Add(90 * time.Second)
	lim.note(gh.RateLimit{Remaining: lowWater - 1, ResetAt: reset})
	if err := lim.wait(ctx, events); err != nil {
		t.Fatal(err)
	}
	if len(rec.durs) != 1 || rec.durs[0] != 90*time.Second {
		t.Fatalf("sleeps = %v, want [90s]", rec.durs)
	}
	select {
	case ev := <-events:
		if rl, ok := ev.(RateLimited); !ok || !rl.Until.Equal(reset) {
			t.Fatalf("event = %+v, want RateLimited until %v", ev, reset)
		}
	default:
		t.Fatal("no RateLimited event emitted for the pause")
	}

	// Another worker hitting the same pause waits but stays quiet: three
	// workers used to print three "rate limited" lines per pause.
	if err := lim.wait(ctx, events); err != nil {
		t.Fatal(err)
	}
	if len(rec.durs) != 2 || len(events) != 0 {
		t.Fatalf("second waiter: sleeps = %v, events = %d; want a second sleep and no new event", rec.durs, len(events))
	}

	// An earlier reset from another response never shortens the pause; a
	// later one extends it, and the new target is announced.
	lim.note(gh.RateLimit{Remaining: 1, ResetAt: fixedNow.Add(30 * time.Second)})
	lim.note(gh.RateLimit{Remaining: 1, ResetAt: fixedNow.Add(2 * time.Minute)})
	if err := lim.wait(ctx, events); err != nil {
		t.Fatal(err)
	}
	if got := rec.durs[len(rec.durs)-1]; got != 2*time.Minute {
		t.Fatalf("sleep after competing resets = %v, want 2m (the latest)", got)
	}
	select {
	case ev := <-events:
		if rl, ok := ev.(RateLimited); !ok || !rl.Until.Equal(fixedNow.Add(2*time.Minute)) {
			t.Fatalf("event = %+v, want RateLimited until the extended target", ev)
		}
	default:
		t.Fatal("extended pause was not announced")
	}

	// Once the clock reaches the reset, waits are free again.
	lim.now = func() time.Time { return fixedNow.Add(2 * time.Minute) }
	before := len(rec.durs)
	if err := lim.wait(ctx, events); err != nil || len(rec.durs) != before {
		t.Fatalf("wait at reset: err = %v, sleeps = %v; want no new sleep", err, rec.durs)
	}
}

// Defect 5: sync.concurrency had no ceiling, and 500 workers is a direct
// route to a secondary-rate-limit block.
func TestNewClampsConcurrency(t *testing.T) {
	if got := New(nil, nil, Options{Concurrency: 500}).Opts.Concurrency; got != maxConcurrency {
		t.Fatalf("Concurrency = %d, want %d (clamped)", got, maxConcurrency)
	}
	if got := New(nil, nil, Options{Concurrency: 0}).Opts.Concurrency; got != 3 {
		t.Fatalf("Concurrency = %d, want the default 3", got)
	}
}

// flakyDoer fails its first PR-page fetch with err (a retryable 502 when
// unset) and serves soloDoer's single page from then on.
type flakyDoer struct {
	soloDoer
	err    error
	failed atomic.Bool
}

func (d *flakyDoer) DoWithContext(ctx context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	if !strings.Contains(query, "PRProbe") && d.failed.CompareAndSwap(false, true) {
		if d.err != nil {
			return d.err
		}
		return retryableErr()
	}
	return d.soloDoer.DoWithContext(ctx, query, vars, resp)
}

// syncOnce runs a single-worker sync of target through a recording sleep
// and returns the repo's outcome and the sleeps it asked for.
func syncOnce(t *testing.T, store *db.Store, target Target, doer gh.Doer) (durs []time.Duration, repoErr error) {
	t.Helper()
	rec := &sleepRecorder{}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})
	engine.sleep = rec.sleep

	events := make(chan Event, 32)
	if err := engine.SyncAll(context.Background(), []Target{target}, events); err != nil {
		t.Fatal(err) // per-repo failures don't surface here by design
	}
	for ev := range events {
		if d, ok := ev.(RepoDone); ok {
			repoErr = d.Err
		}
	}
	return rec.durs, repoErr
}

// A transient fetch failure is retried through the sleep seam and the walk
// still commits clean: one first-step backoff, no repo error, bookkeeping
// stamped from the engine clock.
func TestSyncRetriesTransientFetch(t *testing.T) {
	store, target := newTestStore(t)
	durs, repoErr := syncOnce(t, store, target, &flakyDoer{soloDoer: soloDoer{now: fixedNow}})
	if repoErr != nil {
		t.Fatalf("RepoDone.Err = %v, want nil after the retry", repoErr)
	}
	if len(durs) != 1 || !inLadderStep(durs[0], 0) {
		t.Fatalf("sleeps = %v, want exactly one first-step backoff", durs)
	}
	if prs, _ := counts(t, store); prs != 1 {
		t.Fatalf("PRs = %d, want 1", prs)
	}
	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastError != nil || st.WatermarkUpdated == nil {
		t.Fatalf("sync state after recovered walk: %+v", st)
	}
	if st.LastSyncedAt == nil || *st.LastSyncedAt != fixedNow.Unix() {
		t.Fatalf("LastSyncedAt = %v, want %d (the engine clock)", st.LastSyncedAt, fixedNow.Unix())
	}
}

// A one-off INTERNAL GraphQL error on a page fetch is re-asked and the walk
// completes clean, with the failed reply discarded rather than merged.
func TestSyncRetriesTransientGraphQLError(t *testing.T) {
	store, target := newTestStore(t)
	doer := &flakyDoer{soloDoer: soloDoer{now: fixedNow}, err: internalErr()}
	durs, repoErr := syncOnce(t, store, target, doer)
	if repoErr != nil {
		t.Fatalf("RepoDone.Err = %v, want nil after the retry", repoErr)
	}
	if len(durs) != 1 || !inLadderStep(durs[0], 0) {
		t.Fatalf("sleeps = %v, want exactly one first-step backoff", durs)
	}
	if prs, _ := counts(t, store); prs != 1 {
		t.Fatalf("PRs = %d, want 1", prs)
	}
	if pages := doer.prPages.Load(); pages != 1 {
		t.Fatalf("pages served = %d, want 1 (the failed attempt served nothing)", pages)
	}
}

// samlDoer answers every request with a permanent 403.
type samlDoer struct{ calls atomic.Int32 }

func (d *samlDoer) DoWithContext(context.Context, string, map[string]interface{}, interface{}) error {
	d.calls.Add(1)
	return samlErr()
}

// A repo the token cannot see fails on the first request with the server's
// reason recorded, not after a minute of pointless retries.
func TestSyncFailsFastOnPermanent403(t *testing.T) {
	store, target := newTestStore(t)
	doer := &samlDoer{}
	durs, repoErr := syncOnce(t, store, target, doer)
	if repoErr == nil || !strings.Contains(repoErr.Error(), "SAML") {
		t.Fatalf("RepoDone.Err = %v, want the SAML message", repoErr)
	}
	if calls := doer.calls.Load(); calls != 1 || len(durs) != 0 {
		t.Fatalf("calls = %d, sleeps = %v; want 1 and none", calls, durs)
	}
	st, err := store.GetSyncState(target.RepoID)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastError == nil || !strings.Contains(*st.LastError, "SAML") {
		t.Fatalf("LastError = %v, want the SAML message", st.LastError)
	}
}
