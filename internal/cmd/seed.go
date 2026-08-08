package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/seed"
)

var (
	seedTeam    string
	seedMembers int
	seedDays    int
	seedSeed    int64
	seedWipe    bool
)

// seedCmd is a hidden developer tool: it fills a local-only demo team with
// deterministic fake history so the charts can be inspected at scale.
var seedCmd = &cobra.Command{
	Use:    "seed",
	Short:  "Populate a no-sync demo team with generated PR history",
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		opts := seed.Options{Members: seedMembers, Days: seedDays, Seed: seedSeed, Now: time.Now()}

		cfg, err := config.Load()
		if err == config.ErrNotFound {
			cfg = config.Default()
		} else if err != nil {
			return err
		}

		team := seed.Team(seedTeam, opts)
		replaced := false
		for i, t := range cfg.Teams {
			if t.Name == team.Name {
				cfg.Teams[i] = team
				replaced = true
				break
			}
		}
		if !replaced {
			cfg.Teams = append(cfg.Teams, team)
		}
		if cfg.DefaultTeam == "" {
			cfg.DefaultTeam = team.Name
		}
		if err := config.Save(cfg); err != nil {
			return err
		}

		dbPath, err := config.DBPath()
		if err != nil {
			return err
		}
		sqldb, err := db.Open(dbPath)
		if err != nil {
			return err
		}
		defer sqldb.Close()

		store := db.NewStore(sqldb)
		_, repoIDs, err := store.MirrorTeam(team)
		if err != nil {
			return err
		}

		if seedWipe {
			for _, id := range repoIDs {
				// reviews/issue_comments cascade with their PR.
				if _, err := sqldb.Exec(`DELETE FROM pull_requests WHERE repo_id = ?`, id); err != nil {
					return err
				}
			}
		}

		prs := seed.Generate(team, repoIDs, opts)
		for start := 0; start < len(prs); start += 200 {
			end := min(start+200, len(prs))
			if err := store.SavePullRequests(prs[start:end]); err != nil {
				return err
			}
		}

		// Mark the fake repos as freshly synced so nothing tries to walk them.
		now := opts.Now.Unix()
		horizon := opts.Now.AddDate(0, 0, -opts.Days).Unix()
		for _, id := range repoIDs {
			st := db.SyncState{RepoID: id, WatermarkUpdated: &now, BackfillUntil: &horizon, LastSyncedAt: &now}
			if err := store.SetSyncState(st); err != nil {
				return err
			}
		}

		fmt.Printf("seeded team %q: %d members, %d PRs across %d repos (%dd of history)\n",
			team.Name, len(team.Members), len(prs), len(repoIDs), opts.Days)
		fmt.Printf("open the app and press t to switch to %q\n", team.Name)
		return nil
	},
}

func init() {
	seedCmd.Flags().StringVar(&seedTeam, "team", "demo", "name for the demo team profile")
	seedCmd.Flags().IntVar(&seedMembers, "members", 38, "number of fake teammates (max 45)")
	seedCmd.Flags().IntVar(&seedDays, "days", 120, "days of history to generate")
	seedCmd.Flags().Int64Var(&seedSeed, "seed", 1, "RNG seed (same seed → same data)")
	seedCmd.Flags().BoolVar(&seedWipe, "wipe", false, "delete the demo repos' PRs before seeding")
	rootCmd.AddCommand(seedCmd)
}
