package overlays

import (
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/text"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// RepoChoice is one repo the picker offers: the cache id the metrics
// filter keys on, and the owner/name the user knows it by.
type RepoChoice struct {
	ID   int64
	Name string
}

// ReposChosenMsg carries the repo ids to narrow every view to. IDs is nil
// when every repo is checked: that is the unfiltered state, and the app
// should show no filter rather than one that happens to cover everything.
type ReposChosenMsg struct{ IDs []int64 }

// ReposCancelledMsg is emitted when the picker is dismissed.
type ReposCancelledMsg struct{}

// RepoPicker is a checkbox list over the active team's configured repos.
// The selection is one-shot per session, like a custom date range: repo
// ids are cache-local integers that belong to one team, so nothing here is
// meant to survive a restart.
type RepoPicker struct {
	theme   *theme.Theme
	repos   []RepoChoice
	checked []bool
	cursor  int
	note    string // inline refusal, e.g. applying with nothing checked
}

// NewRepoPicker opens on the current filter. An empty selected means every
// repo, so the picker starts fully checked rather than fully empty.
func NewRepoPicker(th *theme.Theme, repos []RepoChoice, selected []int64) RepoPicker {
	p := RepoPicker{theme: th, repos: repos, checked: make([]bool, len(repos))}
	keep := make(map[int64]bool, len(selected))
	for _, id := range selected {
		keep[id] = true
	}
	for i, r := range repos {
		p.checked[i] = len(selected) == 0 || keep[r.ID]
	}
	return p
}

func (p RepoPicker) Update(msg tea.Msg) (RepoPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	p.note = ""
	switch key.String() {
	// Both spellings of the shifted letter, as the keymap binds them.
	case "esc", "R", "shift+r":
		return p, func() tea.Msg { return ReposCancelledMsg{} }
	case "up", "k":
		if p.cursor > 0 {
			p.cursor--
		}
	case "down", "j":
		if p.cursor < len(p.repos)-1 {
			p.cursor++
		}
	case "space", " ":
		if len(p.repos) > 0 {
			p.checked[p.cursor] = !p.checked[p.cursor]
		}
	case "a":
		for i := range p.checked {
			p.checked[i] = true
		}
	case "enter":
		ids := p.selection()
		if ids != nil && len(ids) == 0 {
			// Nothing checked would render an empty dashboard that looks
			// like a quiet month. Refuse rather than apply it.
			p.note = "pick at least one repo"
			return p, nil
		}
		return p, func() tea.Msg { return ReposChosenMsg{IDs: ids} }
	}
	return p, nil
}

// selection is the checked ids in config order: nil when every repo is
// checked, which is no filter at all, and an empty slice when none is.
func (p RepoPicker) selection() []int64 {
	ids := make([]int64, 0, len(p.repos))
	for i, r := range p.repos {
		if p.checked[i] {
			ids = append(ids, r.ID)
		}
	}
	if len(ids) == len(p.repos) {
		return nil
	}
	return ids
}

// View renders the modal box; the app centers it over the page.
func (p RepoPicker) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Primary).Render("Filter repos")
	lines := []string{title, ""}
	for i, r := range p.repos {
		mark := "[ ]"
		if p.checked[i] {
			mark = "[x]"
		}
		// Repo names come from config, which Validate screens for control
		// characters, but the modal must be safe against a file that
		// skipped it, so they render sanitized anyway.
		line := mark + " " + text.Sanitize(r.Name)
		if i == p.cursor {
			line = p.theme.Selected.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		lines = append(lines, line)
	}
	footer := p.theme.HelpDesc.Render("space toggle · a all · enter apply · esc cancel")
	if p.note != "" {
		footer = lipgloss.NewStyle().Foreground(p.theme.Bad).Render(p.note)
	}
	lines = append(lines, "", footer)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(p.theme.Primary).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}
