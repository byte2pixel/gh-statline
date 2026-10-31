package pages

import (
	"fmt"
	"image/color"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// deltaCount formats the change between two volume counts (more is greener).
// Shared by the stat tiles and the trends rows so the color semantics can
// never drift; the percentage itself is metrics.PctChange, the same rounded
// definition the movers card uses.
func deltaCount(th *theme.Theme, curr, prev int, hasPrev bool) (string, lipgloss.Style) {
	return countDelta(th, curr, prev, hasPrev, th.Good, th.Bad)
}

// deltaBurden formats the change between two counts of something unwanted
// (fewer is greener): PRs closed without merging. Same arrows and
// percentage as deltaCount, only the colours trade places, so a rise reads
// as a rise everywhere and the colour alone says whether it is good news.
func deltaBurden(th *theme.Theme, curr, prev int, hasPrev bool) (string, lipgloss.Style) {
	return countDelta(th, curr, prev, hasPrev, th.Bad, th.Good)
}

// countDelta is the shared arithmetic; up is the colour of a rise.
func countDelta(th *theme.Theme, curr, prev int, hasPrev bool, up, down color.Color) (string, lipgloss.Style) {
	switch {
	case !hasPrev:
		return "–", th.HelpDesc
	case prev == 0 && curr == 0:
		return "±0", th.HelpDesc
	case prev == 0:
		return "▲new", lipgloss.NewStyle().Foreground(up)
	}
	pct := metrics.PctChange(prev, curr)
	switch {
	case pct > 0:
		return fmt.Sprintf("▲%d%%", pct), lipgloss.NewStyle().Foreground(up)
	case pct < 0:
		return fmt.Sprintf("▼%d%%", -pct), lipgloss.NewStyle().Foreground(down)
	}
	return "±0", th.HelpDesc
}

// deltaDur formats the change between two latencies (less is greener).
func deltaDur(th *theme.Theme, curr, prev time.Duration, hasPrev bool) (string, lipgloss.Style) {
	if !hasPrev || curr == 0 || prev == 0 {
		return "–", th.HelpDesc
	}
	switch d := curr - prev; {
	case d < 0:
		return "▼" + metrics.FmtDur(-d), lipgloss.NewStyle().Foreground(th.Good)
	case d > 0:
		return "▲" + metrics.FmtDur(d), lipgloss.NewStyle().Foreground(th.Bad)
	}
	return "±0", th.HelpDesc
}
