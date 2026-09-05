package syncer

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/cli/go-gh/v2/pkg/api"
)

// retryableErr is what the client surfaces for a 502: worth a retry.
func retryableErr() error { return &api.HTTPError{StatusCode: 502, Message: "bad gateway"} }

// inLadderStep reports whether d is retryDelays[i] plus less than one
// retryJitter of slack.
func inLadderStep(d time.Duration, i int) bool {
	return d >= retryDelays[i] && d < retryDelays[i]+retryJitter
}

// retryHarness is a store-less engine whose sleep records instead of
// waiting, with a limiter sharing that sleep and the pinned clock.
func retryHarness() (*Engine, *limiter, *sleepRecorder) {
	rec := &sleepRecorder{}
	e := newEngine(nil, nil, Options{})
	e.sleep = rec.sleep
	return e, e.newLimiter(), rec
}

func TestWithRetry(t *testing.T) {
	t.Run("walks the ladder then gives up", func(t *testing.T) {
		e, lim, rec := retryHarness()
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
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
		e, lim, rec := retryHarness()
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
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
		e, lim, rec := retryHarness()
		perm := &api.GraphQLError{} // semantic errors are never retried
		calls := 0
		err := e.withRetry(context.Background(), lim, nil, func() error {
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
		lim := e.newLimiter()

		apiErr := retryableErr()
		calls := 0
		err := e.withRetry(ctx, lim, nil, func() error {
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
