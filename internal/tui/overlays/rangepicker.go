// Package overlays contains Statline's modal components; while one is
// active it swallows all key input.
package overlays

import (
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// RangeChosenMsg is emitted when the user confirms a custom date range.
type RangeChosenMsg struct{ From, To time.Time }

// RangeCancelledMsg is emitted when the picker is dismissed.
type RangeCancelledMsg struct{}

const dateLayout = "2006-01-02"

// RangePicker is a small modal with from/to date inputs.
type RangePicker struct {
	theme *theme.Theme
	from  textinput.Model
	to    textinput.Model
	focus int // 0 = from, 1 = to
	err   string
}

func NewRangePicker(th *theme.Theme) RangePicker {
	mk := func(ph string) textinput.Model {
		ti := textinput.New()
		ti.Placeholder = ph
		ti.CharLimit = 10
		ti.SetWidth(12)
		return ti
	}
	now := time.Now()
	p := RangePicker{
		theme: th,
		from:  mk(now.AddDate(0, -3, 0).Format(dateLayout)),
		to:    mk(now.Format(dateLayout)),
	}
	p.from.Focus()
	return p
}

func (p *RangePicker) SetTheme(th *theme.Theme) { p.theme = th }

func (p RangePicker) Update(msg tea.Msg) (RangePicker, tea.Cmd) {
	if key, ok := msg.(tea.KeyPressMsg); ok {
		switch key.String() {
		case "esc":
			return p, func() tea.Msg { return RangeCancelledMsg{} }
		case "tab", "shift+tab":
			p.swapFocus()
			return p, nil
		case "enter":
			if p.focus == 0 {
				p.swapFocus()
				return p, nil
			}
			from, err1 := time.Parse(dateLayout, p.from.Value())
			to, err2 := time.Parse(dateLayout, p.to.Value())
			if err1 != nil || err2 != nil {
				p.err = "use YYYY-MM-DD"
				return p, nil
			}
			if to.Before(from) {
				p.err = "'to' is before 'from'"
				return p, nil
			}
			return p, func() tea.Msg { return RangeChosenMsg{From: from, To: to} }
		}
	}
	var cmd tea.Cmd
	if p.focus == 0 {
		p.from, cmd = p.from.Update(msg)
	} else {
		p.to, cmd = p.to.Update(msg)
	}
	return p, cmd
}

func (p *RangePicker) swapFocus() {
	p.err = ""
	if p.focus == 0 {
		p.focus = 1
		p.from.Blur()
		p.to.Focus()
	} else {
		p.focus = 0
		p.to.Blur()
		p.from.Focus()
	}
}

// View renders the modal box; the app centers it over the page.
func (p RangePicker) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Primary).Render("Custom range")
	body := lipgloss.JoinVertical(lipgloss.Left,
		title,
		"",
		"from  "+p.from.View(),
		"to    "+p.to.View(),
		"",
		p.theme.HelpDesc.Render("enter apply · tab switch · esc cancel"),
	)
	if p.err != "" {
		body = lipgloss.JoinVertical(lipgloss.Left, body,
			lipgloss.NewStyle().Foreground(p.theme.Bad).Render(p.err))
	}
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(p.theme.Primary).
		Padding(1, 2).
		Render(body)
}
