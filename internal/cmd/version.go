package cmd

import (
	"runtime/debug"

	"github.com/spf13/cobra"
)

// buildVersion is set by Execute from main's ldflags-injected version.
var buildVersion string

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print the Statline version",
	Run: func(cmd *cobra.Command, args []string) {
		printer{cmd.OutOrStdout()}.printf("statline %s\n", resolveVersion())
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
	// A non-empty Version is what makes cobra honour --version on the root
	// command. Set here so the flag works for tests that drive rootCmd
	// without going through Execute; Execute sets it again once main has
	// handed over the injected release version. The flag is declared rather
	// than left to cobra so its help line reads like the subcommand's, and
	// the template prints the same line the subcommand does.
	rootCmd.Version = resolveVersion()
	rootCmd.Flags().BoolP("version", "v", false, "Print the Statline version")
	rootCmd.SetVersionTemplate("statline {{.Version}}\n")
}
