package pages

import (
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Card exports are what the y key puts on the clipboard, and AGENTS.md holds
// them to the metric definitions exactly — enforced by review alone until
// now. These pin every card's table.

// exportWindow is pinned: a card export prints the window label, and half
// the cards print dates.
var exportWindow = metrics.LastDays(30, time.Date(2026, 6, 15, 12, 0, 0, 0, time.UTC))

func exportDay(n int) time.Time {
	return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, n)
}

// exportDash is small and fully pinned: every exported cell has a value a
// reader can check by eye, and no offset hangs off the wall clock.
func exportDash() metrics.Dashboard {
	d := metrics.Dashboard{
		Rows: []metrics.Row{
			{Login: "alice", PRsOpened: 4, PRsMerged: 3, ReviewsGiven: 6,
				Approved: 3, Commented: 2, ChangesReq: 1,
				CommentsGiven: 7, CommentsRecv: 2, SizeP50: 40},
			// Silent both ways: the comments card must leave bob out entirely.
			{Login: "bob", PRsOpened: 1, SizeP50: -1},
			{Login: "carol", PRsOpened: 2, PRsMerged: 2, ReviewsGiven: 4,
				Approved: 4, CommentsGiven: 1, CommentsRecv: 9, SizeP50: 12},
		},
		Buckets: []metrics.Bucket{
			{Day: exportDay(0), Opened: 3, Merged: 1},
			{Day: exportDay(1), Opened: 0, Merged: 2},
		},
		Trend: []metrics.TrendPoint{
			{WeekStart: exportDay(0), Median: 2 * time.Hour, Merged: 4},
			// No merges that week: the median is the 0 sentinel, not "0s".
			{WeekStart: exportDay(7), Median: 0, Merged: 0},
		},
		TTFR:  metrics.Dist{Labels: []string{"<1h", "<4h"}, Counts: []int{3, 1}},
		Sizes: metrics.Dist{Labels: []string{"XS", "S"}, Counts: []int{5, 2}},
		Matrix: metrics.Matrix{
			Logins:  []string{"alice", "carol"},
			Authors: []string{"alice", "carol", "(others)"},
			// Self-review cells are zero everywhere and must not be exported.
			Counts: [][]int{{0, 2, 1}, {3, 0, 0}},
			Max:    3,
		},
		Aging: metrics.Aging{
			Buckets: metrics.Dist{Labels: []string{"<1d", "2w+"}, Counts: []int{1, 1}},
			Total:   2,
			Stalest: []metrics.StalePR{
				{Repo: "acme/api", Number: 12, Title: "old one", Author: "bob", AgeDays: 95},
			},
		},
	}
	d.Punch.Counts[2][9] = 4  // Wed 09:00
	d.Punch.Counts[4][17] = 1 // Fri 17:00
	d.Punch.Max, d.Punch.Total = 4, 5
	return d
}

// exportTrends is three weeks of pinned team totals, enough for the count
// and duration cards to print a full column.
func exportTrends() metrics.TrendData {
	return metrics.TrendData{
		Weeks: []time.Time{exportDay(0), exportDay(7), exportDay(14)},
		Team: metrics.TeamTrend{
			Opened:   []int{3, 5, 4},
			Merged:   []int{2, 4, 3},
			Reviews:  []int{7, 9, 8},
			Comments: []int{1, 0, 2},
			Cycle:    []time.Duration{2 * time.Hour, 0, 45 * time.Minute},
			TTFR:     []time.Duration{30 * time.Minute, 0, time.Hour},
		},
		Members: []metrics.MemberTrend{{
			Login: "alice", Opened: []int{1, 2, 2}, Merged: []int{1, 1, 1},
			Reviews: []int{2, 3, 3}, Comments: []int{0, 0, 1},
		}},
	}
}

func chartsExport(t *testing.T, key string) string {
	t.Helper()
	th := theme.New(true)
	c := NewCharts(&th, keys.Default())
	c.SetSize(120, 40)
	_ = c.SetData(exportDash())
	c.grid.openFull(key)
	if !c.Fullscreen() {
		t.Fatalf("card %q did not open fullscreen; the export would be the team table", key)
	}
	return c.Export("acme", exportWindow)
}

func trendsExport(t *testing.T, key string) string {
	t.Helper()
	tr := newTestTrends(120, 40, exportTrends())
	tr.grid.openFull(key)
	if !tr.Fullscreen() {
		t.Fatalf("trend card %q did not open fullscreen", key)
	}
	return tr.Export("acme", exportWindow)
}

// mdCells splits a Markdown table row on its unescaped pipes. A well-formed
// row is bounded by pipes, so the pieces outside them are empty and dropped.
//
// A row that is not bounded is a bug in the export rather than in the test,
// and it is reported as one: a missing trailing pipe would otherwise drop the
// last cell here and resurface as an unexplained off-by-one against the
// header, several assertions away from the code that caused it.
func mdCells(t *testing.T, line string) []string {
	t.Helper()
	var pieces []string
	var cur strings.Builder
	for i := 0; i < len(line); i++ {
		if line[i] == '|' && (i == 0 || line[i-1] != '\\') {
			pieces = append(pieces, strings.TrimSpace(cur.String()))
			cur.Reset()
			continue
		}
		cur.WriteByte(line[i])
	}
	pieces = append(pieces, strings.TrimSpace(cur.String()))
	if len(pieces) < 2 || pieces[0] != "" || pieces[len(pieces)-1] != "" {
		t.Fatalf("table row is not bounded by unescaped pipes: %q", line)
	}
	return pieces[1 : len(pieces)-1]
}

// mdTable pulls the header and data rows out of an exported card table.
func mdTable(t *testing.T, md string) (headers []string, rows [][]string) {
	t.Helper()
	for _, line := range strings.Split(strings.TrimSpace(md), "\n") {
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := mdCells(t, line)
		switch {
		case headers == nil:
			headers = cells
		case len(cells) > 0 && strings.HasPrefix(cells[0], "---"):
			// alignment row
		default:
			rows = append(rows, cells)
		}
	}
	return headers, rows
}

func assertTable(t *testing.T, md, title string, wantHeaders []string, wantRows [][]string) {
	t.Helper()
	if !strings.HasPrefix(md, "## "+title) {
		t.Errorf("export should lead with %q:\n%s", "## "+title, md)
	}
	headers, rows := mdTable(t, md)
	if !slices.Equal(headers, wantHeaders) {
		t.Errorf("headers = %q, want %q", headers, wantHeaders)
	}
	if len(rows) != len(wantRows) {
		t.Fatalf("got %d rows, want %d:\n%s", len(rows), len(wantRows), md)
	}
	for i := range rows {
		if !slices.Equal(rows[i], wantRows[i]) {
			t.Errorf("row %d = %q, want %q", i, rows[i], wantRows[i])
		}
	}
}

// The chart cards whose export had no test at all. Each table is the card's
// own numbers, in the card's own order.
func TestChartCardExports(t *testing.T) {
	cases := []struct {
		key, title string
		headers    []string
		rows       [][]string
	}{
		{
			key: "throughput", title: "PR throughput — Last 30 days",
			headers: []string{"Period", "Opened", "Merged"},
			rows:    [][]string{{"2026-06-01", "3", "1"}, {"2026-06-02", "0", "2"}},
		},
		{
			// A week with no merges exports the no-data dash, not "0s".
			key: "cycle", title: "Cycle time — Last 30 days",
			headers: []string{"Week", "Median cycle", "Merged"},
			rows:    [][]string{{"2026-06-01", "2.0h", "4"}, {"2026-06-08", "–", "0"}},
		},
		{
			// Only cells with reviews, reviewer-major, with "(others)"
			// carried through as an ordinary author column.
			key: "matrix", title: "Who reviews whom — Last 30 days",
			headers: []string{"Reviewer", "Author", "Reviews"},
			rows: [][]string{
				{"alice", "carol", "2"},
				{"alice", "(others)", "1"},
				{"carol", "alice", "3"},
			},
		},
		{
			key: "ttfr", title: "First review after — Last 30 days",
			headers: []string{"Waited", "PRs"},
			rows:    [][]string{{"<1h", "3"}, {"<4h", "1"}},
		},
		{
			key: "size", title: "PR size — Last 30 days",
			headers: []string{"Size", "PRs"},
			rows:    [][]string{{"XS", "5"}, {"S", "2"}},
		},
		{
			// Busiest first by given+received, and members with neither are
			// left out rather than exported as zero rows.
			key: "comments", title: "Comments — Last 30 days",
			headers: []string{"Member", "Given", "Received"},
			rows:    [][]string{{"carol", "1", "9"}, {"alice", "7", "2"}},
		},
		{
			// Local-time buckets, day-major, and only the hours with events.
			key: "punch", title: "Activity punch card — Last 30 days",
			headers: []string{"Day", "Hour", "Events"},
			rows:    [][]string{{"Wed", "09:00", "4"}, {"Fri", "17:00", "1"}},
		},
	}
	for _, c := range cases {
		t.Run(c.key, func(t *testing.T) {
			assertTable(t, chartsExport(t, c.key), c.title, c.headers, c.rows)
		})
	}
}

// The duration trend cards print the same no-data dash the charts do, rather
// than formatting the zero sentinel as a duration.
func TestTrendDurationCardExports(t *testing.T) {
	assertTable(t, trendsExport(t, "cycle"), "Cycle p50 — weekly trend",
		[]string{"Week", "Cycle p50"},
		[][]string{{"2026-06-01", "2.0h"}, {"2026-06-08", "–"}, {"2026-06-15", "45m"}})

	assertTable(t, trendsExport(t, "ttfr"), "TTFR p50 — weekly trend",
		[]string{"Week", "TTFR p50"},
		[][]string{{"2026-06-01", "30m"}, {"2026-06-08", "–"}, {"2026-06-15", "60m"}})
}

func TestTrendCountCardExport(t *testing.T) {
	assertTable(t, trendsExport(t, "opened"), "PRs opened — weekly trend",
		[]string{"Week", "PRs opened"},
		[][]string{{"2026-06-01", "3"}, {"2026-06-08", "5"}, {"2026-06-15", "4"}})
}

// PR titles come from the GitHub API and routinely contain pipes, which
// export.Table escapes. The escaped pipe must stay inside its cell: a split
// on it would add a phantom column, and the sweep below counts cells.
func TestCardExportKeepsAnEscapedPipeInOneCell(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th, keys.Default())
	c.SetSize(120, 40)
	d := exportDash()
	d.Aging.Stalest = []metrics.StalePR{
		{Repo: "acme/api", Number: 12, Title: "fix: a|b", Author: "bob", AgeDays: 95},
	}
	_ = c.SetData(d)
	c.grid.openFull("aging")

	headers, rows := mdTable(t, c.Export("acme", exportWindow))
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1", len(rows))
	}
	if len(rows[0]) != len(headers) {
		t.Fatalf("row has %d cells, header has %d — the escaped pipe split the row: %q",
			len(rows[0]), len(headers), rows[0])
	}
	if !slices.Contains(rows[0], `fix: a\|b`) {
		t.Errorf("escaped title is not one cell: %q", rows[0])
	}
}

// Whatever a card exports, every row has to carry exactly as many cells as
// its header: export.Table pads nothing, so a short or long row lands ragged
// in whatever the clipboard is pasted into.
func TestEveryCardExportIsRectangular(t *testing.T) {
	check := func(t *testing.T, md string) {
		t.Helper()
		headers, rows := mdTable(t, md)
		if len(headers) == 0 {
			t.Fatalf("no table in the export:\n%s", md)
		}
		for i, r := range rows {
			if len(r) != len(headers) {
				t.Errorf("row %d has %d cells, header has %d:\n%s", i, len(r), len(headers), md)
			}
		}
	}
	for _, key := range []string{
		"throughput", "outcomes", "cycle", "matrix", "ttfr", "size", "comments", "aging", "punch",
	} {
		t.Run("charts/"+key, func(t *testing.T) { check(t, chartsExport(t, key)) })
	}
	for _, key := range []string{"opened", "merged", "reviews", "comments", "cycle", "ttfr", "movers"} {
		t.Run("trends/"+key, func(t *testing.T) { check(t, trendsExport(t, key)) })
	}
}
