// Package theme centralizes Statline's visual identity: a Charm-style
// chrome palette (purples/pinks/teals) with explicit light/dark variants,
// and an Okabe–Ito colorblind-safe palette reserved for chart series.
package theme

import (
	"image/color"

	lipgloss "charm.land/lipgloss/v2"
)

type Theme struct {
	IsDark bool

	Primary color.Color // Charm purple — brand, headers, selection
	Accent  color.Color // pink — highlights, active sort
	Good    color.Color // green — approvals, merged
	Warn    color.Color // amber — changes requested
	Bad     color.Color // red — errors
	Text    color.Color
	Subtle  color.Color // secondary text
	Faint   color.Color // borders, separators

	// Series is the chart palette (Okabe–Ito), distinguishable under the
	// common forms of color vision deficiency.
	Series []color.Color

	Title       lipgloss.Style // app name chip in the header bar
	Header      lipgloss.Style // header bar text
	StatusBar   lipgloss.Style
	StatusError lipgloss.Style
	TableHeader lipgloss.Style
	TableCell   lipgloss.Style
	Selected    lipgloss.Style
	HelpKey     lipgloss.Style
	HelpDesc    lipgloss.Style
}

// New builds the theme for the terminal's background. Call once at startup
// (and again if a tea.BackgroundColorMsg reports a change).
func New(isDark bool) Theme {
	pick := lipgloss.LightDark(isDark)

	t := Theme{
		IsDark:  isDark,
		Primary: pick(lipgloss.Color("#6C3FD8"), lipgloss.Color("#9B79FF")),
		Accent:  pick(lipgloss.Color("#C7317F"), lipgloss.Color("#FF6AC1")),
		Good:    pick(lipgloss.Color("#02845A"), lipgloss.Color("#2CE0A7")),
		Warn:    pick(lipgloss.Color("#A8820A"), lipgloss.Color("#F2C744")),
		Bad:     pick(lipgloss.Color("#D11A5B"), lipgloss.Color("#FF5F87")),
		Text:    pick(lipgloss.Color("#2B2B33"), lipgloss.Color("#E6E6EF")),
		Subtle:  pick(lipgloss.Color("#8A8A98"), lipgloss.Color("#777788")),
		Faint:   pick(lipgloss.Color("#DDDDE6"), lipgloss.Color("#3A3A4A")),
		Series: []color.Color{
			lipgloss.Color("#0072B2"), // blue
			lipgloss.Color("#E69F00"), // orange
			lipgloss.Color("#009E73"), // green
			lipgloss.Color("#CC79A7"), // pink
			lipgloss.Color("#56B4E9"), // sky
			lipgloss.Color("#D55E00"), // vermillion
			lipgloss.Color("#F0E442"), // yellow
		},
	}

	selBG := pick(lipgloss.Color("#E8DFFC"), lipgloss.Color("#3B2D66"))
	barBG := pick(lipgloss.Color("#ECECF4"), lipgloss.Color("#2A2A38"))

	t.Title = lipgloss.NewStyle().Bold(true).
		Foreground(pick(lipgloss.Color("#FFFFFF"), lipgloss.Color("#1A1025"))).
		Background(t.Primary).Padding(0, 1)
	t.Header = lipgloss.NewStyle().Foreground(t.Subtle)
	t.StatusBar = lipgloss.NewStyle().Foreground(t.Subtle).Background(barBG).Padding(0, 1)
	t.StatusError = lipgloss.NewStyle().Foreground(t.Bad).Background(barBG).Padding(0, 1)
	t.TableHeader = lipgloss.NewStyle().Bold(true).Foreground(t.Primary).Padding(0, 1).
		BorderStyle(lipgloss.NormalBorder()).BorderBottom(true).BorderForeground(t.Faint)
	t.TableCell = lipgloss.NewStyle().Foreground(t.Text).Padding(0, 1)
	t.Selected = lipgloss.NewStyle().Bold(true).Foreground(t.Text).Background(selBG)
	t.HelpKey = lipgloss.NewStyle().Foreground(t.Accent)
	t.HelpDesc = lipgloss.NewStyle().Foreground(t.Subtle)
	return t
}
