// Package syncer walks GitHub pull-request data into the local cache. It is
// TUI-agnostic: progress is reported as typed events on a channel, consumed
// by both the Bubble Tea status bar and the headless `sync` command.
package syncer

import (
	"context"
	"fmt"
	"math/rand"
	"sync"
	"time"

	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/gh"
)

// Event is a progress notification from a sync run.
type Event interface{ event() }

type RepoStarted struct{ Repo string }

// RepoPage reports cumulative PRs stored for a repo after each page. The
// count spans verify retries (see maxWalkAttempts), so it can exceed the
// number of distinct PRs — it is progress, not a dedupe.
type RepoPage struct {
	Repo string
	PRs  int
}

type RepoDone struct {
	Repo string
	PRs  int
	Err  error
}

// RateLimited reports that the walk is sleeping until the API quota resets.
type RateLimited struct{ Until time.Time }

type Complete struct {
	TotalPRs int
	Failed   int
}

func (RepoStarted) event() {}
func (RepoPage) event()    {}
func (RepoDone) event()    {}
func (RateLimited) event() {}
func (Complete) event()    {}

// Target is one repo to sync.
type Target struct {
	Owner  string
	Name   string
	RepoID int64
}

func (t Target) String() string { return t.Owner + "/" + t.Name }

type Options struct {
	BackfillDays int
	PageSize     int
	Concurrency  int
	// Overlap re-fetches PRs updated slightly before the watermark, guarding
	// against clock skew and page-ordering races.
	Overlap time.Duration
}

type Engine struct {
	Store *db.Store
	Doer  gh.Doer
	Opts  Options

	// The walk's two real-time dependencies. New wires the wall clock and
	// a context-aware timer; tests substitute a pinned clock and a sleep
	// that records instead of waiting.
	now   func() time.Time
	sleep func(context.Context, time.Duration) error
}

func New(store *db.Store, doer gh.Doer, opts Options) *Engine {
	if opts.PageSize <= 0 {
		opts.PageSize = 25
	}
	if opts.BackfillDays <= 0 {
		opts.BackfillDays = 120
	}
	if opts.Concurrency <= 0 {
		opts.Concurrency = 3
	}
	if opts.Overlap <= 0 {
		opts.Overlap = time.Hour
	}
	return &Engine{Store: store, Doer: doer, Opts: opts, now: time.Now, sleep: sleepFor}
}

// SyncAll walks the targets with a bounded worker pool and closes events
// when finished. Individual repo failures are reported via RepoDone.Err and
// don't abort other repos; only context cancellation stops the run early.
// Two guarantees the TUI leans on: cancellation unblocks every send and the
// dispatch loop, so the goroutine exits even after the consumer stops
// draining; and events closes only after the last worker returns, so the
// close means every database write of this run has finished.
func (e *Engine) SyncAll(ctx context.Context, targets []Target, events chan<- Event) error {
	if events != nil {
		defer close(events)
	}

	var (
		wg            sync.WaitGroup
		mu            sync.Mutex
		total, failed int
	)
	lim := &limiter{now: e.now, sleep: e.sleep}
	sem := make(chan struct{}, e.Opts.Concurrency)

dispatch:
	for _, t := range targets {
		if ctx.Err() != nil {
			break
		}
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break dispatch
		}
		wg.Add(1)
		go func(t Target) {
			defer wg.Done()
			defer func() { <-sem }()

			emit(ctx, events, RepoStarted{Repo: t.String()})
			n, err := e.syncRepo(ctx, t, lim, events)
			// Record the failure, but not on cancellation (same condition as
			// the failed counter below): a quit mid-walk is not a repo error
			// and must leave sync_state exactly as the last real walk did.
			if err != nil && ctx.Err() == nil {
				if werr := e.Store.SetSyncError(t.RepoID, err.Error()); werr != nil {
					err = fmt.Errorf("%w (also failed to record sync failure: %w)", err, werr)
				}
			}
			mu.Lock()
			total += n
			if err != nil && ctx.Err() == nil {
				failed++
			}
			mu.Unlock()
			emit(ctx, events, RepoDone{Repo: t.String(), PRs: n, Err: err})
		}(t)
	}
	wg.Wait()
	emit(ctx, events, Complete{TotalPRs: total, Failed: failed})
	return ctx.Err()
}

// maxWalkAttempts caps verify-retry re-walks per repo per sync. After the
// cap the final attempt commits anyway: a permanent miss would need the
// same PR skipped in every independent attempt, and the next run's overlap
// re-covers the newest hour regardless.
const maxWalkAttempts = 3

// walkSig fingerprints the top of the UPDATED_AT DESC list at walk start.
// Any insert or update moves a PR to position one or bumps the first
// node's timestamp; any deletion changes totalCount. An unchanged
// signature after a multi-page walk therefore means no reorder crossed the
// cursor. (Same-second ties and a transient insert+delete of one PR are
// undetectable — both marginal, and the overlap covers the newest hour.)
type walkSig struct {
	firstID      string
	firstUpdated int64
	total        int
}

// prWalk is one walk attempt's outcome. stored and maxUpdated are
// per-attempt; only the final attempt's values are committed.
type prWalk struct {
	stored     int
	maxUpdated int64
	pages      int // PR-page fetches used (overflow follow-ups excluded)
	sig        walkSig
}

// syncRepo walks one repo newest-updated-first until it reaches data it has
// already processed (watermark − overlap) or the backfill horizon, then
// verifies that the PR list didn't mutate mid-walk before committing.
func (e *Engine) syncRepo(ctx context.Context, t Target, lim *limiter, events chan<- Event) (int, error) {
	state, err := e.Store.GetSyncState(t.RepoID)
	if err != nil {
		return 0, err
	}

	now := e.now().UTC()
	horizon := now.AddDate(0, 0, -e.Opts.BackfillDays).Unix()

	stopAt := horizon
	if state.WatermarkUpdated != nil {
		needDeeper := state.BackfillUntil != nil && horizon < *state.BackfillUntil
		if !needDeeper {
			if wm := *state.WatermarkUpdated - int64(e.Opts.Overlap.Seconds()); wm > stopAt {
				stopAt = wm
			}
		}
	}

	var (
		walk  prWalk
		total int
	)
	for attempt := 1; ; attempt++ {
		walk, err = e.walkPRs(ctx, t, stopAt, total, lim, events)
		total += walk.stored
		if err != nil {
			return total, err
		}
		if walk.pages <= 1 {
			break // single fetch = one atomic snapshot; nothing can be skipped
		}
		if attempt >= maxWalkAttempts {
			break // commit anyway; see maxWalkAttempts
		}
		clean, perr := e.probeUnchanged(ctx, t, walk.sig, lim, events)
		if perr != nil {
			return total, perr
		}
		if clean {
			break
		}
		// Dirty: the list mutated mid-walk and may have shifted a PR past
		// the cursor unseen. Re-walk with the same stopAt; SavePullRequests
		// upserts, so repeats are idempotent.
	}

	// Commit bookkeeping only after a verified walk; a crash mid-walk just
	// re-fetches next run.
	newState := state
	newState.RepoID = t.RepoID
	if walk.maxUpdated > 0 {
		newState.WatermarkUpdated = &walk.maxUpdated
	}
	// Record how deep this walk actually went. stopAt is watermark−overlap
	// on incremental runs and the horizon on fresh or deepening runs;
	// stamping the configured horizon unconditionally would let
	// CoverageFloor claim coverage the cache lacks. A repo whose recorded
	// coverage is shallower than the horizon triggers needDeeper on the
	// next run and self-heals.
	if state.BackfillUntil == nil || stopAt < *state.BackfillUntil {
		newState.BackfillUntil = &stopAt
	}
	syncedAt := now.Unix()
	newState.LastSyncedAt = &syncedAt
	newState.LastError = nil
	return total, e.Store.SetSyncState(newState)
}

// walkPRs is one walk attempt: page newest-updated-first until a node
// older than stopAt or the list ends. prior is the PR count already stored
// by earlier attempts, folded into RepoPage progress events.
func (e *Engine) walkPRs(ctx context.Context, t Target, stopAt int64, prior int, lim *limiter, events chan<- Event) (prWalk, error) {
	var (
		w      prWalk
		cursor string
	)
	for {
		if err := ctx.Err(); err != nil {
			return w, err
		}
		if err := lim.wait(ctx, events); err != nil {
			return w, err
		}

		var page *gh.PRPage
		err := e.withRetry(ctx, func() error {
			var err error
			page, err = gh.FetchPRPage(ctx, e.Doer, t.Owner, t.Name, cursor, e.Opts.PageSize)
			return err
		})
		if err != nil {
			return w, fmt.Errorf("fetching %s: %w", t, err)
		}
		lim.note(page.RateLimit)
		w.pages++
		if w.pages == 1 && len(page.Nodes) > 0 {
			w.sig = walkSig{
				firstID:      page.Nodes[0].ID,
				firstUpdated: page.Nodes[0].UpdatedAt.Unix(),
				total:        page.TotalCount,
			}
		}

		var batch []db.PullRequest
		reachedStop := false
		for _, node := range page.Nodes {
			updated := node.UpdatedAt.Unix()
			if updated <= 0 {
				// A missing or zero updatedAt satisfies every stop
				// condition and would silently truncate the walk; fail the
				// repo instead of committing bookkeeping over it.
				return w, fmt.Errorf("%s#%d: missing or invalid updatedAt in API response", t, node.Number)
			}
			if updated > w.maxUpdated {
				w.maxUpdated = updated
			}
			if updated < stopAt {
				reachedStop = true
				break
			}
			if err := e.resolveOverflow(ctx, &node, lim, events); err != nil {
				return w, fmt.Errorf("fetching overflow for %s#%d: %w", t, node.Number, err)
			}
			batch = append(batch, convertPR(node, t.RepoID))
		}
		if err := e.Store.SavePullRequests(batch); err != nil {
			return w, fmt.Errorf("saving %s: %w", t, err)
		}
		w.stored += len(batch)
		emit(ctx, events, RepoPage{Repo: t.String(), PRs: prior + w.stored})

		if reachedStop || !page.HasNextPage {
			return w, nil
		}
		cursor = page.EndCursor
	}
}

// probeUnchanged re-fetches the top of the PR list after a multi-page walk
// and reports whether it still matches the signature captured at walk
// start (see walkSig).
func (e *Engine) probeUnchanged(ctx context.Context, t Target, sig walkSig, lim *limiter, events chan<- Event) (bool, error) {
	if err := lim.wait(ctx, events); err != nil {
		return false, err
	}
	var probe *gh.PRProbe
	err := e.withRetry(ctx, func() error {
		var err error
		probe, err = gh.FetchPRProbe(ctx, e.Doer, t.Owner, t.Name)
		return err
	})
	if err != nil {
		return false, fmt.Errorf("probing %s: %w", t, err)
	}
	lim.note(probe.RateLimit)
	got := walkSig{
		firstID:      probe.FirstID,
		firstUpdated: probe.FirstUpdated.Unix(),
		total:        probe.TotalCount,
	}
	return got == sig, nil
}

// resolveOverflow completes a PR whose nested reviews/comments exceeded the
// main walk's first:50 by paging the rest via node(id:) queries. Rare
// (giant discussion threads), so per-PR follow-ups are fine.
func (e *Engine) resolveOverflow(ctx context.Context, node *gh.PRNode, lim *limiter, events chan<- Event) error {
	if node.Reviews.PageInfo.HasNextPage {
		if err := lim.wait(ctx, events); err != nil {
			return err
		}
		err := e.withRetry(ctx, func() error {
			more, rl, err := gh.FetchAllReviews(ctx, e.Doer, node.ID, node.Reviews.PageInfo.EndCursor)
			lim.note(rl)
			if err == nil {
				node.Reviews.Nodes = append(node.Reviews.Nodes, more...)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	if node.Comments.PageInfo.HasNextPage {
		if err := lim.wait(ctx, events); err != nil {
			return err
		}
		err := e.withRetry(ctx, func() error {
			more, rl, err := gh.FetchAllComments(ctx, e.Doer, node.ID, node.Comments.PageInfo.EndCursor)
			lim.note(rl)
			if err == nil {
				node.Comments.Nodes = append(node.Comments.Nodes, more...)
			}
			return err
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func convertPR(n gh.PRNode, repoID int64) db.PullRequest {
	pr := db.PullRequest{
		ID:           n.ID,
		RepoID:       repoID,
		Number:       n.Number,
		Author:       n.Author.SafeLogin(),
		AuthorIsBot:  n.Author.IsBot(),
		Title:        n.Title,
		State:        n.State,
		IsDraft:      n.IsDraft,
		CreatedAt:    n.CreatedAt.Unix(),
		UpdatedAt:    n.UpdatedAt.Unix(),
		MergedAt:     unixPtr(n.MergedAt),
		ClosedAt:     unixPtr(n.ClosedAt),
		Additions:    n.Additions,
		Deletions:    n.Deletions,
		ChangedFiles: n.ChangedFiles,
	}
	for _, r := range n.Reviews.Nodes {
		if r.SubmittedAt == nil || r.State == "PENDING" {
			continue // unsubmitted draft review, visible only to its author
		}
		pr.Reviews = append(pr.Reviews, db.Review{
			ID:           r.ID,
			Author:       r.Author.SafeLogin(),
			AuthorIsBot:  r.Author.IsBot(),
			State:        r.State,
			SubmittedAt:  r.SubmittedAt.Unix(),
			CommentCount: r.Comments.TotalCount,
		})
	}
	for _, c := range n.Comments.Nodes {
		pr.Comments = append(pr.Comments, db.IssueComment{
			ID:          c.ID,
			Author:      c.Author.SafeLogin(),
			AuthorIsBot: c.Author.IsBot(),
			CreatedAt:   c.CreatedAt.Unix(),
		})
	}
	return pr
}

// limiter pauses all workers when any response reports the API quota
// running low. GitHub's GraphQL budget is shared across the token, so one
// worker seeing trouble means everyone waits.
type limiter struct {
	mu         sync.Mutex
	pauseUntil time.Time
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
}

const lowWater = 100

func (l *limiter) wait(ctx context.Context, events chan<- Event) error {
	l.mu.Lock()
	until := l.pauseUntil
	l.mu.Unlock()
	if now := l.now(); now.Before(until) {
		emit(ctx, events, RateLimited{Until: until})
		return l.sleep(ctx, until.Sub(now))
	}
	return nil
}

func (l *limiter) note(rl gh.RateLimit) {
	// A zero Remaining with a zero ResetAt means the block was absent.
	if rl.Remaining >= lowWater || rl.ResetAt.IsZero() {
		return
	}
	l.mu.Lock()
	if rl.ResetAt.After(l.pauseUntil) {
		l.pauseUntil = rl.ResetAt
	}
	l.mu.Unlock()
}

// retryDelays is the backoff ladder for retryable API errors; each step
// gets up to retryJitter of random slack so concurrent workers do not
// retry in lockstep.
var retryDelays = []time.Duration{2 * time.Second, 8 * time.Second, 20 * time.Second, 45 * time.Second}

const retryJitter = time.Second

// withRetry runs fn, retrying retryable API errors with jittered backoff.
// A cancellation during the backoff surfaces the API error, not the
// context's: it names what actually went wrong.
func (e *Engine) withRetry(ctx context.Context, fn func() error) error {
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || ctx.Err() != nil || attempt >= len(retryDelays) || !gh.IsRetryable(err) {
			return err
		}
		delay := retryDelays[attempt] + time.Duration(rand.Int63n(int64(retryJitter)))
		if serr := e.sleep(ctx, delay); serr != nil {
			return err
		}
	}
}

func unixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	u := t.Unix()
	return &u
}

// sleepFor is the production sleeper behind Engine.sleep: a timer that a
// cancelled context cuts short.
func sleepFor(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// emit sends ev, or drops it once ctx is cancelled: after a quit or team
// switch nobody drains the channel, and an unconditional send would wedge
// the worker forever.
func emit(ctx context.Context, events chan<- Event, ev Event) {
	if events == nil {
		return
	}
	select {
	case events <- ev:
	case <-ctx.Done():
	}
}
