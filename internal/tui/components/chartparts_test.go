package components

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/text"
)

// ansiRE strips color escapes so geometry assertions read the characters
// that actually land on screen.
var ansiRE = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func plain(s string) string { return ansiRE.ReplaceAllString(s, "") }

// probeStyles returns two styles that render distinguishably, so a test can
// tell which one covered a run without pinning lipgloss's exact escapes.
func probeStyles() (mark, faint lipgloss.Style) {
	return lipgloss.NewStyle().Foreground(lipgloss.Color("1")),
		lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
}

// Pad promises exactly w cells — rune counting broke that for CJK and emoji,
// shifting every bar and matrix column after a wide label.
func TestPadIsDisplayWidthAware(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"你好", 5, "你好 "},   // 4 cells + 1 space
		{"你好世界", 5, "你好…"}, // truncated: 4 cells + ellipsis
		{"abc", 2, "a…"},
		{"", 3, "   "},
	}
	for _, c := range cases {
		got := Pad(c.in, c.w)
		if got != c.want {
			t.Errorf("Pad(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
	// A wide char straddling the cut can leave a hole before the ellipsis;
	// the padding must still land on exactly w cells, and never split UTF-8.
	for _, s := range []string{"你好世", "a你b好c", "naïve🎉name"} {
		for w := 2; w <= 8; w++ {
			got := Pad(s, w)
			if !utf8.ValidString(got) {
				t.Errorf("Pad(%q, %d) produced invalid UTF-8 %q", s, w, got)
			}
			if text.Width(got) != w {
				t.Errorf("Pad(%q, %d) is %d cells, want %d", s, w, text.Width(got), w)
			}
		}
	}
}

// A bar's cell count is the whole contract of scaleTo: zero and a
// nonpositive scale draw nothing, any nonzero value keeps at least one cell
// so it cannot vanish, and an out-of-range value clamps instead of
// overflowing the column.
func TestBarRowCellCount(t *testing.T) {
	mark, txt := probeStyles()
	cases := []struct {
		name       string
		value, max float64
		barW, want int
	}{
		{"zero value", 0, 10, 10, 0},
		{"negative value", -3, 10, 10, 0},
		{"zero max", 5, 0, 10, 0},
		{"negative max", 5, -1, 10, 0},
		{"full", 10, 10, 10, 10},
		{"half", 5, 10, 10, 5},
		{"tiny but nonzero", 0.01, 10, 10, 1},
		{"over max clamps", 20, 10, 10, 10},
		{"rounds up at .5", 0.25, 1, 10, 3},
		{"rounds down below .5", 0.24, 1, 10, 2},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := plain(BarRow("alice", 6, c.value, c.max, c.barW, "12", mark, txt, txt))
			want := "alice " + " " + strings.Repeat("█", c.want) +
				strings.Repeat(" ", c.barW-c.want+1) + "12"
			if got != want {
				t.Errorf("BarRow(%v/%v, barW %d) = %q, want %q (%d cells)",
					c.value, c.max, c.barW, got, want, c.want)
			}
		})
	}
}

// Every row in a bar chart has to be the same width whatever it plots, or
// the printed values stop lining up down the column.
func TestBarRowWidthIsIndependentOfValue(t *testing.T) {
	mark, txt := probeStyles()
	const labelW, barW = 8, 12
	want := labelW + barW + 2 + text.Width("1.2k")
	for _, v := range []float64{0, 0.001, 1, 7, 40, 40.5, 999} {
		row := plain(BarRow("bob", labelW, v, 40, barW, "1.2k", mark, txt, txt))
		if got := text.Width(row); got != want {
			t.Errorf("BarRow(value %v) is %d cells, want %d: %q", v, got, want, row)
		}
	}
}

// The spacer rule: nonzero segments are separated by one cell so adjacent
// colors stay readable, and a zero segment contributes neither a block nor a
// spacer — an empty bucket must not widen the bar.
func TestStackedRowSpacerRule(t *testing.T) {
	mark, faint := probeStyles()
	segs := func(vals ...float64) []Seg {
		out := make([]Seg, len(vals))
		for i, v := range vals {
			out[i] = Seg{Value: v, Style: mark}
		}
		return out
	}

	twoNonzero := plain(StackedRow("bob", 5, segs(3, 3), 6, 10, "9", faint, faint))
	want := "bob  " + " " + "█████" + " " + "███" + "  " + "9"
	if twoNonzero != want {
		t.Errorf("two segments = %q, want %q", twoNonzero, want)
	}

	withZero := plain(StackedRow("bob", 5, segs(3, 0, 3), 6, 10, "9", faint, faint))
	if withZero != twoNonzero {
		t.Errorf("a zero segment changed the bar:\n got %q\nwant %q", withZero, twoNonzero)
	}
}

// Segments scale against the width still unspent, so a saturated bar stops
// at barW instead of running into the printed total.
func TestStackedRowClampsToBarWidth(t *testing.T) {
	mark, faint := probeStyles()
	const barW = 6
	full := []Seg{{Value: 10, Style: mark}, {Value: 10, Style: mark}}
	got := plain(StackedRow("x", 2, full, 10, barW, "20", faint, faint))
	if want := "x " + " " + strings.Repeat("█", barW) + " " + "20"; got != want {
		t.Errorf("saturated bar = %q, want %q", got, want)
	}

	// A spacer that would reach barW ends the bar rather than overrunning it.
	tight := []Seg{{Value: 2, Style: mark}, {Value: 2, Style: mark}}
	got = plain(StackedRow("x", 2, tight, 4, 2, "4", faint, faint))
	if want := "x " + " " + "█" + " " + " " + "4"; got != want {
		t.Errorf("bar with no room for a second segment = %q, want %q", got, want)
	}
}

// Sparkrow maps values onto eighth blocks, floors any nonzero value at one
// eighth so it stays visible, and clamps anything at or above max to a full
// block. Zeros are dots, not blanks, so a gap reads as a gap.
func TestSparkrowGlyphs(t *testing.T) {
	mark, faint := probeStyles()
	cases := []struct {
		name string
		vals []float64
		max  float64
		want string
	}{
		{"ramp", []float64{0, 1, 4, 8}, 8, "·▁▄█"},
		{"tiny stays visible", []float64{0.0001}, 8, "▁"},
		{"over max clamps", []float64{20}, 8, "█"},
		{"negative is a gap", []float64{-4}, 8, "·"},
		{"nonpositive max scales against 1", []float64{0.5}, 0, "▄"},
		{"empty", nil, 8, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := plain(Sparkrow(c.vals, c.max, mark, faint)); got != c.want {
				t.Errorf("Sparkrow(%v, max %v) = %q, want %q", c.vals, c.max, got, c.want)
			}
		})
	}
}

// Consecutive cells of the same kind are rendered as one styled run: the
// dots must carry the faint style and the blocks the series style, and
// batching them is what keeps a sparkline from emitting an escape per cell.
func TestSparkrowBatchesRunsByStyle(t *testing.T) {
	mark, faint := probeStyles()
	got := Sparkrow([]float64{0, 0, 8, 8, 0}, 8, mark, faint)
	want := faint.Render("··") + mark.Render("██") + faint.Render("·")
	if got != want {
		t.Errorf("run batching changed:\n got %q\nwant %q", got, want)
	}
}

// The chart is returned top row first and each column is drawn from the
// bottom up, with partial eighths at the tip. A zero column shows a faint
// dot on the baseline so an empty bucket is visible rather than blank.
func TestColumnChartRowsAndBaseline(t *testing.T) {
	mark, faint := probeStyles()
	rows := ColumnChart([]float64{0, 1, 2}, 2, 3, 2, mark, faint)

	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if got, want := plain(rows[0]), "  █"; got != want {
		t.Errorf("top row = %q, want %q — only the tallest column reaches it", got, want)
	}
	if got, want := plain(rows[1]), "·██"; got != want {
		t.Errorf("baseline row = %q, want %q — the zero column is a faint dot", got, want)
	}
	if !strings.Contains(rows[1], faint.Render("·")) {
		t.Errorf("the baseline dot is not drawn in the faint style: %q", rows[1])
	}
}

// Columns widen to use the space when there is room, capped at four cells
// and separated by one, and stay a single cell when there is not. Either way
// the chart must not exceed the width it was given.
func TestColumnChartColumnWidths(t *testing.T) {
	mark, faint := probeStyles()
	cases := []struct {
		name      string
		vals      int
		w         int
		wantWidth int
	}{
		{"one cell per column when tight", 10, 10, 10},
		{"widens to fill", 5, 20, 20},
		{"caps at four cells", 2, 100, 8},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			vals := make([]float64, c.vals)
			for i := range vals {
				vals[i] = 1
			}
			rows := ColumnChart(vals, 1, c.w, 3, mark, faint)
			for i, row := range rows {
				if got := text.Width(plain(row)); got != c.wantWidth {
					t.Errorf("row %d is %d cells, want %d: %q", i, got, c.wantWidth, plain(row))
				}
				if text.Width(plain(row)) > c.w {
					t.Errorf("row %d overruns the %d-cell budget", i, c.w)
				}
			}
		})
	}
}

// A nonpositive max would divide by zero when sizing the rows; it scales
// against 1 instead, and an all-zero series still renders its baseline.
func TestColumnChartSurvivesNoData(t *testing.T) {
	mark, faint := probeStyles()
	rows := ColumnChart([]float64{0, 0, 0}, 0, 3, 2, mark, faint)
	if len(rows) != 2 {
		t.Fatalf("got %d rows, want 2", len(rows))
	}
	if got, want := plain(rows[0]), "   "; got != want {
		t.Errorf("top row = %q, want %q", got, want)
	}
	if got, want := plain(rows[1]), "···"; got != want {
		t.Errorf("baseline = %q, want %q", got, want)
	}
	if empty := ColumnChart(nil, 1, 10, 2, mark, faint); len(empty) != 2 {
		t.Errorf("ColumnChart with no values returned %d rows, want 2", len(empty))
	}
}
