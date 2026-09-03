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
	riser := mdMover("▲", metrics.Mover{
		Login: "alice", Metric: "reviews", Prior: 0, Recent: 8, IsNew: true, Streak: 4,
	})
	if !strings.Contains(riser, "(new)") {
		t.Errorf("a mover from zero should read as new: %s", riser)
	}
	if !strings.Contains(riser, "up 4 weeks running") {
		t.Errorf("missing streak badge: %s", riser)
	}

	faller := mdMover("▼", metrics.Mover{
		Login: "bob", Metric: "PRs opened", Prior: 10, Recent: 4, Pct: -60, Streak: -3,
	})
	if !strings.Contains(faller, "(-60%)") {
		t.Errorf("missing percent change: %s", faller)
	}
	if !strings.Contains(faller, "down 3 weeks running") {
		t.Errorf("missing streak badge: %s", faller)
	}
}

func TestMdDurAndMdSize(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "–"},
		{45 * time.Second, "45s"},
		{30 * time.Minute, "30m"},
		{5 * time.Hour, "5.0h"},
		{48 * time.Hour, "2.0d"},
	} {
		if got := mdDur(c.d); got != c.want {
			t.Errorf("mdDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
	if got := mdSize(-1); got != "–" {
		t.Errorf("mdSize(-1) = %q, want the no-data dash", got)
	}
	if got := mdSize(120); got != "120" {
		t.Errorf("mdSize(120) = %q, want 120", got)
	}
}
