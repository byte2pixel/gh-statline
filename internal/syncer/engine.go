// Package syncer walks GitHub pull-request data into the local cache. It is
// TUI-agnostic: progress is reported as typed events on a channel, consumed
// by both the Bubble Tea status bar and the headless `sync` command.
package syncer

import (
	"context"
	"fmt"
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
		opts.BackfillDays = 90
	}
	if opts.Overlap <= 0 {
		opts.Overlap = time.Hour
	}
	return &Engine{Store: store, Doer: doer, Opts: opts}
}

// SyncAll walks every target serially and closes events when finished.
// Individual repo failures are reported as RepoDone.Err and do not abort the
// remaining targets; only context cancellation stops the run early.
func (e *Engine) SyncAll(ctx context.Context, targets []Target, events chan<- Event) error {
	if events != nil {
		defer close(events)
	}
	total, failed := 0, 0
	for _, t := range targets {
		emit(events, RepoStarted{Repo: t.String()})
		n, err := e.syncRepo(ctx, t, events)
		total += n
		if err != nil {
			failed++
			msg := err.Error()
			now := time.Now().Unix()
			_ = e.Store.SetSyncState(mergeState(e.mustState(t.RepoID), func(s *db.SyncState) {
				s.LastError = &msg
				s.LastSyncedAt = &now
			}))
		}
		emit(events, RepoDone{Repo: t.String(), PRs: n, Err: err})
		if ctx.Err() != nil {
			return ctx.Err()
		}
	}
	emit(events, Complete{TotalPRs: total, Failed: failed})
	return nil
}

// syncRepo walks one repo newest-updated-first until it reaches data it has
// already processed (watermark − overlap) or the backfill horizon.
func (e *Engine) syncRepo(ctx context.Context, t Target, events chan<- Event) (int, error) {
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
		page, err := gh.FetchPRPage(ctx, e.Doer, t.Owner, t.Name, cursor, e.Opts.PageSize)
		if err != nil {
			return stored, fmt.Errorf("fetching %s: %w", t, err)
		}

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
			batch = append(batch, convertPR(node, t.RepoID))
		}
		if err := e.Store.SavePullRequests(batch); err != nil {
			return stored, fmt.Errorf("saving %s: %w", t, err)
		}
		stored += len(batch)
		emit(events, RepoPage{Repo: t.String(), PRs: stored})

		if reachedStop || !page.HasNextPage {
			break
		}
		cursor = page.EndCursor

		if page.RateLimit.Remaining > 0 && page.RateLimit.Remaining < 100 {
			emit(events, RateLimited{Until: page.RateLimit.ResetAt})
			if err := sleepUntil(ctx, page.RateLimit.ResetAt); err != nil {
				return stored, err
			}
		}
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

func (e *Engine) mustState(repoID int64) db.SyncState {
	s, err := e.Store.GetSyncState(repoID)
	if err != nil {
		return db.SyncState{RepoID: repoID}
	}
	return s
}

func mergeState(s db.SyncState, fn func(*db.SyncState)) db.SyncState {
	fn(&s)
	return s
}

func unixPtr(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	u := t.Unix()
	return &u
}

func sleepUntil(ctx context.Context, t time.Time) error {
	d := time.Until(t)
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

func emit(events chan<- Event, ev Event) {
	if events != nil {
		events <- ev
	}
}
