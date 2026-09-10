package overlays

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

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
//
// A team can have a hundred repos, so the list scrolls inside the height
// the app gives it, and / narrows it by name.
type RepoPicker struct {
	checklist
	repos []RepoChoice
}

// NewRepoPicker opens on the current filter. An empty selected means every
// repo, so the picker starts fully checked rather than fully empty.
func NewRepoPicker(th *theme.Theme, repos []RepoChoice, selected []int64) RepoPicker {
	keep := make(map[int64]bool, len(selected))
	for _, id := range selected {
		keep[id] = true
	}
	names := make([]string, len(repos))
	checked := make([]bool, len(repos))
	for i, r := range repos {
		names[i] = r.Name
		checked[i] = len(selected) == 0 || keep[r.ID]
	}
	return RepoPicker{checklist: newChecklist(th, names, checked, "no repos match"), repos: repos}
}

// SetHeight gives the modal h rows to fit in; the list scrolls inside
// them. The app passes its content height on open and on every resize.
func (p *RepoPicker) SetHeight(h int) { p.setHeight(h) }

func (p RepoPicker) Update(msg tea.Msg) (RepoPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	if p.handle(key) {
		return p, nil
	}
	cancel := func() tea.Msg { return ReposCancelledMsg{} }
	switch key.String() {
	// esc reaches here only with no query left to clear. Both spellings of
	// the shifted letter, as the keymap binds them.
	case "esc", "R", "shift+r":
		return p, cancel
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
	return p.view(fmt.Sprintf("Filter repos · %d of %d", p.count(), len(p.repos)), "enter apply · esc cancel")
}
