package cmd

import (
	"errors"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/app"
	"github.com/byte2pixel/gh-statline/internal/tui/wizard"
)

var rootTeam string

var rootCmd = &cobra.Command{
	Use:   "gh-statline",
	Short: "Statline — GitHub team statistics in your terminal",
	Long: `Statline is a terminal dashboard for GitHub team statistics.

See PRs opened and merged, reviews given (approved, commented, changes
requested), cycle times, PR sizes, and review comment activity for every
member of your team across the repos you care about.`,
	SilenceUsage:  true,
	SilenceErrors: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		doer, err := newClient()
		if err != nil {
			return err
		}

		env, err := bootstrap(rootTeam)
		if errors.Is(err, config.ErrNotFound) {
			if err := runWizard(doer, true); err != nil {
				return err
			}
			env, err = bootstrap(rootTeam)
		}
		if err != nil {
			return err
		}
		defer env.Close()

		model := app.New(app.Deps{
			DB:      env.DB,
			Store:   env.Store,
			Cfg:     env.Cfg,
			Team:    env.Team,
			TeamID:  env.TeamID,
			Targets: env.Targets,
			Doer:    doer,
		})
		_, err = runProgram(model)
		return err
	},
}

// runWizard walks the setup flow and appends the produced team profile to
// the config (creating it when firstRun).
func runWizard(doer gh.Doer, firstRun bool) error {
	cfg := config.Default()
	if !firstRun {
		loaded, err := config.Load()
		if err != nil && !errors.Is(err, config.ErrNotFound) {
			return err
		}
		if err == nil {
			cfg = loaded
		}
	}
	var existing []string
	for _, t := range cfg.Teams {
		existing = append(existing, t.Name)
	}

	final, err := runProgram(wizard.New(doer, existing))
	if err != nil {
		return err
	}
	team, err := wizard.Outcome(final)
	if err != nil {
		return err
	}
	if team == nil {
		return errors.New("setup aborted")
	}
	cfg.Teams = append(cfg.Teams, *team)
	if cfg.DefaultTeam == "" {
		cfg.DefaultTeam = team.Name
	}
	if err := config.Save(cfg); err != nil {
		return err
	}
	path, _ := config.FilePath()
	fmt.Printf("Saved team %q (%d members, %d repos) to %s\n",
		team.Name, len(team.Members), len(team.Repos), path)
	return nil
}

// Execute runs the root command. v is the release version injected by main.
func Execute(v string) {
	buildVersion = v
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVar(&rootTeam, "team", "", "team profile to show (default: config default_team)")
}
