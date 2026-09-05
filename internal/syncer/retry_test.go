package syncer

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/byte2pixel/gh-statline/internal/gh"
)

// retryableErr is what the client surfaces for a 502: worth a retry.
func retryableErr() error { return &api.HTTPError{StatusCode: 502, Message: "bad gateway"} }

// inLadderStep reports whether d is retryDelays[i] plus less than one
// retryJitter of slack.
func inLadderStep(d time.Duration, i int) bool {
	return d >= retryDelays[i] && d < retryDelays[i]+retryJitter
}

func TestWithRetry(t *testing.T) {
	t.Run("walks the ladder then gives up", func(t *testing.T) {
		rec := &sleepRecorder{}
		e := newEngine(nil, nil, Options{})
		e.sleep = rec.sleep

		calls := 0
		err := e.withRetry(context.Background(), func() error {
			calls++
			return retryableErr()
		})
		if err == nil {
			t.Fatal("exhausted retries returned nil")
		}
		if calls != len(retryDelays)+1 {
			t.Fatalf("calls = %d, want %d (initial + one per ladder step)", calls, len(retryDelays)+1)
		}
		if len(rec.durs) != len(retryDelays) {
			t.Fatalf("sleeps = %v, want one per ladder step", rec.durs)
		}
		for i, d := range rec.durs {
			if !inLadderStep(d, i) {
				t.Errorf("sleep %d = %v, want in [%v, %v)", i, d, retryDelays[i], retryDelays[i]+retryJitter)
			}
		}
	})

	t.Run("recovers after transient errors", func(t *testing.T) {
		rec := &sleepRecorder{}
		e := newEngine(nil, nil, Options{})
		e.sleep = rec.sleep

		calls := 0
		err := e.withRetry(context.Background(), func() error {
			calls++
			if calls <= 2 {
				return retryableErr()
			}
			return nil
		})
		if err != nil || calls != 3 || len(rec.durs) != 2 {
			t.Fatalf("err = %v, calls = %d, sleeps = %v; want nil, 3, two backoffs", err, calls, rec.durs)
		}
	})

	t.Run("permanent error returns at once", func(t *testing.T) {
		rec := &sleepRecorder{}
		e := newEngine(nil, nil, Options{})
		e.sleep = rec.sleep

		perm := &api.GraphQLError{} // semantic errors are never retried
		calls := 0
		err := e.withRetry(context.Background(), func() error {
			calls++
			return perm
		})
		if !errors.Is(err, perm) || calls != 1 || len(rec.durs) != 0 {
			t.Fatalf("err = %v, calls = %d, sleeps = %v; want the error itself, 1, none", err, calls, rec.durs)
		}
	})

	t.Run("cancelled during backoff returns the API error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		e := newEngine(nil, nil, Options{})
		e.sleep = func(ctx context.Context, _ time.Duration) error {
			cancel()
			return ctx.Err()
		}

		apiErr := retryableErr()
		calls := 0
		err := e.withRetry(ctx, func() error {
			calls++
			return apiErr
		})
		if !errors.Is(err, apiErr) {
			t.Fatalf("err = %v, want the API error, not the context's", err)
		}
		if calls != 1 {
			t.Fatalf("calls = %d, want 1 (no retry after cancellation)", calls)
		}
	})
}

// The limiter pauses every worker until the quota resets once any response
// reports it running low, and reports the pause as an event.
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

	// An earlier reset from another response never shortens the pause; a
	// later one extends it.
	lim.note(gh.RateLimit{Remaining: 1, ResetAt: fixedNow.Add(30 * time.Second)})
	lim.note(gh.RateLimit{Remaining: 1, ResetAt: fixedNow.Add(2 * time.Minute)})
	if err := lim.wait(ctx, events); err != nil {
		t.Fatal(err)
	}
	if got := rec.durs[len(rec.durs)-1]; got != 2*time.Minute {
		t.Fatalf("sleep after competing resets = %v, want 2m (the latest)", got)
	}
	<-events

	// Once the clock reaches the reset, waits are free again.
	lim.now = func() time.Time { return fixedNow.Add(2 * time.Minute) }
	before := len(rec.durs)
	if err := lim.wait(ctx, events); err != nil || len(rec.durs) != before {
		t.Fatalf("wait at reset: err = %v, sleeps = %v; want no new sleep", err, rec.durs)
	}
}

// sleepFor is the production sleeper: a cancelled context must end the
// wait at once, and a non-positive duration never starts one.
func TestSleepForHonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := sleepFor(ctx, time.Hour); !errors.Is(err, context.Canceled) {
		t.Fatalf("sleepFor on a cancelled context = %v, want context.Canceled", err)
	}
	if err := sleepFor(context.Background(), 0); err != nil {
		t.Fatalf("sleepFor(0) = %v, want nil", err)
	}
}

// flakyDoer fails its first PR-page fetch with a retryable 502 and serves
// soloDoer's single page from then on.
type flakyDoer struct {
	soloDoer
	failed atomic.Bool
}

func (d *flakyDoer) DoWithContext(ctx context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	if !strings.Contains(query, "PRProbe") && d.failed.CompareAndSwap(false, true) {
		return retryableErr()
	}
	return d.soloDoer.DoWithContext(ctx, query, vars, resp)
}

// A transient fetch failure is retried through the sleep seam and the walk
// still commits clean: one first-step backoff, no repo error, bookkeeping
// stamped from the engine clock.
func TestSyncRetriesTransientFetch(t *testing.T) {
	store, target := newTestStore(t)
	doer := &flakyDoer{soloDoer: soloDoer{now: fixedNow}}
	rec := &sleepRecorder{}
	engine := newEngine(store, doer, Options{BackfillDays: 30, PageSize: 25, Concurrency: 1})
	engine.sleep = rec.sleep

	events := make(chan Event, 32)
	if err := engine.SyncAll(context.Background(), []Target{target}, events); err != nil {
		t.Fatal(err)
	}
	for ev := range events {
		if d, ok := ev.(RepoDone); ok && d.Err != nil {
			t.Fatalf("RepoDone.Err = %v, want nil after the retry", d.Err)
		}
	}
	if len(rec.durs) != 1 || !inLadderStep(rec.durs[0], 0) {
		t.Fatalf("sleeps = %v, want exactly one first-step backoff", rec.durs)
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
