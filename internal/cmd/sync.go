package cmd

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/syncer"
)

var (
	syncTeam     string
	syncBackfill int
	syncJSON     bool
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Refresh the local cache from GitHub (cron-friendly, no TUI)",
	Long: `Walk every configured repo and update the local cache.

Exits non-zero when any repo fails, so a cron job notices a stale cache
instead of trusting numbers that stopped moving.

--json replaces the progress lines with one object on stdout: what this run
updated, and the state every repo is left in. The per-repo fields are the
ones doctor reports, under the same names.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := bootstrap(syncTeam)
		if err != nil {
			return err
		}
		defer env.Close()

		if env.Team.NoSync {
			return fmt.Errorf("team %q is local-only (no_sync: true) and cannot be synced", env.Team.Name)
		}

		doer, err := newClient()
		if err != nil {
			return err
		}

		opts := syncer.Options{
			BackfillDays: env.Cfg.Sync.BackfillDays,
			PageSize:     env.Cfg.Sync.PageSize,
			Concurrency:  env.Cfg.Sync.Concurrency,
		}
		if syncBackfill > 0 {
			opts.BackfillDays = syncBackfill
		}
		engine := syncer.New(env.Store, doer, opts)

		events := make(chan syncer.Event, 16)
		errCh := make(chan error, 1)
		started := time.Now()
		go func() { errCh <- engine.SyncAll(cmd.Context(), env.Targets, events) }()

		// --json owns stdout. Progress lines ahead of the object would
		// leave a script with something it cannot parse.
		out := printer{cmd.OutOrStdout()}
		if syncJSON {
			out = printer{io.Discard}
		}
		var failed, total int
		updated := map[string]int{}
		for ev := range events {
			switch ev := ev.(type) {
			case syncer.RepoStarted:
				out.printf("syncing %s...\n", ev.Repo)
			case syncer.RepoPage:
				out.printf("  %s: %d PRs\n", ev.Repo, ev.PRs)
			case syncer.RateLimited:
				// A quota reset can be an hour out; say how long, not just when.
				wait := max(time.Until(ev.Until), 0).Round(time.Second)
				out.printf("  rate limited, sleeping %s until %s\n", wait, ev.Until.Local().Format("15:04:05 MST"))
			case syncer.RepoDone:
				updated[ev.Repo] = ev.PRs
				if ev.Err != nil {
					out.printf("  %s FAILED: %v\n", ev.Repo, ev.Err)
				} else {
					out.printf("  %s done (%d PRs updated)\n", ev.Repo, ev.PRs)
				}
			case syncer.Complete:
				failed, total = ev.Failed, ev.TotalPRs
				out.printf("sync complete: %d PRs updated, %d repos failed\n", ev.TotalPRs, ev.Failed)
			}
		}
		if err := <-errCh; err != nil {
			return err
		}
		if syncJSON {
			if err := printSyncJSON(cmd.OutOrStdout(), env, syncRun{
				started: started, finished: time.Now(),
				total: total, failed: failed, updated: updated,
			}); err != nil {
				return err
			}
		}
		// A failed repo means the cache is stale for part of the team; exit
		// non-zero so cron and scripts notice instead of trusting old numbers.
		if failed > 0 {
			return fmt.Errorf("%d repo(s) failed to sync", failed)
		}
		return nil
	},
}

// syncRun is what this run did, as gathered from the event stream.
type syncRun struct {
	started, finished time.Time
	total, failed     int
	updated           map[string]int // repo → PRs stored
}

// syncReport is the --json document. Field order is declaration order.
type syncReport struct {
	Team       string       `json:"team"`
	StartedAt  string       `json:"started_at"`
	FinishedAt string       `json:"finished_at"`
	TotalPRs   int          `json:"total_prs"`
	Failed     int          `json:"failed"`
	Repos      []syncedRepo `json:"repos"`
}

// syncedRepo is what this run stored, then the state the cache is left in.
// Status and below come from sync_state through doctor, not from this run's
// events, so a repo that failed before this run still reports as failing.
type syncedRepo struct {
	Repo             string  `json:"repo"`
	PRsUpdated       int     `json:"prs_updated"`
	Status           string  `json:"status"`
	LastSyncedAt     *string `json:"last_synced_at"`
	WatermarkUpdated *string `json:"watermark_updated"`
	BackfillUntil    *string `json:"backfill_until"`
	LastError        *string `json:"last_error"`
}

func printSyncJSON(w io.Writer, env *appEnv, run syncRun) error {
	rep, err := doctor.Load(env.Store, env.Team, env.TeamID, run.finished)
	if err != nil {
		return err
	}
	report := syncReport{
		Team:       rep.Team,
		StartedAt:  run.started.UTC().Format(time.RFC3339),
		FinishedAt: run.finished.UTC().Format(time.RFC3339),
		TotalPRs:   run.total,
		Failed:     run.failed,
		Repos:      make([]syncedRepo, 0, len(rep.Rows)),
	}
	for _, r := range rep.Rows {
		report.Repos = append(report.Repos, syncedRepo{
			Repo:             r.Repo,
			PRsUpdated:       run.updated[r.Repo],
			Status:           r.Status.String(),
			LastSyncedAt:     rfc3339(r.LastSyncedAt),
			WatermarkUpdated: rfc3339(r.WatermarkUpdated),
			BackfillUntil:    rfc3339(r.BackfillUntil),
			LastError:        nilOnEmpty(r.Error),
		})
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}

// rfc3339 renders a cache timestamp. A missing one stays null rather than
// reading as a sync in 1970.
func rfc3339(ts *int64) *string {
	if ts == nil {
		return nil
	}
	s := time.Unix(*ts, 0).UTC().Format(time.RFC3339)
	return &s
}

// nilOnEmpty keeps a clean repo's error null rather than an empty string.
func nilOnEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func init() {
	syncCmd.Flags().StringVar(&syncTeam, "team", "", "team profile to sync (default: config default_team)")
	syncCmd.Flags().IntVar(&syncBackfill, "backfill", 0, "override backfill window in days")
	syncCmd.Flags().BoolVar(&syncJSON, "json", false, "print one JSON object instead of progress lines")
	rootCmd.AddCommand(syncCmd)
}
