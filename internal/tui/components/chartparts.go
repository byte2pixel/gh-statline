// Package components holds Statline's shared chart rendering primitives.
// Every helper prints values in text styles and lets a colored mark carry
// identity — numbers first, color second.
package components

import (
	"strings"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/text"
)

// Pad right-pads or truncates s to exactly w display cells. Cells, not
// runes: CJK and emoji occupy two columns, and a rune count would push
// every later bar and matrix column out of line.
func Pad(s string, w int) string {
	sw := text.Width(s)
	if sw > w {
		s = text.Truncate(s, w)
		sw = text.Width(s)
	}
	return s + strings.Repeat(" ", w-sw)
}

func scaleTo(value, max float64, width int) int {
	if max <= 0 || value <= 0 {
		return 0
	}
	n := int(value/max*float64(width) + 0.5)
	if n == 0 {
		n = 1 // nonzero values always show at least one cell
	}
	if n > width {
		n = width
	}
	return n
}

// BarRow renders `label ███████ 12`: label and value in text styles, the
// bar in the series style.
func BarRow(label string, labelW int, value, max float64, barW int, display string,
	bar, txt, value2 lipgloss.Style) string {
	n := scaleTo(value, max, barW)
	return txt.Render(Pad(label, labelW)) + " " +
		bar.Render(strings.Repeat("█", n)) + strings.Repeat(" ", barW-n+1) +
		value2.Render(display)
}

// Seg is one segment of a stacked bar.
type Seg struct {
	Value float64
	Style lipgloss.Style
}

// StackedRow renders segments on a shared scale with 1-cell gaps between
// nonzero segments (the spacer rule), followed by a printed total.
func StackedRow(label string, labelW int, segs []Seg, max float64, barW int,
	display string, txt, value lipgloss.Style) string {
	var b strings.Builder
	used := 0
	for _, s := range segs {
		n := scaleTo(s.Value, max, barW-used)
		if n == 0 {
			continue
		}
		if used > 0 {
			b.WriteString(" ")
			used++
			if used >= barW {
				break
			}
		}
		b.WriteString(s.Style.Render(strings.Repeat("█", n)))
		used += n
	}
	if used > barW {
		used = barW
	}
	return txt.Render(Pad(label, labelW)) + " " + b.String() +
		strings.Repeat(" ", barW-used+1) + value.Render(display)
}

var blockEighths = []rune(" ▁▂▃▄▅▆▇█")

// Sparkrow renders values as one line of block characters; zeros render as
// a faint dot so gaps stay visible against the baseline.
func Sparkrow(vals []float64, max float64, mark, faint lipgloss.Style) string {
	if max <= 0 {
		max = 1
	}
	var out, run strings.Builder
	runFaint := false
	flush := func() {
		if run.Len() == 0 {
			return
		}
		style := mark
		if runFaint {
			style = faint
		}
		out.WriteString(style.Render(run.String()))
		run.Reset()
	}
	for _, v := range vals {
		isZero := v <= 0
		if run.Len() > 0 && isZero != runFaint {
			flush()
		}
		runFaint = isZero
		if isZero {
			run.WriteString("·")
			continue
		}
		idx := int(v/max*8 + 0.5)
		if idx < 1 {
			idx = 1
		}
		if idx > 8 {
			idx = 8
		}
		run.WriteRune(blockEighths[idx])
	}
	flush()
	return out.String()
}

// ColumnChart renders values as an h-row column chart and returns the rows
// top-first. Partial cells use eighth blocks. Columns widen to fill w when
// there is room, with a 1-cell gap once columns are ≥2 wide.
func ColumnChart(vals []float64, max float64, w, h int, mark, faint lipgloss.Style) []string {
	if max <= 0 {
		max = 1
	}
	colW, gap := 1, 0
	if len(vals) > 0 && w/len(vals) >= 2 {
		colW = w / len(vals)
		if colW > 4 {
			colW = 4
		}
		if colW >= 2 {
			gap = 1
			colW--
		}
	}
	rows := make([]string, h)
	unit := max / float64(h)
	for r := 0; r < h; r++ {
		var marks strings.Builder
		line := ""
		flush := func(style lipgloss.Style) {
			if marks.Len() > 0 {
				line += style.Render(marks.String())
				marks.Reset()
			}
		}
		level := float64(h-1-r) * unit // value covered below this row
		for _, v := range vals {
			rem := v - level
			switch {
			case rem <= 0:
				flush(mark)
				if r == h-1 && v <= 0 {
					line += faint.Render(strings.Repeat("·", colW))
				} else {
					line += strings.Repeat(" ", colW)
				}
			default:
				frac := rem / unit
				idx := int(frac*8 + 0.5)
				if idx < 1 {
					idx = 1
				}
				if idx > 8 {
					idx = 8
				}
				for i := 0; i < colW; i++ {
					marks.WriteRune(blockEighths[idx])
				}
			}
			if gap > 0 {
				flush(mark)
				line += " "
			}
		}
		flush(mark)
		rows[r] = line
	}
	return rows
}
