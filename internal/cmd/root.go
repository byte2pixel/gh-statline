package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

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
		fmt.Println("Statline TUI is under construction — try 'gh-statline version'")
		return nil
	},
}

// Execute runs the root command. v is the release version injected by main.
func Execute(v string) {
	buildVersion = v
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}
