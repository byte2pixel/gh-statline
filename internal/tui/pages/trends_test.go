package pages

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// bigTrendData builds n members with twelve weeks of varied counts so the
// per-member fullscreen rows overflow any realistic screen. Every member
// opens the same total, so the busiest-first rows fall back to login
// order and the last login is the last row.
func bigTrendData(n int) metrics.TrendData {
	const weeks = metrics.TrendWeeks
	start := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	zero := func() []int { return make([]int, weeks) }
	d := metrics.TrendData{
		Weeks: make([]time.Time, weeks),
		Team: metrics.TeamTrend{Opened: zero(), Merged: zero(), Reviews: zero(), Comments: zero(),
			Cycle: make([]time.Duration, weeks), TTFR: make([]time.Duration, weeks)},
	}
	for w := range d.Weeks {
		d.Weeks[w] = start.AddDate(0, 0, 7*w)
		d.Team.Cycle[w] = time.Duration(w+1) * time.Hour
		d.Team.TTFR[w] = time.Duration(w+1) * 30 * time.Minute
	}
	for m := 0; m < n; m++ {
		mt := metrics.MemberTrend{Login: fmt.Sprintf("m%02d", m),
			Opened: zero(), Merged: zero(), Reviews: zero(), Comments: zero()}
		for w := 0; w < weeks; w++ {
			mt.Opened[w] = 1 + (m+w)%3
			mt.Merged[w] = (m * w) % 2
			mt.Reviews[w] = (m + 2*w) % 4
			mt.Comments[w] = (3*m + w) % 5
			d.Team.Opened[w] += mt.Opened[w]
			d.Team.Merged[w] += mt.Merged[w]
			d.Team.Reviews[w] += mt.Reviews[w]
			d.Team.Comments[w] += mt.Comments[w]
		}
		d.Members = append(d.Members, mt)
	}
	return d
}

func newTestTrends(w, h int, d metrics.TrendData) *Trends {
	th := theme.New(true)
	tr := NewTrends(&th, keys.Default())
	tr.SetSize(w, h)
	tr.SetData(d)
	return tr
}

// The grid must never exceed the size it was given, whatever the terminal.
func TestTrendsGridFitsExactly(t *testing.T) {
	titles := []string{"PRs opened", "Merged", "Reviews", "Comments", "Cycle p50", "TTFR p50", "Movers"}
	for _, tc := range []struct{ w, h int }{
		{80, 21}, {100, 25}, {120, 35}, {80, 13}, {60, 20}, {80, 10},
	} {
		view := newTestTrends(tc.w, tc.h, bigTrendData(40)).View()
		if got := lipgloss.Height(view); got > tc.h {
			t.Errorf("(%d×%d): view is %d lines tall, budget %d", tc.w, tc.h, got, tc.h)
		}
		if got := lipgloss.Width(view); got > tc.w {
			t.Errorf("(%d×%d): view is %d cols wide, budget %d", tc.w, tc.h, got, tc.w)
		}
		switch {
		case tc.h >= 13 && tc.w >= 80:
			for _, title := range titles {
				if !strings.Contains(view, title) {
					t.Errorf("(%d×%d): card %q missing from grid", tc.w, tc.h, title)
				}
			}
		case tc.h < 13:
			if !strings.Contains(view, "too short") {
				t.Errorf("(%d×%d): expected too-short message", tc.w, tc.h)
			}
		}
	}
}

// Every member must be reachable in a fullscreen count card.
func TestTrendsFullscreenScrollsToLastMember(t *testing.T) {
	tr := newTestTrends(100, 28, bigTrendData(40))
	tr.grid.openFull("opened")

	view := plain(tr.View())
	if got := lipgloss.Height(view); got > 28 {
		t.Errorf("fullscreen view is %d lines, budget 28", got)
	}
	if strings.Contains(view, "m39") {
		t.Fatal("last member visible without scrolling: fixture not overflowing?")
	}
	if !strings.Contains(view, "per member") {
		t.Errorf("fullscreen count card lost its member breakdown:\n%s", view)
	}
	if !tr.HandleKey(press("G")) {
		t.Fatal("G not handled in fullscreen")
	}
	if view = plain(tr.View()); !strings.Contains(view, "m39") {
		t.Error("G (bottom) did not reveal the last member")
	}
	tr.HandleKey(press("g"))
	if tr.grid.vp.YOffset() != 0 {
		t.Error("g did not return to the top")
	}
}

// The movers card takes the whole bottom row; focus moves through it and
// back up to the middle card.
func TestTrendsNavigationMoversRow(t *testing.T) {
	tr := newTestTrends(100, 28, bigTrendData(5))
	for _, step := range []struct {
		k    string
		want int
	}{{"j", 3}, {"j", 6}, {"h", 6}, {"l", 6}, {"k", 4}, {"l", 5}, {"j", 6}} {
		tr.HandleKey(press(step.k))
		if tr.grid.focus != step.want {
			t.Errorf("after %q: focus = %d, want %d", step.k, tr.grid.focus, step.want)
		}
	}
	tr.HandleKey(press("f"))
	if !tr.Fullscreen() || tr.grid.full != "movers" {
		t.Errorf("f on the movers card opened %q", tr.grid.full)
	}
}

// Export follows the view: the fullscreen card's table, or the weekly
// summary plus movers from the grid.
func TestTrendsExportFollowsView(t *testing.T) {
	tr := newTestTrends(100, 28, bigTrendData(3))
	w := metrics.LastDays(30, time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC))

	if md := tr.Export("acme", w); !strings.Contains(md, "## acme — Trends (last 12 weeks)") ||
		!strings.Contains(md, "| Week |") {
		t.Errorf("grid export should be the weekly summary:\n%s", md)
	}
	tr.grid.openFull("opened")
	md := tr.Export("acme", w)
	if !strings.Contains(md, "## PRs opened — weekly trend") {
		t.Errorf("fullscreen export missing the card title:\n%s", md)
	}
	if strings.Contains(md, "## acme") {
		t.Errorf("fullscreen export should be the card table, not the summary:\n%s", md)
	}
}

// Coverage can vanish mid-session (a repo over-claimed its backfill). A
// fullscreen card must survive an empty series and come back with data.
func TestTrendsSurvivesEmptySeriesFullscreen(t *testing.T) {
	tr := newTestTrends(100, 28, bigTrendData(5))
	tr.grid.openFull("opened")

	tr.SetData(metrics.TrendData{})
	tr.SetSize(90, 24)
	if v := plain(tr.View()); !strings.Contains(v, "unlock") {
		t.Errorf("empty series should show the unlock hint:\n%s", v)
	}
	if !tr.Fullscreen() {
		t.Error("an empty series must not silently close the card")
	}
	tr.SetData(bigTrendData(5))
	if v := plain(tr.View()); !strings.Contains(v, "PRs opened") || !strings.Contains(v, "per member") {
		t.Errorf("fullscreen did not resume once data returned:\n%s", v)
	}
	tr.Reset()
	if tr.Fullscreen() {
		t.Error("Reset must return to the grid for the next team")
	}
}

// Each mover's sparkline comes from the series its typed metric names, so
// the fullscreen movers card shows one for every metric.
func TestMoverSparklinesFollowTypedMetric(t *testing.T) {
	series := func(vals ...int) []int { return vals }
	zero := series(0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0)
	d := bigTrendData(0)
	d.Members = []metrics.MemberTrend{
		{Login: "ramp", Opened: zero, Merged: zero, Comments: zero,
			Reviews: series(0, 0, 0, 0, 0, 0, 0, 0, 3, 3, 3, 3)}, // new riser
		{Login: "drop", Merged: zero, Reviews: zero, Comments: zero,
			Opened: series(5, 5, 5, 5, 5, 5, 5, 5, 1, 0, 0, 0)}, // -95% faller
		{Login: "chat", Opened: zero, Merged: zero, Reviews: zero,
			Comments: series(2, 2, 2, 2, 2, 2, 2, 2, 8, 8, 8, 8)}, // +300% riser
		{Login: "ship", Opened: zero, Reviews: zero, Comments: zero,
			Merged: series(1, 1, 1, 1, 4, 4, 4, 4, 2, 2, 2, 2)}, // -50% faller
	}
	risers, fallers := metrics.Movers(d.Members, 0)
	movers := slices.Concat(risers, fallers)
	if len(movers) != 4 {
		t.Fatalf("fixture yields %d movers, want 4: %+v", len(movers), movers)
	}

	tr := newTestTrends(120, 40, d)
	ctx := tr.ctx()
	for _, m := range movers {
		i := slices.IndexFunc(d.Members, func(mt metrics.MemberTrend) bool { return mt.Login == m.Login })
		want := toFloats(m.Metric.Of(d.Members[i]))
		if got := memberMetricSeries(ctx, m); !slices.Equal(got, want) {
			t.Errorf("%s/%s: series = %v, want %v", m.Login, m.Metric, got, want)
		}
	}

	tr.grid.openFull("movers")
	spark := regexp.MustCompile("[·▁▂▃▄▅▆▇█]{12}")
	for _, line := range strings.Split(plain(tr.View()), "\n") {
		for _, m := range movers {
			if strings.Contains(line, m.Login) && !spark.MatchString(line) {
				t.Errorf("%s (%s) has no sparkline: %q", m.Login, m.Metric, line)
			}
		}
	}
}
