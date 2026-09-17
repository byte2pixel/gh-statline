package seed_test

import (
	"database/sql"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/export"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/seed"
)

// The seeded 38-member team, rendered through every metrics entry point to
// Markdown and pinned byte for byte under testdata/. These goldens are the
// proof that a refactor of the metric SQL (#106) moved no number: the
// exclusion-rule rewrite must pass this test without -update.
//
// Regenerate deliberately, and say why in the PR, with:
//
//	go test ./internal/seed -run Golden -update
var update = flag.Bool("update", false, "rewrite the seed golden files")

// goldenNow is the dump harness's pinned clock (STATLINE_DUMP_NOW), so a
// headless dump and these goldens describe the same instant.
var goldenNow = time.Date(2026, 9, 6, 0, 0, 0, 0, time.UTC)

const goldenTeam = "demo"

// goldenPerson is namePool[0]: the first member, present in every window.
const goldenPerson = "aiko"

func TestSeedGoldens(t *testing.T) {
	// The punch card is the one metric in local time; pin the zone so the
	// goldens agree on every machine and in CI.
	prev := time.Local
	time.Local = time.UTC
	t.Cleanup(func() { time.Local = prev })

	sqldb, f := seedStore(t, seed.Options{Members: 38, Days: 120, Seed: 1, Now: goldenNow})

	views := []struct {
		name   string
		render func() (string, error)
	}{
		{"team-30d", func() (string, error) { return renderTeam(sqldb, f, 30) }},
		{"team-90d", func() (string, error) { return renderTeam(sqldb, f, 90) }},
		{"person-90d", func() (string, error) { return renderPerson(sqldb, f, 90) }},
		{"trends", func() (string, error) { return renderTrends(sqldb, f) }},
		{"charts-30d", func() (string, error) { return renderCharts(sqldb, f, 30) }},
		{"charts-90d", func() (string, error) { return renderCharts(sqldb, f, 90) }},
	}
	for _, v := range views {
		t.Run(v.name, func(t *testing.T) {
			got, err := v.render()
			if err != nil {
				t.Fatal(err)
			}
			path := filepath.Join("testdata", v.name+".golden.md")
			if *update {
				if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if got != string(want) {
				t.Errorf("%s drifted from %s (rerun with -update only if the numbers were meant to move)\n%s",
					v.name, path, firstDiff(got, string(want)))
			}
		})
	}
}

// firstDiff points at the first line that differs so a failure reads as a
// number, not two hundred lines of table.
func firstDiff(got, want string) string {
	g, w := strings.Split(got, "\n"), strings.Split(want, "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			return fmt.Sprintf("line %d:\n  got:  %q\n  want: %q", i+1, gl, wl)
		}
	}
	return "(lengths differ)"
}

func renderTeam(sqldb *sql.DB, f metrics.Filter, days int) (string, error) {
	w := metrics.LastDays(days, goldenNow)
	rows, err := metrics.TeamStats(sqldb, f, w)
	if err != nil {
		return "", err
	}
	return export.TeamStats(goldenTeam, w, rows), nil
}

// renderPerson is the person drill-down as the export command builds it,
// plus the daily activity pulse behind the sparkline.
func renderPerson(sqldb *sql.DB, f metrics.Filter, days int) (string, error) {
	w := metrics.LastDays(days, goldenNow)
	rows, err := metrics.TeamStats(sqldb, f, w)
	if err != nil {
		return "", err
	}
	var row metrics.Row
	found := false
	for _, r := range rows {
		if r.Login == goldenPerson {
			row, found = r, true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%q is not a visible member", goldenPerson)
	}
	d, err := metrics.LoadPerson(sqldb, f, w, goldenPerson)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(export.Person(goldenPerson, w, row, d.Repos))
	b.WriteString("\n")
	start := time.Unix(w.Start, 0).UTC().Truncate(24 * time.Hour)
	var act [][]string
	for i, v := range d.Activity {
		if v > 0 {
			act = append(act, []string{start.AddDate(0, 0, i).Format("2006-01-02"), strconv.FormatFloat(v, 'f', -1, 64)})
		}
	}
	b.WriteString(export.Table("Activity — "+w.Label, []string{"Day", "Events"}, act))
	return b.String(), nil
}

// renderTrends is the trends export plus every member's weekly series, so
// the movers and the numbers they were ranked from are both pinned.
func renderTrends(sqldb *sql.DB, f metrics.Filter) (string, error) {
	d, err := metrics.TrendSeries(sqldb, f, metrics.TrendWeeks, goldenNow)
	if err != nil {
		return "", err
	}
	risers, fallers := metrics.Movers(d.Members, 3)
	var b strings.Builder
	b.WriteString(export.Trends(goldenTeam, d, risers, fallers))
	b.WriteString("\n")
	headers := []string{"Member", "Metric"}
	for _, wk := range d.Weeks {
		headers = append(headers, wk.Format("01-02"))
	}
	var rows [][]string
	for _, m := range d.Members {
		for _, metric := range metrics.CountMetrics {
			row := []string{m.Login, metric.String()}
			for _, v := range metric.Of(m) {
				row = append(row, strconv.Itoa(v))
			}
			rows = append(rows, row)
		}
	}
	b.WriteString(export.Table("Members — weekly", headers, rows))
	return b.String(), nil
}

// renderCharts is every dataset behind the charts dashboard, each as the
// table its card exports.
func renderCharts(sqldb *sql.DB, f metrics.Filter, days int) (string, error) {
	w := metrics.LastDays(days, goldenNow)
	d, err := metrics.LoadDashboard(sqldb, f, w, goldenNow)
	if err != nil {
		return "", err
	}
	var tables []string
	add := func(title string, headers []string, rows [][]string) {
		tables = append(tables, export.Table(title+" — "+w.Label, headers, rows))
	}

	tiles := [][]string{
		{"Cycle p50", dur(d.Tiles.Cycle)},
		{"TTFR p50", dur(d.Tiles.TTFR)},
		{"Has previous window", strconv.FormatBool(d.Tiles.HasPrev)},
	}
	if d.Tiles.HasPrev {
		tiles = append(tiles,
			[]string{"Prev opened", strconv.Itoa(d.Tiles.PrevOpened)},
			[]string{"Prev merged", strconv.Itoa(d.Tiles.PrevMerged)},
			[]string{"Prev reviews", strconv.Itoa(d.Tiles.PrevReviews)},
			[]string{"Prev comments", strconv.Itoa(d.Tiles.PrevComments)},
			[]string{"Prev cycle p50", dur(d.Tiles.PrevCycle)},
			[]string{"Prev TTFR p50", dur(d.Tiles.PrevTTFR)},
		)
	}
	add("Stat tiles", []string{"Tile", "Value"}, tiles)

	var rows [][]string
	for _, bk := range d.Buckets {
		rows = append(rows, []string{bk.Day.Format("2006-01-02"), strconv.Itoa(bk.Opened), strconv.Itoa(bk.Merged)})
	}
	add("Throughput", []string{"Period", "Opened", "Merged"}, rows)

	rows = nil
	for _, p := range d.Trend {
		rows = append(rows, []string{p.WeekStart.Format("2006-01-02"), dur(p.Median), strconv.Itoa(p.Merged)})
	}
	add("Cycle time", []string{"Week", "Median cycle", "Merged"}, rows)

	add("Time to first review", []string{"Waited", "PRs"}, distRows(d.TTFR))
	add("PR size", []string{"Lines", "PRs"}, distRows(d.Sizes))

	rows = nil
	for r, reviewer := range d.Matrix.Logins {
		for a, author := range d.Matrix.Authors {
			if n := d.Matrix.Counts[r][a]; n > 0 {
				rows = append(rows, []string{reviewer, author, strconv.Itoa(n)})
			}
		}
	}
	add("Review matrix", []string{"Reviewer", "Author", "Reviews"}, rows)

	rows = nil
	punchDays := []string{"Mon", "Tue", "Wed", "Thu", "Fri", "Sat", "Sun"}
	for day := 0; day < 7; day++ {
		for hh := 0; hh < 24; hh++ {
			if n := d.Punch.Counts[day][hh]; n > 0 {
				rows = append(rows, []string{punchDays[day], fmt.Sprintf("%02d:00", hh), strconv.Itoa(n)})
			}
		}
	}
	add("Activity punch card", []string{"Day", "Hour", "Events"}, rows)

	add("Open PR age", []string{"Age", "PRs"}, distRows(d.Aging.Buckets))
	rows = nil
	for _, s := range d.Aging.Stalest {
		rows = append(rows, []string{s.Repo, strconv.Itoa(s.Number), s.Title, s.Author, strconv.Itoa(s.AgeDays)})
	}
	add("Stalest open PRs", []string{"Repo", "PR", "Title", "Author", "Age (days)"}, rows)

	return strings.Join(tables, "\n"), nil
}

func distRows(d metrics.Dist) [][]string {
	rows := make([][]string, len(d.Labels))
	for i, l := range d.Labels {
		rows[i] = []string{l, strconv.Itoa(d.Counts[i])}
	}
	return rows
}

// dur formats a median the way the cards do: the no-data dash for zero.
func dur(d time.Duration) string {
	if d == 0 {
		return "–"
	}
	return metrics.FmtDur(d)
}
