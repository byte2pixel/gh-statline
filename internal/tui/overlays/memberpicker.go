package overlays

import (
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/components"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// MemberChoice is one configured member: the login, and whether the config
// hides them from every view today.
type MemberChoice struct {
	Login  string
	Hidden bool
}

// MembersChosenMsg carries the hidden flag the user settled on for every
// member, keyed by login, once the picker is applied.
type MembersChosenMsg struct{ Hidden map[string]bool }

// MembersCancelledMsg is emitted when the picker is dismissed.
type MembersCancelledMsg struct{}

// MemberPicker is a checkbox list over the active team's configured
// members, checked meaning shown. Applying it writes hidden: to the config
// file, which until now was the only place the flag could be set. A hidden
// member stays in the file and in the cache and leaves every number.
type MemberPicker struct {
	components.Checklist
	members []MemberChoice
}

// NewMemberPicker opens with the cursor on login, the row the table had
// selected, so hiding the person in front of you is m, space, enter. An
// unknown or empty login leaves the cursor on the first row.
func NewMemberPicker(th *theme.Theme, members []MemberChoice, login string) MemberPicker {
	names := make([]string, len(members))
	checked := make([]bool, len(members))
	cursor := 0
	for i, m := range members {
		names[i] = m.Login
		checked[i] = !m.Hidden
		if m.Login == login {
			cursor = i
		}
	}
	p := MemberPicker{Checklist: components.NewChecklist(th, names, checked, "no members match"), members: members}
	p.SetCursor(cursor)
	return p
}

func (p MemberPicker) Update(msg tea.Msg) (MemberPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	if p.Handle(key) {
		return p, nil
	}
	cancel := func() tea.Msg { return MembersCancelledMsg{} }
	switch key.String() {
	// esc reaches here only with no query left to clear; m closes the
	// picker it opened, the way t closes the team switcher.
	case "esc", "m":
		return p, cancel
	case "enter":
		if p.Count() == 0 {
			// Every member hidden renders an empty table that reads like a
			// team with nothing to show. Refuse rather than write it.
			p.SetNote("keep at least one member shown")
			return p, nil
		}
		hidden := make(map[string]bool, len(p.members))
		for i, m := range p.members {
			hidden[m.Login] = !p.Checked(i)
		}
		return p, func() tea.Msg { return MembersChosenMsg{Hidden: hidden} }
	}
	return p, nil
}

// View renders the modal box; the app centers it over the page.
func (p MemberPicker) View() string {
	return p.Checklist.View(fmt.Sprintf("Members · %d of %d shown", p.Count(), len(p.members)), "enter save · esc cancel")
}
