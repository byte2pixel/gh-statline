package cmd

import (
	"github.com/spf13/cobra"
)

var initCmd = &cobra.Command{
	Use:   "init",
	Short: "Add a team profile via the interactive setup wizard",
	RunE: func(cmd *cobra.Command, args []string) error {
		doer, err := newClient()
		if err != nil {
			return err
		}
		return runWizard(doer, false)
	},
}

func init() {
	rootCmd.AddCommand(initCmd)
}
