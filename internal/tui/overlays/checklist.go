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

// checklist is the list the repo picker and the member picker share: a
// column of checkboxes that scrolls inside the height the app gives it and
// narrows by name on /. It knows nothing about what a box means. Each
// picker names its rows, writes the title, and turns the checked set into
// its own message. Deliberately hand-rolled, like the wizard's review
// list: the bubbles list component filters but does not do checkboxes.
type checklist struct {
	theme   *theme.Theme
	names   []string
	checked []bool
	// cursor and offset index the matching rows, not names: with a query
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
	empty     string // the row shown when the query matches nothing
}

// Rows the modal spends outside the list: border and padding, the title,
// two blank lines, and the two-line footer. A query line adds one, and the
// scroll indicators take two from the list's share. minListRows keeps a
// few rows on screen on a terminal too short for the chrome, where the
// modal overflows rather than showing an empty list.
const (
	pickerChrome = 10
	minListRows  = 3
)

func newChecklist(th *theme.Theme, names []string, checked []bool, empty string) checklist {
	return checklist{theme: th, names: names, checked: checked, empty: empty}
}

// setHeight gives the modal h rows to fit in; the list scrolls inside
// them. The cap is not strict: on a terminal too short for the chrome plus
// minListRows the modal overflows instead, since a few rows on a modal
// that runs off the screen beat an empty list that fits. Zero lifts the
// cap.
func (c *checklist) setHeight(h int) {
	c.height = h
	c.scroll()
}

// handle applies one key and reports whether the list owned it: the
// cursor, the checkboxes, the query, and esc while a query is narrowing
// the list. The picker deals with what is left, applying and closing.
func (c *checklist) handle(key tea.KeyPressMsg) bool {
	c.note = ""
	if c.filtering {
		c.typeQuery(key)
		return true
	}
	switch key.String() {
	case "esc":
		if c.query == "" {
			return false
		}
		// A narrowed list is the first thing esc undoes, the way the
		// bubbles list clears its filter; the next esc closes.
		c.setQuery("")
	case "/":
		c.filtering = true
	case "up", "k":
		c.cursor--
		c.scroll()
	case "down", "j":
		c.cursor++
		c.scroll()
	case "space", " ":
		if m := c.matches(); len(m) > 0 {
			c.checked = slices.Clone(c.checked)
			c.checked[m[c.cursor]] = !c.checked[m[c.cursor]]
		}
	case "a":
		c.setAll(true)
	case "n":
		c.setAll(false)
	default:
		return false
	}
	return true
}

// typeQuery edits the query while filtering. Every printable key goes
// into it, the pickers' own letters and space included, so none of their
// keys can fire while a name is being typed.
func (c *checklist) typeQuery(key tea.KeyPressMsg) {
	switch key.String() {
	case "esc":
		c.filtering = false
		c.setQuery("")
	case "enter":
		c.filtering = false // keep the narrowed list; the cursor keys take over
	case "backspace":
		if r := []rune(c.query); len(r) > 0 {
			c.setQuery(string(r[:len(r)-1]))
		}
	case "up":
		c.cursor--
		c.scroll()
	case "down":
		c.cursor++
		c.scroll()
	default:
		if key.Text != "" && !text.HasControls(key.Text) {
			c.setQuery(c.query + key.Text)
		}
	}
}

// setQuery replaces the query and puts the cursor on the first match: the
// row it sat on may not be in the new list at all.
func (c *checklist) setQuery(q string) {
	c.query = q
	c.cursor, c.offset = 0, 0
	c.scroll()
}

// setAll checks or unchecks every matching row. With a query active this
// is the fast path through a long list: n, then / and a name, then a.
func (c *checklist) setAll(v bool) {
	c.checked = slices.Clone(c.checked)
	for _, i := range c.matches() {
		c.checked[i] = v
	}
}

// matches lists the rows the query keeps, as indices into names.
func (c checklist) matches() []int {
	q := strings.ToLower(c.query)
	out := make([]int, 0, len(c.names))
	for i, n := range c.names {
		if q == "" || strings.Contains(strings.ToLower(n), q) {
			out = append(out, i)
		}
	}
	return out
}

func (c checklist) showQuery() bool { return c.filtering || c.query != "" }

// listRows is the share of the height left for the list, indicators
// included.
func (c checklist) listRows() int {
	chrome := pickerChrome
	if c.showQuery() {
		chrome++
	}
	return max(c.height-chrome, minListRows)
}

// window sizes the scroll window over n matching rows: how many are on
// screen, and whether the list overflows and needs its indicators.
func (c checklist) window(n int) (visible int, overflow bool) {
	if c.height <= 0 || n <= c.listRows() {
		return n, false
	}
	return max(c.listRows()-2, 1), true
}

// scroll clamps the cursor to the matching rows and moves the window so
// the cursor stays on screen.
func (c *checklist) scroll() {
	n := len(c.matches())
	c.cursor = min(c.cursor, n-1)
	c.cursor = max(c.cursor, 0)
	visible, _ := c.window(n)
	if c.cursor < c.offset {
		c.offset = c.cursor
	}
	if c.cursor >= c.offset+visible {
		c.offset = c.cursor - visible + 1
	}
	c.offset = min(c.offset, max(n-visible, 0))
	c.offset = max(c.offset, 0)
}

// count is how many rows are checked, query or no query.
func (c checklist) count() int {
	n := 0
	for _, v := range c.checked {
		if v {
			n++
		}
	}
	return n
}

// view renders the modal box; the app centers it over the page. title is
// the picker's heading and apply its second footer line, which the note
// replaces while there is one.
func (c checklist) view(title, apply string) string {
	head := lipgloss.NewStyle().Bold(true).Foreground(c.theme.Primary).Render(title)
	lines := []string{head}
	m := c.matches()
	if c.showQuery() {
		lines = append(lines, c.queryLine(len(m)))
	}
	lines = append(lines, "")
	lines = append(lines, c.rows(m)...)
	// The footer keeps its two rows whatever it says, so the box never
	// changes height under the cursor.
	second := c.theme.HelpDesc.Render(apply)
	if c.note != "" {
		second = lipgloss.NewStyle().Foreground(c.theme.Bad).Render(c.note)
	}
	lines = append(lines, "", c.theme.HelpDesc.Render("space toggle · a all · n none · / filter"), second)
	return lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).
		BorderForeground(c.theme.Primary).
		Padding(1, 2).
		Render(lipgloss.JoinVertical(lipgloss.Left, lines...))
}

// queryLine echoes the query with a caret while it is being typed, and
// says how much of the list it keeps.
func (c checklist) queryLine(matches int) string {
	q := "/" + text.Sanitize(c.query)
	if c.filtering {
		q += "▌"
	}
	tail := " · type to narrow"
	if c.query != "" {
		tail = fmt.Sprintf(" · %d of %d match", matches, len(c.names))
	}
	return lipgloss.NewStyle().Foreground(c.theme.Accent).Render(q) + c.theme.HelpDesc.Render(tail)
}

// rows renders the scroll window over the matching rows, bracketed by
// the indicators when the list overflows.
func (c checklist) rows(m []int) []string {
	n := len(m)
	if n == 0 {
		return []string{c.theme.HelpDesc.Render("  " + c.empty)}
	}
	visible, overflow := c.window(n)
	end := min(c.offset+visible, n)
	out := make([]string, 0, visible+2)
	if overflow {
		out = append(out, c.edge("↑", c.offset))
	}
	for pos := c.offset; pos < end; pos++ {
		i := m[pos]
		mark := "[ ]"
		if c.checked[i] {
			mark = "[x]"
		}
		// Names come from config, which Validate screens for control
		// characters, but the modal must be safe against a file that
		// skipped it, so they render sanitized anyway.
		line := mark + " " + text.Sanitize(c.names[i])
		if pos == c.cursor {
			line = c.theme.Selected.Render("▸ " + line)
		} else {
			line = "  " + line
		}
		out = append(out, line)
	}
	if overflow {
		out = append(out, c.edge("↓", n-end))
	}
	return out
}

// edge is one scroll indicator: how many rows lie past that end of the
// window, or a blank line holding its place so the box never jumps.
func (c checklist) edge(arrow string, hidden int) string {
	if hidden <= 0 {
		return ""
	}
	return c.theme.HelpDesc.Render(fmt.Sprintf("  %s %d more", arrow, hidden))
}
