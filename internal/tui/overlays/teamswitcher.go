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

// TeamDeleteMsg asks the app to delete the named team profile.
type TeamDeleteMsg struct{ Name string }

// TeamSwitcher is a small modal listing the configured team profiles.
type TeamSwitcher struct {
	theme   *theme.Theme
	names   []string
	current int
	cursor  int
	armed   bool   // delete confirmation pending for names[cursor]
	note    string // inline refusal, e.g. deleting the only team
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
	// While armed every key is swallowed: y deletes, anything else disarms,
	// so the target can never drift from the name shown in the footer.
	if ts.armed {
		ts.armed = false
		if key.String() == "y" {
			name := ts.names[ts.cursor]
			return ts, func() tea.Msg { return TeamDeleteMsg{Name: name} }
		}
		return ts, nil
	}
	ts.note = ""
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
	case "d":
		if len(ts.names) <= 1 {
			ts.note = "can't delete the only team"
		} else {
			ts.armed = true
		}
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
	footer := ts.theme.HelpDesc.Render("enter switch · d delete · esc cancel")
	if ts.armed {
		footer = lipgloss.NewStyle().Foreground(ts.theme.Bad).Render("delete " + ts.names[ts.cursor] + "? y/n")
	} else if ts.note != "" {
		footer = lipgloss.NewStyle().Foreground(ts.theme.Bad).Render(ts.note)
	}
	lines = append(lines, "", footer)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(ts.theme.Primary).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
