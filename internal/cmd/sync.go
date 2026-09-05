package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/syncer"
)

var (
	syncTeam     string
	syncBackfill int
)

var syncCmd = &cobra.Command{
	Use:   "sync",
	Short: "Refresh the local cache from GitHub (cron-friendly, no TUI)",
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
		go func() { errCh <- engine.SyncAll(cmd.Context(), env.Targets, events) }()

		var failed int
		for ev := range events {
			switch ev := ev.(type) {
			case syncer.RepoStarted:
				fmt.Printf("syncing %s...\n", ev.Repo)
			case syncer.RepoPage:
				fmt.Printf("  %s: %d PRs\n", ev.Repo, ev.PRs)
			case syncer.RateLimited:
				// A quota reset can be an hour out; say how long, not just when.
				wait := max(time.Until(ev.Until), 0).Round(time.Second)
				fmt.Printf("  rate limited, sleeping %s until %s\n", wait, ev.Until.Local().Format("15:04:05"))
			case syncer.RepoDone:
				if ev.Err != nil {
					fmt.Printf("  %s FAILED: %v\n", ev.Repo, ev.Err)
				} else {
					fmt.Printf("  %s done (%d PRs updated)\n", ev.Repo, ev.PRs)
				}
			case syncer.Complete:
				failed = ev.Failed
				fmt.Printf("sync complete: %d PRs updated, %d repos failed\n", ev.TotalPRs, ev.Failed)
			}
		}
		if err := <-errCh; err != nil {
			return err
		}
		// A failed repo means the cache is stale for part of the team; exit
		// non-zero so cron and scripts notice instead of trusting old numbers.
		if failed > 0 {
			return fmt.Errorf("%d repo(s) failed to sync", failed)
		}
		return nil
	},
}

func init() {
	syncCmd.Flags().StringVar(&syncTeam, "team", "", "team profile to sync (default: config default_team)")
	syncCmd.Flags().IntVar(&syncBackfill, "backfill", 0, "override backfill window in days")
	rootCmd.AddCommand(syncCmd)
}
