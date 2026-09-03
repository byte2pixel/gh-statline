package pages

import (
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// ansiRE strips color escapes so substring checks can't accidentally match
// across a sequence boundary (e.g. "…;239m" + "39" reading as "m39").
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// bigChartData fills every dataset with n members so the per-person cards
// overflow any realistic screen.
func bigChartData(n int) metrics.Dashboard {
	var d metrics.Dashboard
	day := time.Now().AddDate(0, 0, -14)
	for i := 0; i < 14; i++ {
		d.Buckets = append(d.Buckets, metrics.Bucket{Day: day.AddDate(0, 0, i), Opened: i % 5, Merged: i % 3})
	}
	for i := 0; i < 8; i++ {
		d.Trend = append(d.Trend, metrics.TrendPoint{WeekStart: day.AddDate(0, 0, i*7), Median: time.Duration(i+1) * time.Hour, Merged: i})
	}
	d.TTFR = metrics.Dist{Labels: []string{"<1h", "<4h", "<1d", "<3d", "3d+"}, Counts: []int{3, 4, 5, 2, 1}}
	d.Sizes = metrics.Dist{Labels: []string{"XS", "S", "M", "L", "XL"}, Counts: []int{5, 8, 6, 2, 1}}
	d.Aging = metrics.Aging{
		Buckets: metrics.Dist{Labels: []string{"<1d", "<3d", "<1w", "<2w", "2w+"}, Counts: []int{1, 2, 1, 1, 3}},
		Total:   8,
		Stalest: []metrics.StalePR{{Repo: "acme/api", Number: 12, Title: "old one", Author: "m00", AgeDays: 95}},
	}
	d.Punch.Counts[0][10] = 4
	d.Punch.Counts[5][14] = 2
	d.Punch.Max, d.Punch.Total = 4, 6

	logins := make([]string, n)
	for i := range logins {
		logins[i] = fmt.Sprintf("m%02d", i)
		d.Rows = append(d.Rows, metrics.Row{
			Login: logins[i], PRsOpened: 2, PRsMerged: 1,
			Approved: 3, Commented: 2, ChangesReq: 1,
			// Distinct, descending: m00 sorts first, m(n-1) last.
			ReviewsGiven:  5 + n - i,
			CommentsGiven: 4, CommentsRecv: 3, SizeP50: 40,
		})
	}
	d.Matrix.Logins = logins
	d.Matrix.Authors = logins
	d.Matrix.Counts = make([][]int, n)
	for i := range d.Matrix.Counts {
		d.Matrix.Counts[i] = make([]int, n)
		for j := range d.Matrix.Counts[i] {
			if i != j {
				d.Matrix.Counts[i][j] = (i + j) % 4
			}
		}
	}
	d.Matrix.Max = 3
	return d
}

// TestGridFitsExactly: the 3×3 grid must never exceed the size it was given —
// the overflow was what pushed the help footer off screen (gh symptom).
func TestGridFitsExactly(t *testing.T) {
	th := theme.New(true)
	cases := []struct{ w, h int }{
		{80, 21}, {100, 25}, {120, 35}, {80, 15}, {60, 20}, {80, 10},
	}
	for _, tc := range cases {
		c := NewCharts(&th)
		c.SetSize(tc.w, tc.h)
		_ = c.SetData(bigChartData(40))
		view := c.View()

		if got := lipgloss.Height(view); got > tc.h {
			t.Errorf("(%d×%d): view is %d lines tall, budget %d", tc.w, tc.h, got, tc.h)
		}
		if got := lipgloss.Width(view); got > tc.w {
			t.Errorf("(%d×%d): view is %d cols wide, budget %d", tc.w, tc.h, got, tc.w)
		}
		switch {
		case tc.h >= 18 && tc.w >= 80: // full grid: every card title visible
			for _, cd := range c.cards {
				if !strings.Contains(view, cd.title()) {
					t.Errorf("(%d×%d): card %q missing from grid", tc.w, tc.h, cd.title())
				}
			}
		case tc.h < 15: // too short: explanatory message, not a broken grid
			if !strings.Contains(view, "too short") {
				t.Errorf("(%d×%d): expected too-short message", tc.w, tc.h)
			}
		}
	}
}

// TestFullscreenScrollsToLastMember is the regression for gh issue #2: with a
// 40-person team every member must be reachable in the fullscreen view.
func TestFullscreenScrollsToLastMember(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th)
	c.SetSize(100, 28)
	_ = c.SetData(bigChartData(40))
	c.openFull("outcomes")

	view := plain(c.View())
	if got := lipgloss.Height(view); got > 28 {
		t.Errorf("fullscreen view is %d lines, budget 28", got)
	}
	if strings.Contains(view, "m39") {
		t.Fatal("last member visible without scrolling — fixture not overflowing?")
	}
	if m, _ := regexp.MatchString(`of 4\d`, view); !m {
		t.Error("scroll indicator missing or wrong")
	}

	if !c.HandleKey("G") {
		t.Fatal("G not handled in fullscreen")
	}
	if view = plain(c.View()); !strings.Contains(view, "m39") {
		t.Error("G (bottom) did not reveal the last member")
	}

	c.HandleKey("g")
	if c.vp.YOffset() != 0 {
		t.Error("g did not return to the top")
	}
}

func TestFullscreenWheelScroll(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th)
	c.SetSize(100, 28)
	_ = c.SetData(bigChartData(40))

	c.Scroll(3) // grid mode: wheel must be a no-op
	c.openFull("comments")
	before := c.vp.YOffset()
	c.Scroll(3)
	if c.vp.YOffset() == before {
		t.Error("wheel scroll did not move the fullscreen viewport")
	}
	c.Scroll(-3)
	if c.vp.YOffset() != before {
		t.Error("wheel scroll up did not return")
	}
}

// TestMatrixPinnedLabels: the fullscreen matrix keeps the author header row
// and the reviewer name column on screen while j/k/h/l move the cell window.
func TestMatrixPinnedLabels(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th)
	c.SetSize(80, 24)
	_ = c.SetData(bigChartData(40))
	c.openFull("matrix")

	lineAt := func(i int) string { return strings.Split(plain(c.View()), "\n")[i] }
	if !strings.Contains(lineAt(1), "rev↓") || !strings.Contains(lineAt(1), "m00") {
		t.Fatalf("header row missing: %q", lineAt(1))
	}
	if !strings.HasPrefix(lineAt(2), "m00") {
		t.Fatalf("first reviewer row should be m00: %q", lineAt(2))
	}

	// Pan right and scroll down: both labels must stay pinned.
	if !c.HandleKey("l") || !c.HandleKey("j") {
		t.Fatal("l/j not handled in matrix fullscreen")
	}
	if !strings.Contains(lineAt(1), "rev↓") {
		t.Error("header row scrolled away after panning")
	}
	if strings.Contains(lineAt(1), "m00") || !strings.Contains(lineAt(1), "m01") {
		t.Errorf("header should start at m01 after l: %q", lineAt(1))
	}
	if !strings.HasPrefix(lineAt(2), "m01") {
		t.Errorf("first reviewer row should be m01 after j: %q", lineAt(2))
	}
	view := plain(c.View())
	if !strings.Contains(view, "rows 2–22 of 40") || !strings.Contains(view, "cols 2–19 of 40") {
		t.Errorf("indicator wrong: %q", view[strings.LastIndex(view, "rows"):])
	}

	// G jumps to the last reviewer; names still pinned left.
	c.HandleKey("G")
	if !strings.Contains(plain(c.View()), "m39") {
		t.Error("G did not reveal the last reviewer")
	}
	c.HandleKey("g")
	if c.mRow != 0 || c.mCol != 0 {
		t.Error("g did not reset the matrix window")
	}
}

// TestTileDeltas: stat tiles show window-over-window changes, with latency
// tiles colored inversely, and plain dashes when coverage is insufficient.
func TestTileDeltas(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th)
	c.SetSize(160, 40)
	d := bigChartData(5) // sums: opened 10, merged 5, reviews 40, comments 20
	d.Tiles = metrics.TileTrend{
		HasPrev:    true,
		PrevOpened: 5, PrevMerged: 10, PrevReviews: 30, PrevComments: 20,
		Cycle: 20 * time.Hour, PrevCycle: 26 * time.Hour,
		TTFR: 5 * time.Hour, PrevTTFR: 3 * time.Hour,
	}
	_ = c.SetData(d)
	row := plain(c.tilesRow())
	for _, want := range []string{
		"▲100%",              // opened 10 vs 5
		"▼50%",               // merged 5 vs 10
		"▲33%",               // reviews 40 vs 30
		"±0",                 // comments 20 vs 20
		"Cycle p50", "▼6.0h", // 20h vs 26h: faster
		"TTFR p50", "▲2.0h", // 5h vs 3h: slower
	} {
		if !strings.Contains(row, want) {
			t.Errorf("tiles row missing %q:\n%s", want, row)
		}
	}

	d.Tiles = metrics.TileTrend{} // no coverage: comparisons must not render
	_ = c.SetData(d)
	row = plain(c.tilesRow())
	if strings.Contains(row, "▲") || strings.Contains(row, "▼") {
		t.Errorf("tiles must show dashes without coverage:\n%s", row)
	}
	if !strings.Contains(row, "–") {
		t.Errorf("expected dash placeholders:\n%s", row)
	}
}

// TestPunchCardGranularitySteps: the punch card steps hourly → 2-hour →
// 4-hour buckets as width shrinks instead of jumping straight to 4-hour.
func TestPunchCardGranularitySteps(t *testing.T) {
	th := theme.New(true)
	d := bigChartData(5)
	ctx := renderCtx{d: &d, th: &th, grow: 1}
	header := func(w int) string {
		return plain(strings.Split(punchCard{}.body(ctx, w, 20, true), "\n")[0])
	}
	if h := header(110); !strings.Contains(h, "23") {
		t.Errorf("110 cols should use hourly buckets, header %q", h)
	}
	if h := header(60); !strings.Contains(h, "22") || strings.Contains(h, "23") {
		t.Errorf("60 cols should use 2-hour buckets, header %q", h)
	}
	if h := header(40); !strings.Contains(h, "20") || strings.Contains(h, "22") {
		t.Errorf("40 cols should use 4-hour buckets, header %q", h)
	}
}

// TestMatrixThreeDigitCells is the regression for gh issue #23: 3-digit
// review counts must render whole, like the punch card, not clip to " 1…".
func TestMatrixThreeDigitCells(t *testing.T) {
	th := theme.New(true)
	d := bigChartData(4)
	d.Matrix.Counts[0][1] = 123
	d.Matrix.Counts[1][0] = 999
	d.Matrix.Max = 999
	ctx := renderCtx{d: &d, th: &th, grow: 1}

	grid := plain(matrixCard{}.body(ctx, 80, 20, false))
	full := plain(strings.Join(matrixCard{}.pinnedGrid(ctx, 0, 0, 10, 10), "\n"))
	for name, view := range map[string]string{"grid": grid, "fullscreen": full} {
		for _, want := range []string{"123", "999"} {
			if !strings.Contains(view, want) {
				t.Errorf("%s: count %s missing or clipped:\n%s", name, want, view)
			}
		}
		if strings.Contains(view, "…") {
			t.Errorf("%s: cell truncated:\n%s", name, view)
		}
	}
}

// TestAgingCardSanitizesTitles: PR titles are written by whoever opens a PR
// against a watched repo. The aging card must never pass their escape
// sequences to the terminal, and must truncate by display cells (gh #43).
func TestAgingCardSanitizesTitles(t *testing.T) {
	th := theme.New(true)
	d := bigChartData(3)
	d.Aging.Stalest = []metrics.StalePR{{
		Repo: "acme/api", Number: 7, Author: "mal", AgeDays: 40,
		Title: "evil \x1b]0;pwned\x07 \x1b[31mred 你好又 end",
	}}
	ctx := renderCtx{d: &d, th: &th, grow: 1}

	for _, w := range []int{60, 24, 18} { // wide, cutting mid-title, cutting near 你好
		body := agingCard{}.body(ctx, w, 12, true)
		if strings.Contains(body, "\x1b]") || strings.Contains(body, "\a") {
			t.Errorf("w=%d: raw OSC/BEL reached the render:\n%q", w, body)
		}
		if strings.Contains(body, "pwned") {
			t.Errorf("w=%d: OSC payload survived:\n%q", w, body)
		}
		for _, ln := range strings.Split(plain(body), "\n") {
			if !utf8.ValidString(ln) {
				t.Errorf("w=%d: truncation split UTF-8: %q", w, ln)
			}
			if got := lipgloss.Width(ln); got > w {
				t.Errorf("w=%d: line is %d cells: %q", w, got, ln)
			}
		}
	}

	// The export hands the raw title to export.Table, whose cell() sanitizes;
	// the card itself must not pre-mangle cache data.
	_, rows := agingCard{}.export(ctx)
	if !strings.Contains(rows[0][2], "\x1b") {
		t.Error("export should carry the original title; sanitizing is the table renderer's job")
	}
}

// TestGridNavigationThreeColumns: arrows move within a 3-wide grid.
func TestGridNavigationThreeColumns(t *testing.T) {
	th := theme.New(true)
	c := NewCharts(&th)
	c.SetSize(100, 28)
	_ = c.SetData(bigChartData(5))

	c.HandleKey("l")
	c.HandleKey("j")
	if c.focus != 4 {
		t.Errorf("l+j: focus = %d, want 4", c.focus)
	}
	c.HandleKey("j")
	if c.focus != 7 {
		t.Errorf("j: focus = %d, want 7", c.focus)
	}
	c.HandleKey("j") // 7+3 > 8: clamped
	if c.focus != 7 {
		t.Errorf("j at bottom row: focus = %d, want 7", c.focus)
	}
	c.HandleKey("k")
	c.HandleKey("k")
	if c.focus != 1 {
		t.Errorf("k,k: focus = %d, want 1", c.focus)
	}
}
