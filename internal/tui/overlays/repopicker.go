package overlays

import (
	"fmt"
	"slices"
	"strings"

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
//
// A team can have a hundred repos, so the list scrolls inside the height
// the app gives it, and / narrows it by name. Deliberately hand-rolled,
// like the wizard's review list: the bubbles list component filters but
// does not do checkboxes.
type RepoPicker struct {
	theme   *theme.Theme
	repos   []RepoChoice
	checked []bool
	// cursor and offset index the matching rows, not repos: with a query
	// active the list is the subset that matches.
	cursor int
	offset int
	height int // rows the whole modal may take; 0 lifts the cap
	// query narrows the list by case-insensitive substring. filtering is
	// set while the query is being typed, entered with / and left with
	// enter or esc; outside it the picker's own keys apply.
	query     string
	filtering bool
	note      string // inline refusal, e.g. applying with nothing checked
}

// Rows the modal spends outside the list: border and padding, the title,
// two blank lines, and the two-line footer. A query line adds one, and the
// scroll indicators take two from the list's share. minListRows keeps a
// few repos on screen on a terminal too short for the chrome, where the
// modal overflows rather than showing an empty list.
const (
	pickerChrome = 10
	minListRows  = 3
)

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

// SetHeight caps the whole modal at h rows; the list scrolls inside it.
// The app passes its content height on open and on every resize.
func (p *RepoPicker) SetHeight(h int) {
	p.height = h
	p.scroll()
}

func (p RepoPicker) Update(msg tea.Msg) (RepoPicker, tea.Cmd) {
	key, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	p.note = ""
	if p.filtering {
		p.typeQuery(key)
		return p, nil
	}
	cancel := func() tea.Msg { return ReposCancelledMsg{} }
	switch key.String() {
	case "esc":
		if p.query != "" {
			// A narrowed list is the first thing esc undoes, the way the
			// bubbles list clears its filter; the next esc closes.
			p.setQuery("")
			return p, nil
		}
		return p, cancel
	// Both spellings of the shifted letter, as the keymap binds them.
	case "R", "shift+r":
		return p, cancel
	case "/":
		p.filtering = true
	case "up", "k":
		p.cursor--
		p.scroll()
	case "down", "j":
		p.cursor++
		p.scroll()
	case "space", " ":
		if m := p.matches(); len(m) > 0 {
			p.checked = slices.Clone(p.checked)
			p.checked[m[p.cursor]] = !p.checked[m[p.cursor]]
		}
	case "a":
		p.setAll(true)
	case "n":
		p.setAll(false)
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

// typeQuery edits the query while filtering. Every printable key goes
// into it, R and a and space included, so none of the picker's own keys
// can fire while a repo name is being typed.
func (p *RepoPicker) typeQuery(key tea.KeyPressMsg) {
	switch key.String() {
	case "esc":
		p.filtering = false
		p.setQuery("")
	case "enter":
		p.filtering = false // keep the narrowed list; the cursor keys take over
	case "backspace":
		if r := []rune(p.query); len(r) > 0 {
			p.setQuery(string(r[:len(r)-1]))
		}
	case "up":
		p.cursor--
		p.scroll()
	case "down":
		p.cursor++
		p.scroll()
	default:
		if key.Text != "" && !text.HasControls(key.Text) {
			p.setQuery(p.query + key.Text)
		}
	}
}

// setQuery replaces the query and puts the cursor on the first match: the
// row it sat on may not be in the new list at all.
func (p *RepoPicker) setQuery(q string) {
	p.query = q
	p.cursor, p.offset = 0, 0
	p.scroll()
}

// setAll checks or unchecks every matching repo. With a query active this
// is the fast path through a long list: n, then / and a name, then a.
func (p *RepoPicker) setAll(v bool) {
	p.checked = slices.Clone(p.checked)
	for _, i := range p.matches() {
		p.checked[i] = v
	}
}

// matches lists the repos the query keeps, as indices into repos.
func (p RepoPicker) matches() []int {
	q := strings.ToLower(p.query)
	out := make([]int, 0, len(p.repos))
	for i, r := range p.repos {
		if q == "" || strings.Contains(strings.ToLower(r.Name), q) {
			out = append(out, i)
		}
	}
	return out
}

func (p RepoPicker) showQuery() bool { return p.filtering || p.query != "" }

// listRows is the share of the height left for the list, indicators
// included.
func (p RepoPicker) listRows() int {
	chrome := pickerChrome
	if p.showQuery() {
		chrome++
	}
	return max(p.height-chrome, minListRows)
}

// window sizes the scroll window over n matching rows: how many are on
// screen, and whether the list overflows and needs its indicators.
func (p RepoPicker) window(n int) (visible int, overflow bool) {
	if p.height <= 0 || n <= p.listRows() {
		return n, false
	}
	return max(p.listRows()-2, 1), true
}

// scroll clamps the cursor to the matching rows and moves the window so
// the cursor stays on screen.
func (p *RepoPicker) scroll() {
	n := len(p.matches())
	p.cursor = min(p.cursor, n-1)
	p.cursor = max(p.cursor, 0)
	visible, _ := p.window(n)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+visible {
		p.offset = p.cursor - visible + 1
	}
	p.offset = min(p.offset, max(n-visible, 0))
	p.offset = max(p.offset, 0)
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

func (p RepoPicker) count() int {
	n := 0
	for _, c := range p.checked {
		if c {
			n++
		}
	}
	return n
}

// View renders the modal box; the app centers it over the page.
func (p RepoPicker) View() string {
	title := lipgloss.NewStyle().Bold(true).Foreground(p.theme.Primary).
		Render(fmt.Sprintf("Filter repos · %d of %d", p.count(), len(p.repos)))
	lines := []string{title}
	m := p.matches()
	if p.showQuery() {
		lines = append(lines, p.queryLine(len(m)))
	}
	lines = append(lines, "")
	lines = append(lines, p.rows(m)...)
	// The footer keeps its two rows whatever it says, so the box never
	// changes height under the cursor.
	second := p.theme.HelpDesc.Render("enter apply · esc cancel")
	if p.note != "" {
		second = lipgloss.NewStyle().Foreground(p.theme.Bad).Render(p.note)
	}
	lines = append(lines, "", p.theme.HelpDesc.Render("space toggle · a all · n none · / filter"), second)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(p.theme.Primary).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// queryLine echoes the query with a caret while it is being typed, and
// says how much of the list it keeps.
func (p RepoPicker) queryLine(matches int) string {
	q := "/" + text.Sanitize(p.query)
	if p.filtering {
		q += "▌"
	}
	tail := " · type to narrow"
	if p.query != "" {
		tail = fmt.Sprintf(" · %d of %d match", matches, len(p.repos))
	}
	return lipgloss.NewStyle().Foreground(p.theme.Accent).Render(q) + p.theme.HelpDesc.Render(tail)
}

// rows renders the scroll window over the matching repos, bracketed by
// the indicators when the list overflows.
func (p RepoPicker) rows(m []int) []string {
	n := len(m)
	if n == 0 {
		return []string{p.theme.HelpDesc.Render("  no repos match")}
	}
	visible, overflow := p.window(n)
	end := min(p.offset+visible, n)
	out := make([]string, 0, visible+2)
	if overflow {
		out = append(out, p.edge("↑", p.offset))
	}
	for pos := p.offset; pos < end; pos++ {
		i := m[pos]
		mark := "[ ]"
		if p.checked[i] {
			mark = "[x]"
		}
		// Repo names come from config, which Validate screens for control
		// characters, but the modal must be safe against a file that
		// skipped it, so they render sanitized anyway.
		line := mark + " " + text.Sanitize(p.repos[i].Name)
		if pos == p.cursor {
			line = p.theme.Selected.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		out = append(out, line)
	}
	if overflow {
		out = append(out, p.edge("↓", n-end))
	}
	return out
}

// edge is one scroll indicator: how many rows lie past that end of the
// window, or a blank line holding its place so the box never jumps.
func (p RepoPicker) edge(arrow string, hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return p.theme.HelpDesc.Render(fmt.Sprintf("  %s %d more", arrow, hidden))
}
