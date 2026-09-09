package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
)

var (
	exportView   string
	exportFormat string
	exportTeam   string
	exportMember string
	exportWindow string
	exportFrom   string
	exportTo     string
	exportFile   string
)

var exportCmd = &cobra.Command{
	Use:   "export",
	Short: "Print a view as Markdown, CSV or JSON (no TUI)",
	Long: `Render one of statline's views to stdout or a file.

The numbers the dashboard shows, in a format something else can read:
Markdown for a standup, CSV for a spreadsheet, JSON for a script. In the
app, y copies the current view as Markdown; this is that without a terminal
or a clipboard.

Reads the cache only, so it works offline and on local-only (no_sync) teams.
Run sync first if the numbers should be fresh.

  gh statline export --view team --format csv --window 30d
  gh statline export --view person --member alice --format json
  gh statline export --view trends --output trends.md
  gh statline export --view sync --format json | jq '.repos[] | select(.last_error)'

CSV writes machine column names as its header row, not the display
headings, so a spreadsheet column keeps its name when the UI rewords one. A
view made of two tables writes both, separated by a blank line.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		render, err := renderer(exportFormat)
		if err != nil {
			return err
		}
		env, err := bootstrap(exportTeam)
		if err != nil {
			return err
		}
		defer env.Close()

		now := time.Now()
		w, err := exportWindowFor(env.Cfg, now)
		if err != nil {
			return err
		}
		doc, err := buildDoc(env, w, now)
		if err != nil {
			return err
		}
		doc.Meta.Team = env.Team.Name
		doc.Meta.GeneratedAt = now

		out, err := render(doc)
		if err != nil {
			return err
		}
		if exportFile == "" || exportFile == "-" {
			_, err = fmt.Fprint(cmd.OutOrStdout(), out)
			return err
		}
		if err := os.WriteFile(exportFile, []byte(out), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", exportFile, err)
		}
		return nil
	},
}

// buildDoc runs the queries behind one view. They are the same metrics
// calls the TUI makes, so an export is only ever a rendering of what the
// dashboard would show.
//
// A failing repo does not fail this command the way it fails doctor: a
// pipeline asking for the numbers gets the numbers. `--view sync` is how a
// script asks about health.
func buildDoc(env *appEnv, w metrics.Window, now time.Time) (export.Doc, error) {
	f := metrics.Filter{TeamID: env.TeamID, Bots: config.NewBotMatcher(env.Cfg.ExcludeBots)}

	switch exportView {
	case "team":
		rows, err := metrics.TeamStats(env.DB, f, w)
		if err != nil {
			return export.Doc{}, err
		}
		return export.TeamDoc(env.Team.Name, w, rows), nil

	case "person":
		if exportMember == "" {
			return export.Doc{}, fmt.Errorf("--view person needs --member <login>")
		}
		rows, err := metrics.TeamStats(env.DB, f, w)
		if err != nil {
			return export.Doc{}, err
		}
		// The row comes from the same TeamStats call the team view uses, so
		// both show the same numbers. No row means the login is not on the
		// team, or is hidden, or is a bot.
		var row metrics.Row
		found := false
		for _, r := range rows {
			if strings.EqualFold(r.Login, exportMember) {
				row, found = r, true
				break
			}
		}
		if !found {
			return export.Doc{}, fmt.Errorf("%q is not a visible member of team %q", exportMember, env.Team.Name)
		}
		d, err := metrics.LoadPerson(env.DB, f, w, row.Login)
		if err != nil {
			return export.Doc{}, err
		}
		return export.PersonDoc(row.Login, w, row, d.Repos), nil

	case "trends":
		d, err := metrics.TrendSeries(env.DB, f, metrics.TrendWeeks, now)
		if err != nil {
			return export.Doc{}, err
		}
		risers, fallers := metrics.Movers(d.Members, 3)
		return export.TrendsDoc(env.Team.Name, d, risers, fallers), nil

	case "sync":
		rep, err := doctor.Load(env.Store, env.Team, env.TeamID, now)
		if err != nil {
			return export.Doc{}, err
		}
		return export.SyncStatusDoc(rep), nil
	}
	return export.Doc{}, fmt.Errorf("unknown view %q: want team, person, trends or sync", exportView)
}

func renderer(format string) (func(export.Doc) (string, error), error) {
	switch format {
	case "md", "markdown":
		// The renderer the y key ends up in, movers bullets and sync
		// failures included.
		return func(d export.Doc) (string, error) { return export.Markdown(d), nil }, nil
	case "csv":
		return export.CSV, nil
	case "json":
		return func(d export.Doc) (string, error) {
			b, err := export.JSON(d)
			return string(b), err
		}, nil
	}
	return nil, fmt.Errorf("unknown format %q: want md, csv or json", format)
}

// exportWindowFor resolves the window flags. --from/--to is the custom
// range the r key offers; otherwise days, defaulting to the configured
// window.
func exportWindowFor(cfg config.Config, now time.Time) (metrics.Window, error) {
	if exportFrom != "" || exportTo != "" {
		if exportFrom == "" || exportTo == "" {
			return metrics.Window{}, fmt.Errorf("--from and --to go together")
		}
		from, err := time.Parse(dateLayout, exportFrom)
		if err != nil {
			return metrics.Window{}, fmt.Errorf("--from %q: want YYYY-MM-DD", exportFrom)
		}
		to, err := time.Parse(dateLayout, exportTo)
		if err != nil {
			return metrics.Window{}, fmt.Errorf("--to %q: want YYYY-MM-DD", exportTo)
		}
		if to.Before(from) {
			return metrics.Window{}, fmt.Errorf("--to %s is before --from %s", exportTo, exportFrom)
		}
		return metrics.Range(from, to), nil
	}

	spec := exportWindow
	if spec == "" {
		spec = cfg.UI.Window
	}
	days, err := strconv.Atoi(strings.TrimSuffix(spec, "d"))
	if err != nil || days <= 0 {
		return metrics.Window{}, fmt.Errorf("--window %q: want a number of days, like 30d", spec)
	}
	return metrics.LastDays(days, now), nil
}

const dateLayout = "2006-01-02"

func init() {
	exportCmd.Flags().StringVar(&exportView, "view", "team", "view to export: team, person, trends, sync")
	exportCmd.Flags().StringVar(&exportFormat, "format", "md", "output format: md, csv, json")
	exportCmd.Flags().StringVar(&exportTeam, "team", "", "team profile to export (default: config default_team)")
	exportCmd.Flags().StringVar(&exportMember, "member", "", "login to drill into (required by --view person)")
	exportCmd.Flags().StringVar(&exportWindow, "window", "", "window in days, e.g. 30d (default: config ui.window)")
	exportCmd.Flags().StringVar(&exportFrom, "from", "", "custom range start, YYYY-MM-DD (with --to)")
	exportCmd.Flags().StringVar(&exportTo, "to", "", "custom range end, YYYY-MM-DD (with --from)")
	exportCmd.Flags().StringVarP(&exportFile, "output", "o", "", "write to a file instead of stdout")
	rootCmd.AddCommand(exportCmd)
}
