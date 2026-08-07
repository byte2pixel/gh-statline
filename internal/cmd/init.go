package cmd

import (
	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/gh"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Add a team profile via the interactive setup wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		doer, err := gh.NewClient()
		if err != nil {
			return err
		}
		return runWizard(doer, false)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
