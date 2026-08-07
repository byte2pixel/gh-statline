package overlays

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// TeamChosenMsg is emitted when the user picks a team profile.
type TeamChosenMsg struct{ Name string }

// TeamCancelledMsg is emitted when the switcher is dismissed.
type TeamCancelledMsg struct{}

// TeamSwitcher is a small modal listing the configured team profiles.
type TeamSwitcher struct {
	theme   *theme.Theme
	names   []string
	current int
	cursor  int
}

func NewTeamSwitcher(th *theme.Theme, names []string, current string) TeamSwitcher {
	ts := TeamSwitcher{theme: th, names: names}
	for i, n := range names {
		if n == current {
			ts.current, ts.cursor = i, i
		}
	}
	return ts
}

func (ts TeamSwitcher) Update(msg tea.Msg) (TeamSwitcher, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return ts, nil
	}
	switch key.String() {
	case "esc", "t":
		return ts, func() tea.Msg { return TeamCancelledMsg{} }
	case "up", "k":
		if ts.cursor > 0 {
			ts.cursor--
		}
	case "down", "j":
		if ts.cursor < len(ts.names)-1 {
			ts.cursor++
		}
	case "enter":
		name := ts.names[ts.cursor]
		return ts, func() tea.Msg { return TeamChosenMsg{Name: name} }
	}
	return ts, nil
}

func (ts TeamSwitcher) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(ts.theme.Primary).Render("Switch team")
	lines := []string{title, ""}
	for i, n := range ts.names {
		marker := "  "
		if i == ts.current {
			marker = "· "
		}
		line := marker + n
		if i == ts.cursor {
			line = ts.theme.Selected.Render("▸ " + line[2:])
		}
		lines = append(lines, line)
	}
	lines = append(lines, "", ts.theme.HelpDesc.Render("enter switch · esc cancel"))
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ts.theme.Primary).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
