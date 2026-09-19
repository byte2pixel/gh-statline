package components

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

var (
	keyEnter = tea.KeyPressMsg{Code: tea.KeyEnter}
	keyEsc   = tea.KeyPressMsg{Code: tea.KeyEscape}
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyDown  = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keySlash = tea.KeyPressMsg{Code: '/', Text: "/"}
	keyAll   = tea.KeyPressMsg{Code: 'a', Text: "a"}
	keyNone  = tea.KeyPressMsg{Code: 'n', Text: "n"}
)

// newList offers n names, name-000 upwards, everything checked, in a box
// capped at height rows. Zero height lifts the cap.
func newList(n, height int) Checklist {
	th := theme.New(true)
	names := make([]string, n)
	checked := make([]bool, n)
	for i := range names {
		names[i] = fmt.Sprintf("name-%03d", i)
		checked[i] = true
	}
	c := NewChecklist(&th, names, checked, "nothing matches")
	c.SetHeight(height)
	return c
}

// people is the three-name list the query tests narrow.
func people() Checklist {
	th := theme.New(true)
	return NewChecklist(&th, []string{"alice", "bob", "carol"}, []bool{true, true, true}, "nobody matches")
}

func press(c Checklist, keys ...tea.KeyPressMsg) (Checklist, bool) {
	owned := false
	for _, k := range keys {
		owned = c.Handle(k)
	}
	return c, owned
}

func typeText(c Checklist, s string) Checklist {
	for _, r := range s {
		c.Handle(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return c
}

func view(c Checklist) string { return ansi.Strip(c.View("List", "enter go")) }

var moreRE = regexp.MustCompile(`([↑↓]) (\d+) more`)

// hidden reads the scroll indicators: rows above and below the window.
func hidden(t *testing.T, v string) (above, below int) {
	t.Helper()
	for _, m := range moreRE.FindAllStringSubmatch(v, -1) {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			t.Fatal(err)
		}
		if m[1] == "↑" {
			above = n
		} else {
			below = n
		}
	}
	return above, below
}

// The list scrolls inside the height it is given: the edges say how many
// rows lie past the window, and the cursor row never leaves the screen.
func TestChecklistScrollsWithinHeight(t *testing.T) {
	c := newList(12, 12)
	v := view(c)
	if above, below := hidden(t, v); above != 0 || below == 0 {
		t.Fatalf("fresh list hides %d above and %d below, want 0 above and some below:\n%s", above, below, v)
	}
	if !strings.Contains(v, "▸ [x] name-000") {
		t.Errorf("cursor not on the first row:\n%s", v)
	}
	for range 10 {
		c.Handle(keyDown)
	}
	v = view(c)
	if !strings.Contains(v, "▸ [x] name-010") {
		t.Errorf("cursor row scrolled off screen after ten downs:\n%s", v)
	}
	above, below := hidden(t, v)
	if shown := strings.Count(v, "[x]"); above+shown+below != 12 {
		t.Errorf("%d above + %d shown + %d below != 12:\n%s", above, shown, below, v)
	}
	if above == 0 {
		t.Errorf("rows above the window are not counted:\n%s", v)
	}
}

// / narrows the list as you type and enter keeps the narrowed list. esc
// then clears the query and is owned by the list; with nothing left to
// clear the next esc is handed back to the caller.
func TestChecklistQueryNarrowsThenEscClearsFirst(t *testing.T) {
	c := people()
	c, owned := press(c, keySlash)
	if !owned {
		t.Fatal("/ was not owned by the list")
	}
	c = typeText(c, "bo")
	c, _ = press(c, keyEnter)
	v := view(c)
	if !strings.Contains(v, "bob") || strings.Contains(v, "alice") || strings.Contains(v, "carol") {
		t.Errorf("query did not narrow to bob:\n%s", v)
	}
	if !strings.Contains(v, "1 of 3 match") {
		t.Errorf("query line lacks the match count:\n%s", v)
	}
	c, owned = press(c, keyEsc)
	if !owned {
		t.Error("esc with a query in force was not owned by the list")
	}
	if v := view(c); strings.Contains(v, "match") || strings.Count(v, "[x]") != 3 {
		t.Errorf("esc did not clear the query:\n%s", v)
	}
	if _, owned = press(c, keyEsc); owned {
		t.Error("esc with no query was owned by the list; the caller should get it")
	}
}

// a and n act on the rows the query shows, so the rest keep their boxes.
func TestChecklistAllAndNoneHonourTheQuery(t *testing.T) {
	c := people()
	c, _ = press(c, keySlash)
	c = typeText(c, "a") // alice and carol
	c, _ = press(c, keyEnter, keyNone)
	if got := []bool{c.Checked(0), c.Checked(1), c.Checked(2)}; got[0] || !got[1] || got[2] {
		t.Errorf("n under a query left checked = %v, want only bob", got)
	}
	c, _ = press(c, keyEsc, keyNone, keySlash)
	c = typeText(c, "bob")
	c, _ = press(c, keyEnter, keyAll)
	if got := []bool{c.Checked(0), c.Checked(1), c.Checked(2)}; got[0] || !got[1] || got[2] {
		t.Errorf("a under a query left checked = %v, want only bob", got)
	}
}

// Count is the checked rows whatever the query shows, Len the rows there
// are, and space flips the row under the cursor.
func TestChecklistCountAndChecked(t *testing.T) {
	c := people()
	if c.Count() != 3 || c.Len() != 3 {
		t.Fatalf("fresh list counts %d of %d, want 3 of 3", c.Count(), c.Len())
	}
	c, _ = press(c, keyDown, keySpace)
	if c.Count() != 2 || c.Checked(1) || !c.Checked(0) {
		t.Errorf("space on bob left count %d and bob %v", c.Count(), c.Checked(1))
	}
	c, _ = press(c, keySlash)
	c = typeText(c, "zzz")
	if c.Count() != 2 || c.Len() != 3 {
		t.Errorf("a query with no matches changed the counts to %d of %d", c.Count(), c.Len())
	}
	if v := view(c); !strings.Contains(v, "nobody matches") {
		t.Errorf("empty row missing:\n%s", v)
	}
}

// The wrappers place the cursor and write the refusal note through the
// accessors; the note shows in the footer until the next key.
func TestChecklistCursorAndNote(t *testing.T) {
	c := newList(30, 14)
	c.SetCursor(25)
	if c.Cursor() != 25 {
		t.Fatalf("Cursor() = %d after SetCursor(25)", c.Cursor())
	}
	if v := view(c); !strings.Contains(v, "▸ [x] name-025") {
		t.Errorf("SetCursor did not scroll the row into view:\n%s", v)
	}
	c.SetCursor(99)
	if c.Cursor() != 29 {
		t.Errorf("SetCursor past the end left the cursor at %d, want 29", c.Cursor())
	}
	c.SetNote("keep one")
	if v := view(c); !strings.Contains(v, "keep one") || strings.Contains(v, "enter go") {
		t.Errorf("note should replace the second footer line:\n%s", v)
	}
	c, _ = press(c, keyDown)
	if v := view(c); strings.Contains(v, "keep one") || !strings.Contains(v, "enter go") {
		t.Errorf("note not cleared by the next key:\n%s", v)
	}
}
