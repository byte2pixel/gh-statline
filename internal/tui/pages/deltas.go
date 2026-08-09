package pages

import (
	"fmt"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// deltaCount formats the change between two volume counts (more is greener).
// Shared by the stat tiles and the trends rows so the color semantics can
// never drift.
func deltaCount(th *theme.Theme, curr, prev int, hasPrev bool) (string, lipgloss.Style) {
	switch {
	case !hasPrev:
		return "–", th.HelpDesc
	case prev == 0 && curr == 0:
		return "±0", th.HelpDesc
	case prev == 0:
		return "▲new", lipgloss.NewStyle().Foreground(th.Good)
	}
	pct := (curr - prev) * 100 / prev
	switch {
	case pct > 0:
		return fmt.Sprintf("▲%d%%", pct), lipgloss.NewStyle().Foreground(th.Good)
	case pct < 0:
		return fmt.Sprintf("▼%d%%", -pct), lipgloss.NewStyle().Foreground(th.Bad)
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
