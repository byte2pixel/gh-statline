package export

import (
	"strings"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/metrics"
)

// unescapedPipes counts the pipes that still act as cell separators.
func unescapedPipes(line string) int {
	n := 0
	for i := 0; i < len(line); i++ {
		if line[i] == '|' && (i == 0 || line[i-1] != '\\') {
			n++
		}
	}
	return n
}

// PR titles come from the GitHub API. A pipe in one used to end the cell
// early and shift every later column; a newline ended the row outright.
func TestTableSurvivesPipesAndNewlinesInCells(t *testing.T) {
	headers := []string{"Repo", "PR", "Title", "Author", "Age (days)"}
	rows := [][]string{{"acme/api", "42", "fix: a|b\nsecond line", "alice", "9"}}

	out := Table("Oldest open PRs", headers, rows)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	data := lines[len(lines)-1]

	if got, want := unescapedPipes(data), len(headers)+1; got != want {
		t.Errorf("row has %d cell separators, want %d:\n%s", got, want, data)
	}
	if !strings.Contains(data, `fix: a\|b second line`) {
		t.Errorf("pipe not escaped or newline not flattened:\n%s", data)
	}
	// Heading, blank, header, separator, one data row.
	if len(lines) != 5 {
		t.Errorf("newline in a cell split the row: got %d lines\n%s", len(lines), out)
	}
}

// A PR title can carry terminal escape sequences; pasting an exported table
// must never replay them (gh #43).
func TestTableStripsEscapeSequences(t *testing.T) {
	rows := [][]string{{"acme/api", "7", "evil \x1b]0;pwned\x07 \x1b[31mtitle 你好", "mal", "40"}}
	out := Table("Open \x1b[2Jnow", []string{"Repo", "PR", "Title", "Author", "Age (days)"}, rows)

	for _, bad := range []string{"\x1b", "\a", "pwned"} {
		if strings.Contains(out, bad) {
			t.Errorf("export contains %q:\n%q", bad, out)
		}
	}
	for _, want := range []string{"## Open now", "evil  title 你好"} {
		if !strings.Contains(out, want) {
			t.Errorf("printable text mangled, want %q in:\n%s", want, out)
		}
	}
}

func TestTableRightAlignsNumericColumns(t *testing.T) {
	headers := []string{"Repo", "PR", "Title", "Author", "Age (days)"}
	rows := [][]string{
		{"acme/api", "42", "fix thing", "alice", "9"},
		{"acme/web", "7", "another", "bob", "12"},
	}

	sep := strings.Split(Table("t", headers, rows), "\n")[3]
	if want := "|---|---:|---|---|---:|"; sep != want {
		t.Errorf("separator = %q, want %q", sep, want)
	}
}

// A column of dashes is "no data", not text, so it must not drag the column
// out of numeric alignment.
func TestTableTreatsDashAsNoData(t *testing.T) {
	rows := [][]string{{"alice", "3"}, {"bob", "–"}}
	sep := strings.Split(Table("t", []string{"Member", "Size"}, rows), "\n")[3]
	if want := "|---|---:|"; sep != want {
		t.Errorf("separator = %q, want %q", sep, want)
	}
}

func TestTeamStatsEscapesLoginAndRendersSentinels(t *testing.T) {
	rows := []metrics.Row{{Login: "a|b", PRsOpened: 2, SizeP50: -1}}
	out := TeamStats("platform", metrics.Window{Label: "Last 7 days"}, rows)

	if !strings.Contains(out, `a\|b`) {
		t.Errorf("login pipe not escaped:\n%s", out)
	}
	// Cycle p50, first-review p50 and size p50 all have no data here.
	if got := strings.Count(out, "–"); got != 3 {
		t.Errorf("no-data dashes = %d, want 3:\n%s", got, out)
	}
}

// The team table exports one cell per header, including the dismissed-review
// column, so a mismatch shifts every number after it into the wrong column.
func TestTeamStatsRowMatchesHeader(t *testing.T) {
	out := TeamStats("platform", metrics.Window{Label: "Last 7 days"}, []metrics.Row{{
		Login: "alice", PRsOpened: 2, PRsMerged: 1, ReviewsGiven: 4,
		Approved: 2, Commented: 1, Dismissed: 1, SizeP50: 40,
	}})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	header, row := lines[2], lines[4]

	if !strings.Contains(header, "| Dismissed |") {
		t.Errorf("header is missing the dismissed column:\n%s", header)
	}
	if got, want := unescapedPipes(row), unescapedPipes(header); got != want {
		t.Errorf("row has %d cells, header has %d:\n%s\n%s", got, want, header, row)
	}
	if got := unescapedPipes(lines[3]); got != unescapedPipes(header) {
		t.Errorf("alignment row has %d cells, header has %d", got, unescapedPipes(header))
	}
}

func TestPersonEscapesRepo(t *testing.T) {
	out := Person("alice", metrics.Window{Label: "Last 30 days"},
		metrics.Row{PRsOpened: 1},
		[]metrics.RepoBreakdown{{Repo: "acme/a|b", PRsOpened: 1}})
	if !strings.Contains(out, `acme/a\|b`) {
		t.Errorf("repo pipe not escaped:\n%s", out)
	}
}

func TestMdMover(t *testing.T) {
	riser := mdMover(metrics.Mover{
		Login: "alice", Metric: metrics.MetricReviews, Prior: 0, Recent: 8, IsNew: true, Streak: 4,
	})
	if !strings.HasPrefix(riser, "▲ ") {
		t.Errorf("riser should lead with the up arrow: %s", riser)
	}
	if !strings.Contains(riser, "(new)") {
		t.Errorf("a mover from zero should read as new: %s", riser)
	}
	if !strings.Contains(riser, ", up 4w running") {
		t.Errorf("missing streak badge: %s", riser)
	}

	faller := mdMover(metrics.Mover{
		Login: "bob", Metric: metrics.MetricOpened, Prior: 10, Recent: 4, Pct: -60, Streak: -3,
	})
	if !strings.HasPrefix(faller, "▼ ") {
		t.Errorf("faller should lead with the down arrow: %s", faller)
	}
	if !strings.Contains(faller, "(-60%)") {
		t.Errorf("missing percent change: %s", faller)
	}
	if !strings.Contains(faller, ", down 3w running") {
		t.Errorf("missing streak badge: %s", faller)
	}
}
func TestMdSize(t *testing.T) {
	if got := mdSize(-1); got != "–" {
		t.Errorf("mdSize(-1) = %q, want the no-data dash", got)
	}
	if got := mdSize(120); got != "120" {
		t.Errorf("mdSize(120) = %q, want 120", got)
	}
}

// Trends is the one export with no test, and the only place the weekly
// series and the movers list are rendered for the clipboard. Pin the whole
// document: header text, column order, the duration sentinel, and the
// movers section, all of which have to match what the trends page shows.
func TestTrendsGolden(t *testing.T) {
	week := func(n int) time.Time { return time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, 7*n) }
	d := metrics.TrendData{
		Weeks: []time.Time{week(0), week(1), week(2)},
		Team: metrics.TeamTrend{
			Opened:   []int{3, 5, 4},
			Merged:   []int{2, 4, 3},
			Reviews:  []int{7, 9, 8},
			Comments: []int{1, 0, 2},
			// The last week has no samples: 0 is the sentinel, not a duration.
			Cycle: []time.Duration{2 * time.Hour, 3*time.Hour + 30*time.Minute, 0},
			TTFR:  []time.Duration{30 * time.Minute, 45 * time.Minute, 0},
		},
	}
	risers := []metrics.Mover{{
		Login: "alice", Metric: metrics.MetricReviews, Prior: 0, Recent: 8, IsNew: true, Streak: 4,
	}}
	fallers := []metrics.Mover{{
		Login: "bob", Metric: metrics.MetricOpened, Prior: 10, Recent: 4, Pct: -60, Streak: -3,
	}}

	want := `## acme — Trends (last 3 weeks)

| Week | Opened | Merged | Reviews | Comments | Cycle p50 | TTFR p50 |
|---|---:|---:|---:|---:|---:|---:|
| 2026-06-01 | 3 | 2 | 7 | 1 | 2.0h | 30m |
| 2026-06-08 | 5 | 4 | 9 | 0 | 3.5h | 45m |
| 2026-06-15 | 4 | 3 | 8 | 2 | – | – |

### Movers

- ▲ alice — reviews 0 → 8 (new), up 4w running
- ▼ bob — PRs opened 10 → 4 (-60%), down 3w running
`
	if got := Trends("acme", d, risers, fallers); got != want {
		t.Errorf("Trends export drifted:\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

// A quiet week produces no movers at all. The section header is part of the
// list, so printing it over nothing leaves a heading with no content.
func TestTrendsOmitsAnEmptyMoversSection(t *testing.T) {
	d := metrics.TrendData{
		Weeks: []time.Time{time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		Team: metrics.TeamTrend{
			Opened: []int{0}, Merged: []int{0}, Reviews: []int{0}, Comments: []int{0},
			Cycle: []time.Duration{0}, TTFR: []time.Duration{0},
		},
	}
	got := Trends("acme", d, nil, nil)
	if strings.Contains(got, "Movers") {
		t.Errorf("empty movers list still printed its heading:\n%s", got)
	}
	if !strings.HasSuffix(got, "| 2026-06-01 | 0 | 0 | 0 | 0 | – | – |\n") {
		t.Errorf("document should end with the last data row:\n%s", got)
	}
}

// Every data row has to carry exactly as many cells as the header, or the
// table renders ragged wherever it is pasted.
func TestTrendsRowMatchesHeader(t *testing.T) {
	d := metrics.TrendData{
		Weeks: []time.Time{time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)},
		Team: metrics.TeamTrend{
			Opened: []int{1}, Merged: []int{1}, Reviews: []int{1}, Comments: []int{1},
			Cycle: []time.Duration{time.Hour}, TTFR: []time.Duration{time.Minute},
		},
	}
	lines := strings.Split(strings.TrimSpace(Trends("acme", d, nil, nil)), "\n")
	header, row := lines[2], lines[4]
	if got, want := unescapedPipes(row), unescapedPipes(header); got != want {
		t.Errorf("row has %d separators, header has %d:\n%s\n%s", got, want, header, row)
	}
}

// A team name arrives from config and rides the clipboard into whatever
// renders it, so control characters must not survive the heading.
func TestTrendsSanitizesTheTeamName(t *testing.T) {
	d := metrics.TrendData{Weeks: nil, Team: metrics.TeamTrend{}}
	got := Trends("evil \x1b]0;pwned\x07\nteam", d, nil, nil)
	for _, bad := range []string{"\x1b", "\a", "pwned", "\n\n\n"} {
		if strings.Contains(got, bad) {
			t.Errorf("export contains %q:\n%q", bad, got)
		}
	}
	if !strings.HasPrefix(got, "## evil  team — Trends (last 0 weeks)") {
		t.Errorf("printable text mangled:\n%q", got)
	}
}

// Card exports build their rows themselves, so a row shorter than the
// headers is a bug the table renderer still must not panic on while it
// decides column alignment.
func TestTableToleratesShortRows(t *testing.T) {
	out := Table("Ragged", []string{"A", "B", "C"}, [][]string{{"1", "2"}, {"3", "4", "5"}})
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if got, want := lines[3], "|---:|---:|---:|"; got != want {
		t.Errorf("alignment row = %q, want %q — a missing cell is not a non-number", got, want)
	}
}
