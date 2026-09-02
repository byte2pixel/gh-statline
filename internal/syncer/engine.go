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

// RepoPage reports cumulative PRs stored for a repo after each page.
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
	return &Engine{Store: store, Doer: doer, Opts: opts}
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
	lim := &limiter{}
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
			if err != nil {
				msg := err.Error()
				now := time.Now().Unix()
				st, _ := e.Store.GetSyncState(t.RepoID)
				st.RepoID = t.RepoID
				st.LastError = &msg
				st.LastSyncedAt = &now
				_ = e.Store.SetSyncState(st)
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

// syncRepo walks one repo newest-updated-first until it reaches data it has
// already processed (watermark − overlap) or the backfill horizon.
func (e *Engine) syncRepo(ctx context.Context, t Target, lim *limiter, events chan<- Event) (int, error) {
	state, err := e.Store.GetSyncState(t.RepoID)
	if err != nil {
		return 0, err
	}

	now := time.Now().UTC()
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
		cursor     string
		stored     int
		maxUpdated int64
	)
	for {
		if err := ctx.Err(); err != nil {
			return stored, err
		}
		if err := lim.wait(ctx, events); err != nil {
			return stored, err
		}

		var page *gh.PRPage
		err := withRetry(ctx, func() error {
			var err error
			page, err = gh.FetchPRPage(ctx, e.Doer, t.Owner, t.Name, cursor, e.Opts.PageSize)
			return err
		})
		if err != nil {
			return stored, fmt.Errorf("fetching %s: %w", t, err)
		}
		lim.note(page.RateLimit)

		var batch []db.PullRequest
		reachedStop := false
		for _, node := range page.Nodes {
			updated := node.UpdatedAt.Unix()
			if updated > maxUpdated {
				maxUpdated = updated
			}
			if updated < stopAt {
				reachedStop = true
				break
			}
			if err := e.resolveOverflow(ctx, &node, lim, events); err != nil {
				return stored, fmt.Errorf("fetching overflow for %s#%d: %w", t, node.Number, err)
			}
			batch = append(batch, convertPR(node, t.RepoID))
		}
		if err := e.Store.SavePullRequests(batch); err != nil {
			return stored, fmt.Errorf("saving %s: %w", t, err)
		}
		stored += len(batch)
		emit(ctx, events, RepoPage{Repo: t.String(), PRs: stored})

		if reachedStop || !page.HasNextPage {
			break
		}
		cursor = page.EndCursor
	}

	// Commit bookkeeping only after a clean walk; a crash mid-walk just
	// re-fetches next run.
	newState := state
	newState.RepoID = t.RepoID
	if maxUpdated > 0 {
		newState.WatermarkUpdated = &maxUpdated
	}
	if state.BackfillUntil == nil || horizon < *state.BackfillUntil {
		newState.BackfillUntil = &horizon
	}
	syncedAt := now.Unix()
	newState.LastSyncedAt = &syncedAt
	newState.LastError = nil
	return stored, e.Store.SetSyncState(newState)
}

// resolveOverflow completes a PR whose nested reviews/comments exceeded the
// main walk's first:50 by paging the rest via node(id:) queries. Rare
// (giant discussion threads), so per-PR follow-ups are fine.
func (e *Engine) resolveOverflow(ctx context.Context, node *gh.PRNode, lim *limiter, events chan<- Event) error {
	if node.Reviews.PageInfo.HasNextPage {
		if err := lim.wait(ctx, events); err != nil {
			return err
		}
		err := withRetry(ctx, func() error {
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
		err := withRetry(ctx, func() error {
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
}

const lowWater = 100

func (l *limiter) wait(ctx context.Context, events chan<- Event) error {
	l.mu.Lock()
	until := l.pauseUntil
	l.mu.Unlock()
	if time.Now().Before(until) {
		emit(ctx, events, RateLimited{Until: until})
		return sleepUntil(ctx, until)
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

// withRetry runs fn, retrying retryable API errors with jittered backoff.
func withRetry(ctx context.Context, fn func() error) error {
	delays := []time.Duration{2 * time.Second, 8 * time.Second, 20 * time.Second, 45 * time.Second}
	for attempt := 0; ; attempt++ {
		err := fn()
		if err == nil || ctx.Err() != nil || attempt >= len(delays) || !gh.IsRetryable(err) {
			return err
		}
		delay := delays[attempt] + time.Duration(rand.Int63n(int64(time.Second)))
		if serr := sleepFor(ctx, delay); serr != nil {
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

func sleepUntil(ctx context.Context, t time.Time) error {
	return sleepFor(ctx, time.Until(t))
}

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
