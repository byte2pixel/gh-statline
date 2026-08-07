package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// buildVersion is set by Execute from main's ldflags-injected version.
var buildVersion string

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Statline version",
	Run: func(cmd *cobra.Command, args []string) {
		fmt.Println("statline", resolveVersion())
	},
}

func resolveVersion() string {
	if buildVersion != "" {
		return buildVersion
	}
	if info, ok := debug.ReadBuildInfo(); ok && info.Main.Version != "" {
		return info.Main.Version
	}
	return "dev"
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
