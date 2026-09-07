package cmd

import (
	"fmt"
	"io"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/doctor"
)

var doctorTeam string

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Report per-repo sync health from the local cache (no TUI)",
	Long: `Print what the cache knows about every configured repo: when it last synced
cleanly, how far back it honestly covers, and why the last sync failed.

Reads the cache only — it never contacts GitHub, so it works offline and on
local-only (no_sync) teams. Exits non-zero when any repo is failing, so a
cron job that has quietly stopped updating shows up as a failure instead of
as numbers that stopped moving.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		env, err := bootstrap(doctorTeam)
		if err != nil {
			return err
		}
		defer env.Close()

		rep, err := doctor.Load(env.Store, env.Team, env.TeamID, time.Now())
		if err != nil {
			return err
		}
		printReport(cmd.OutOrStdout(), rep)
		if !rep.Healthy() {
			return fmt.Errorf("%d repo(s) failing to sync", rep.Failing)
		}
		return nil
	},
}

// printer swallows write errors. A report whose destination has gone away
// has nowhere left to report that, and errcheck will not take a bare
// fmt.Fprintf.
type printer struct{ w io.Writer }

func (p printer) printf(format string, a ...any) { _, _ = fmt.Fprintf(p.w, format, a...) }

// printReport lays the report out for a terminal or a cron log. The
// failures get their own block below the table rather than sitting inline
// under their rows: a tabwriter column block ends at the first line without
// tabs, so an inline error would break the alignment of every row after it.
func printReport(w io.Writer, rep doctor.Report) {
	out := printer{w}
	out.printf("Statline doctor — team %s\n\n", rep.Team)
	// The paths are here because "doctor" is where someone looks when they
	// need to edit the config or delete the cache, and both locations are
	// platform-dependent and overridable by environment variable.
	if p, err := config.FilePath(); err == nil {
		out.printf("  config  %s\n", p)
	}
	if p, err := config.DBPath(); err == nil {
		out.printf("  cache   %s\n", p)
	}
	out.printf("\n%s\n", rep.Summary())
	if len(rep.Rows) == 0 {
		return
	}

	tw := tabwriter.NewWriter(w, 0, 0, 3, ' ', 0)
	table := printer{tw}
	table.printf("\nREPO\tLAST SYNC\tCOVERS SINCE\tNEWEST PR\tSTATUS\n")
	for _, r := range rep.Rows {
		table.printf("%s\t%s\t%s\t%s\t%s\n", r.Repo, r.LastSynced, r.Covers, r.NewestPR, r.Status)
	}
	_ = tw.Flush()

	if rep.Failing == 0 {
		return
	}
	out.printf("\nFailures:\n")
	for _, r := range rep.Rows {
		if r.Status != doctor.StatusFailing {
			continue
		}
		out.printf("  %s\n    %s\n", r.Repo, r.Error)
		if r.Hint != "" {
			out.printf("    → %s\n", r.Hint)
		}
	}
}

func init() {
	doctorCmd.Flags().StringVar(&doctorTeam, "team", "", "team profile to check (default: config default_team)")
	rootCmd.AddCommand(doctorCmd)
}
