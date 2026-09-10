package pages

import (
	"fmt"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// The person page is a pure render of what the app hands it, so every
// number here is pinned by hand and asserted by eye, like the card exports.
var personRow = metrics.Row{
	Login: "alice", PRsOpened: 4, PRsMerged: 3,
	ReviewsGiven: 6, Approved: 3, Commented: 2, ChangesReq: 1,
	CommentsGiven: 7, CommentsRecv: 2, SizeP50: 40,
}

func personRepos() []metrics.RepoBreakdown {
	return []metrics.RepoBreakdown{
		{Repo: "acme/api", PRsOpened: 3, PRsMerged: 2, ReviewsGiven: 4, CommentsGiven: 5},
		{Repo: "acme/web", PRsOpened: 1, PRsMerged: 1, ReviewsGiven: 2, CommentsGiven: 2},
	}
}

// activity is n days of a small repeating pulse.
func activity(n int) []float64 {
	out := make([]float64, n)
	for i := range out {
		out[i] = float64(i % 4)
	}
	return out
}

func newTestPerson(w, h, days int) *Person {
	th := theme.New(true)
	p := NewPerson(&th)
	p.SetSize(w, h)
	p.SetData("alice", personRow, personRepos(), activity(days))
	return p
}

// The page renders nothing until it has both a size and a member, in
// either order. The app sizes every page up front and loads the member on
// drill-down, but a resize can land in between.
func TestPersonViewEmptyUntilSizedAndLoaded(t *testing.T) {
	th := theme.New(true)
	p := NewPerson(&th)
	if p.View() != "" {
		t.Fatal("a bare page rendered something")
	}
	p.SetData("alice", personRow, personRepos(), activity(30))
	if p.View() != "" {
		t.Error("data without a size rendered")
	}
	p.SetSize(100, 20)
	if !strings.Contains(plain(p.View()), "alice") {
		t.Error("sized and loaded, the page did not render the member")
	}

	q := NewPerson(&th)
	q.SetSize(100, 20)
	if q.View() != "" {
		t.Error("a size without a member rendered")
	}
}

// The sparkline takes the width less a margin, but no more than two cells
// per day so short windows get wider bars, and never under ten cells.
func TestPersonSparklineWidth(t *testing.T) {
	for _, tc := range []struct {
		w, days, want int
	}{
		{100, 30, 60}, // capped at two cells per day
		{100, 60, 96}, // width - 4
		{12, 30, 10},  // floor, from a narrow terminal
		{100, 3, 10},  // floor, from a short window
	} {
		p := newTestPerson(tc.w, 20, tc.days)
		if got := p.spark.Width(); got != tc.want {
			t.Errorf("width %d, %d days: sparkline width = %d, want %d", tc.w, tc.days, got, tc.want)
		}
		if got := lipgloss.Height(p.spark.View()); got != personSparkH {
			t.Errorf("width %d, %d days: sparkline is %d rows, want %d", tc.w, tc.days, got, personSparkH)
		}
	}
}

// The frame is exactly the height it was given: three header lines, the
// sparkline, and a table that takes the rest. Under ten rows the table's
// three-row floor wins and the page is ten rows, the documented minimum.
func TestPersonFrameFitsHeight(t *testing.T) {
	for _, tc := range []struct{ h, want int }{
		{40, 40}, {20, 20}, {12, 12}, {10, 10},
		{9, 10}, {3, 10},
	} {
		view := newTestPerson(100, tc.h, 30).View()
		if got := lipgloss.Height(view); got != tc.want {
			t.Errorf("height %d: frame is %d rows, want %d", tc.h, got, tc.want)
		}
		if got := lipgloss.Width(view); got > 100 {
			t.Errorf("height %d: frame is %d cols wide, budget 100", tc.h, got)
		}
	}
}

// The stat line, the sparkline caption, and the repo table print the
// fixture's numbers in the fixture's order.
func TestPersonRendersStatsAndRepoTable(t *testing.T) {
	view := plain(newTestPerson(100, 20, 30).View())
	for _, want := range []string{
		"alice",
		"PRs 4 opened · 3 merged   reviews 6 (3✓ 2💬 1±)   comments 7 given / 2 received",
		"activity (PRs + reviews per day)",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}

	lines := strings.Split(view, "\n")
	row := func(repo string) []string {
		t.Helper()
		for _, l := range lines {
			if strings.Contains(l, repo) {
				return strings.Fields(l)
			}
		}
		t.Fatalf("no row for %s:\n%s", repo, view)
		return nil
	}
	header := row("Repo")
	if want := []string{"Repo", "PRs", "Merged", "Reviews", "Comments"}; !slices.Equal(header, want) {
		t.Errorf("table header = %q, want %q", header, want)
	}
	if got, want := row("acme/api"), []string{"acme/api", "3", "2", "4", "5"}; !slices.Equal(got, want) {
		t.Errorf("acme/api row = %q, want %q", got, want)
	}
	if got, want := row("acme/web"), []string{"acme/web", "1", "1", "2", "2"}; !slices.Equal(got, want) {
		t.Errorf("acme/web row = %q, want %q", got, want)
	}
}

// The export is the member's own document: the login heads it, not the
// team, and the table is the page's repo breakdown.
func TestPersonExportIsTheMemberDoc(t *testing.T) {
	md := newTestPerson(100, 20, 30).Export("acme", exportWindow)
	assertTable(t, md, "alice — Last 30 days",
		[]string{"Repo", "PRs", "Merged", "Reviews", "Comments"},
		[][]string{{"acme/api", "3", "2", "4", "5"}, {"acme/web", "1", "1", "2", "2"}})
	if want := "PRs opened 4 · merged 3 · reviews given 6 (3 approved, 2 commented, 1 changes requested) · comments given 7 / received 2"; !strings.Contains(md, want) {
		t.Errorf("export missing the stat line %q:\n%s", want, md)
	}
	if strings.Contains(md, "## acme") {
		t.Errorf("the team name is not this export's heading:\n%s", md)
	}
}

// The wheel and the table's own keys move the repo cursor; the page claims
// no keys ahead of the global keymap and marks no click zones.
func TestPersonScrollAndKeysMoveTheRepoCursor(t *testing.T) {
	repos := make([]metrics.RepoBreakdown, 12)
	for i := range repos {
		repos[i] = metrics.RepoBreakdown{Repo: fmt.Sprintf("acme/r%02d", i), PRsOpened: 12 - i}
	}
	th := theme.New(true)
	p := NewPerson(&th)
	p.SetSize(100, 12) // a 5-row table: four repos visible at a time
	p.SetData("alice", personRow, repos, activity(30))
	if view := plain(p.View()); !strings.Contains(view, "acme/r00") || strings.Contains(view, "acme/r11") {
		t.Fatalf("expected the first rows and not the last:\n%s", view)
	}

	at := func(step string, want int) {
		t.Helper()
		if got := p.tbl.Cursor(); got != want {
			t.Errorf("after %s: cursor = %d, want %d", step, got, want)
		}
	}
	at("load", 0)
	p.Scroll(3)
	at("wheel down 3", 3)
	p.Scroll(-2)
	at("wheel up 2", 1)
	p.Update(press("j"))
	at("j", 2)
	p.Update(press("k"))
	at("k", 1)
	p.Scroll(100)
	at("wheel past the end", 11)
	if view := plain(p.View()); !strings.Contains(view, "acme/r11") {
		t.Errorf("the table did not follow the cursor to the last repo:\n%s", view)
	}
	p.Scroll(-100)
	at("wheel past the top", 0)

	if p.HandleKey(press("j")) || p.HandleKey(keyEnter) {
		t.Error("the person page claimed a key ahead of the global keymap")
	}
	if cmd := p.HandleClick(tea.MouseClickMsg{X: 1, Y: 1, Button: tea.MouseLeft}); cmd != nil {
		t.Error("a click on the person page produced a command")
	}
}
