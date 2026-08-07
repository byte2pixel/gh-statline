package cmd

import (
	"errors"
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/app"
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
		env, err := bootstrap(rootTeam)
		if errors.Is(err, config.ErrNotFound) {
			return printFirstRunHint()
		}
		if err != nil {
			return err
		}
		defer env.Close()

		doer, err := gh.NewClient()
		if err != nil {
			return err
		}

		model := app.New(app.Deps{
			DB:      env.DB,
			Store:   env.Store,
			Cfg:     env.Cfg,
			Team:    env.Team,
			TeamID:  env.TeamID,
			Targets: env.Targets,
			Doer:    doer,
		})
		_, err = tea.NewProgram(model).Run()
		return err
	},
}

// printFirstRunHint stands in for the setup wizard until it ships.
func printFirstRunHint() error {
	path, _ := config.FilePath()
	fmt.Printf(`No config found. Create %s like:

default_team: myteam
teams:
  - name: myteam
    org: my-github-org
    members:
      - {login: alice}
      - {login: bob}
    repos:
      - {owner: my-github-org, name: repo-one}
      - {owner: my-github-org, name: repo-two}

Then run gh-statline again. (An interactive setup wizard is coming.)
`, path)
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
