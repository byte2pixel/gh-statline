package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/gh"
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

		doer, err := gh.NewClient()
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

		for ev := range events {
			switch ev := ev.(type) {
			case syncer.RepoStarted:
				fmt.Printf("syncing %s...\n", ev.Repo)
			case syncer.RepoPage:
				fmt.Printf("  %s: %d PRs\n", ev.Repo, ev.PRs)
			case syncer.RateLimited:
				fmt.Printf("  rate limited, sleeping until %s\n", ev.Until.Local().Format("15:04:05"))
			case syncer.RepoDone:
				if ev.Err != nil {
					fmt.Printf("  %s FAILED: %v\n", ev.Repo, ev.Err)
				} else {
					fmt.Printf("  %s done (%d PRs updated)\n", ev.Repo, ev.PRs)
				}
			case syncer.Complete:
				fmt.Printf("sync complete: %d PRs updated, %d repos failed\n", ev.TotalPRs, ev.Failed)
			}
		}
		return <-errCh
	},
}

func init() {
	syncCmd.Flags().StringVar(&syncTeam, "team", "", "team profile to sync (default: config default_team)")
	syncCmd.Flags().IntVar(&syncBackfill, "backfill", 0, "override backfill window in days")
	rootCmd.AddCommand(syncCmd)
}
