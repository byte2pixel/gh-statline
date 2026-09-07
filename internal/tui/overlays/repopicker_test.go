package overlays

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

var (
	keySpace     = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyDown      = tea.KeyPressMsg{Code: 'j', Text: "j"}
	keyUp        = tea.KeyPressMsg{Code: 'k', Text: "k"}
	keySlash     = tea.KeyPressMsg{Code: '/', Text: "/"}
	keyAll       = tea.KeyPressMsg{Code: 'a', Text: "a"}
	keyNone      = tea.KeyPressMsg{Code: 'n', Text: "n"}
	keyBackspace = tea.KeyPressMsg{Code: tea.KeyBackspace}
	keyShiftR    = tea.KeyPressMsg{Code: 'R', Text: "R", Mod: tea.ModShift}
)

// newRepoPicker offers three repos; selected is the filter already in force.
func newRepoPicker(selected ...int64) RepoPicker {
	th := theme.New(true)
	return NewRepoPicker(&th, []RepoChoice{
		{ID: 1, Name: "acme/api"}, {ID: 2, Name: "acme/web"}, {ID: 3, Name: "acme/infra"},
	}, selected)
}

// bigPicker offers n repos named repo-000 upwards, everything checked, in
// a modal capped at height rows. Zero height lifts the cap.
func bigPicker(n, height int) RepoPicker {
	th := theme.New(true)
	repos := make([]RepoChoice, n)
	for i := range repos {
		repos[i] = RepoChoice{ID: int64(i + 1), Name: fmt.Sprintf("acme/repo-%03d", i)}
	}
	p := NewRepoPicker(&th, repos, nil)
	p.SetHeight(height)
	return p
}

// pickerKey delivers one key and returns the picker plus whatever message
// its command produced, or nil when it produced none.
func pickerKey(p RepoPicker, key tea.KeyPressMsg) (RepoPicker, tea.Msg) {
	p, cmd := p.Update(key)
	if cmd == nil {
		return p, nil
	}
	return p, cmd()
}

// pressN sends the same key n times.
func pressN(p RepoPicker, key tea.KeyPressMsg, n int) RepoPicker {
	for i := 0; i < n; i++ {
		p, _ = pickerKey(p, key)
	}
	return p
}

// typeQuery types s one key at a time, the way a user would.
func typeQuery(p RepoPicker, s string) RepoPicker {
	for _, r := range s {
		p, _ = pickerKey(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return p
}

func pickerView(p RepoPicker) string { return ansiRE.ReplaceAllString(p.View(), "") }

var moreRE = regexp.MustCompile(`([↑↓]) (\d+) more`)

// hidden reads the scroll indicators: rows above and below the window.
func hidden(t *testing.T, view string) (above, below int) {
	t.Helper()
	for _, m := range moreRE.FindAllStringSubmatch(view, -1) {
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

// No filter in force means every repo is in scope, and the picker has to
// say so: opening on an empty list would read as "nothing selected".
func TestRepoPickerStartsFullyCheckedWithoutAFilter(t *testing.T) {
	v := pickerView(newRepoPicker())
	if strings.Count(v, "[x]") != 3 || strings.Contains(v, "[ ]") {
		t.Errorf("want three checked boxes and no empty ones:\n%s", v)
	}
}

func TestRepoPickerRestoresTheCurrentFilter(t *testing.T) {
	v := pickerView(newRepoPicker(2))
	for _, want := range []string{"[ ] acme/api", "[x] acme/web", "[ ] acme/infra"} {
		if !strings.Contains(v, want) {
			t.Errorf("view is missing %q:\n%s", want, v)
		}
	}
}

// Unchecking one and applying names the rest, in config order.
func TestRepoPickerAppliesTheCheckedRepos(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keyDown)  // onto web
	p, _ = pickerKey(p, keySpace) // uncheck it
	_, msg := pickerKey(p, keyEnter)
	chosen, ok := msg.(ReposChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %#v, want ReposChosenMsg", msg)
	}
	if !slices.Equal(chosen.IDs, []int64{1, 3}) {
		t.Errorf("IDs = %v, want [1 3]", chosen.IDs)
	}
}

// Every repo checked is no filter at all, and the app hears nil rather than
// a list that happens to be complete, so the header shows no scope.
func TestRepoPickerAllCheckedMeansNoFilter(t *testing.T) {
	p := newRepoPicker(2)
	p, _ = pickerKey(p, keyAll)
	_, msg := pickerKey(p, keyEnter)
	chosen, ok := msg.(ReposChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %#v, want ReposChosenMsg", msg)
	}
	if chosen.IDs != nil {
		t.Errorf("IDs = %#v with everything checked, want nil", chosen.IDs)
	}
}

// Nothing checked would render an empty dashboard that looks like a quiet
// month. The picker refuses, says why, and clears the note on the next key.
func TestRepoPickerRefusesAnEmptySelection(t *testing.T) {
	p := newRepoPicker(1)
	p, _ = pickerKey(p, keySpace) // uncheck api, the only one checked
	p, msg := pickerKey(p, keyEnter)
	if msg != nil {
		t.Fatalf("enter with nothing checked emitted %#v", msg)
	}
	if !strings.Contains(pickerView(p), "pick at least one repo") {
		t.Fatalf("refusal note missing:\n%s", pickerView(p))
	}
	if p, _ = pickerKey(p, keyDown); strings.Contains(pickerView(p), "pick at least") {
		t.Error("note not cleared by the next key")
	}
}

// esc cancels, and so does the key that opened the picker, the way t
// closes the team switcher.
func TestRepoPickerEscAndRCancel(t *testing.T) {
	for _, k := range []tea.KeyPressMsg{keyEsc, keyShiftR} {
		if _, msg := pickerKey(newRepoPicker(), k); msg != (ReposCancelledMsg{}) {
			t.Errorf("%q emitted %#v, want ReposCancelledMsg", k.String(), msg)
		}
	}
}

func TestRepoPickerCursorStaysInRange(t *testing.T) {
	p := newRepoPicker()
	if p, _ = pickerKey(p, keyUp); p.cursor != 0 {
		t.Errorf("k at the top moved the cursor to %d", p.cursor)
	}
	if p = pressN(p, keyDown, 5); p.cursor != 2 {
		t.Errorf("cursor = %d after running off the end, want 2", p.cursor)
	}
}

// Repo names come from the config file. Validate rejects control characters
// at load, but the modal must not trust its input either (gh #43).
func TestRepoPickerSanitizesNames(t *testing.T) {
	th := theme.New(true)
	p := NewRepoPicker(&th, []RepoChoice{{ID: 1, Name: "acme/evil\x1b]0;pwned\aname"}}, nil)
	v := p.View()
	if strings.Contains(v, "pwned") || strings.Contains(v, "\a") || strings.Contains(v, "\x1b]") {
		t.Errorf("hostile name leaked into the render:\n%q", v)
	}
	if !strings.Contains(v, "acme/evilname") {
		t.Errorf("printable part of the name missing:\n%s", v)
	}
}

// The title counts the selection. With a hundred repos most checkboxes
// are off screen, so the count is how you know what you have.
func TestRepoPickerTitleCountsTheSelection(t *testing.T) {
	p := newRepoPicker(2)
	want := func(p RepoPicker, count string) {
		t.Helper()
		if v := pickerView(p); !strings.Contains(v, "Filter repos · "+count) {
			t.Errorf("title does not read %q:\n%s", count, v)
		}
	}
	want(p, "1 of 3")
	p, _ = pickerKey(p, keyAll)
	want(p, "3 of 3")
	p, _ = pickerKey(p, keyNone)
	want(p, "0 of 3")
}

// The modal fits the height it is given, whatever the list holds and
// whichever extra line is showing, since the app centres it in a fixed
// area and anything taller pushes the status bar off the screen.
func TestRepoPickerFitsTheHeightItIsGiven(t *testing.T) {
	for _, n := range []int{3, 100} {
		for _, h := range []int{14, 20, 40} {
			p := bigPicker(n, h)
			check := func(state string, p RepoPicker) {
				t.Helper()
				if got := lipgloss.Height(p.View()); got > h {
					t.Errorf("%d repos at height %d, %s: modal is %d rows", n, h, state, got)
				}
			}
			check("fresh", p)
			p, _ = pickerKey(p, keySlash)
			check("typing a query", p)
			check("query with matches", typeQuery(p, "repo-00"))
			check("query with no matches", typeQuery(p, "zzz"))
			p = pressN(p, keyDown, n)
			check("cursor at the end", p)
		}
	}
}

// Without a height the picker shows everything, which is what the unit
// tests above rely on and what a caller that never sizes it gets.
func TestRepoPickerWithoutAHeightShowsEverything(t *testing.T) {
	v := pickerView(bigPicker(100, 0))
	if strings.Count(v, "[x]") != 100 || strings.Contains(v, "more") {
		t.Errorf("want 100 rows and no scroll indicators:\n%s", v)
	}
}

// The window follows the cursor, and the indicators account for every row
// that is not on screen.
func TestRepoPickerScrollsToKeepTheCursorVisible(t *testing.T) {
	p := bigPicker(100, 20)
	v := pickerView(p)
	if above, below := hidden(t, v); above != 0 || below == 0 {
		t.Fatalf("fresh picker hides %d above and %d below, want 0 above and some below:\n%s", above, below, v)
	}
	if !strings.Contains(v, "▸ [x] acme/repo-000") {
		t.Errorf("cursor not on the first repo:\n%s", v)
	}

	p = pressN(p, keyDown, 60)
	v = pickerView(p)
	if !strings.Contains(v, "▸ [x] acme/repo-060") {
		t.Errorf("cursor row scrolled off screen:\n%s", v)
	}
	above, below := hidden(t, v)
	if shown := strings.Count(v, "[x]"); above+shown+below != 100 {
		t.Errorf("%d above + %d shown + %d below != 100:\n%s", above, shown, below, v)
	}
	if above == 0 || below == 0 {
		t.Errorf("mid-list should hide rows at both ends, got %d above and %d below", above, below)
	}

	p = pressN(p, keyDown, 100)
	v = pickerView(p)
	if !strings.Contains(v, "▸ [x] acme/repo-099") {
		t.Errorf("cursor did not reach the last repo:\n%s", v)
	}
	if _, below := hidden(t, v); below != 0 {
		t.Errorf("at the end, %d rows are still hidden below", below)
	}

	p = pressN(p, keyUp, 200)
	if above, _ := hidden(t, pickerView(p)); above != 0 {
		t.Errorf("back at the top, %d rows are still hidden above", above)
	}
}

// A resize while the picker is open keeps the cursor on screen.
func TestRepoPickerResizeKeepsTheCursorOnScreen(t *testing.T) {
	p := bigPicker(100, 40)
	p = pressN(p, keyDown, 25)
	p.SetHeight(14)
	v := pickerView(p)
	if !strings.Contains(v, "▸ [x] acme/repo-025") {
		t.Errorf("cursor row lost after shrinking:\n%s", v)
	}
	if got := lipgloss.Height(p.View()); got > 14 {
		t.Errorf("modal is %d rows after shrinking to 14", got)
	}
}

// / narrows the list as you type, enter keeps the narrowed list and hands
// the keys back to the picker, and the selection applies against it.
func TestRepoPickerFilterNarrowsTheList(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p = typeQuery(p, "we")
	v := pickerView(p)
	if !strings.Contains(v, "acme/web") || strings.Contains(v, "acme/api") || strings.Contains(v, "acme/infra") {
		t.Errorf("query did not narrow to web:\n%s", v)
	}
	if !strings.Contains(v, "/we▌") || !strings.Contains(v, "1 of 3 match") {
		t.Errorf("query line missing the caret or the match count:\n%s", v)
	}

	p, msg := pickerKey(p, keyEnter)
	if msg != nil {
		t.Fatalf("enter while typing emitted %#v, want it to accept the query", msg)
	}
	v = pickerView(p)
	if strings.Contains(v, "▌") || !strings.Contains(v, "/we") {
		t.Errorf("accepted query should drop the caret and keep the text:\n%s", v)
	}

	p, _ = pickerKey(p, keySpace) // web is the only row, so this unchecks it
	_, msg = pickerKey(p, keyEnter)
	chosen, ok := msg.(ReposChosenMsg)
	if !ok || !slices.Equal(chosen.IDs, []int64{1, 3}) {
		t.Errorf("applied %#v, want api and infra", msg)
	}
}

// While a query is being typed every printable key is text, so the
// picker's own keys cannot fire under the user's fingers.
func TestRepoPickerTypingDoesNotTriggerPickerKeys(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	for _, k := range []tea.KeyPressMsg{keyShiftR, keyAll, keyNone, keySpace, keyDown} {
		var msg tea.Msg
		if p, msg = pickerKey(p, k); msg != nil {
			t.Fatalf("%q while typing emitted %#v", k.String(), msg)
		}
	}
	v := pickerView(p)
	if !strings.Contains(v, "/Ran j▌") {
		t.Errorf("keys did not land in the query:\n%s", v)
	}
	if !strings.Contains(v, "Filter repos · 3 of 3") {
		t.Errorf("a or n changed the selection while typing:\n%s", v)
	}
}

func TestRepoPickerBackspaceEditsTheQuery(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p = typeQuery(p, "web")
	p = pressN(p, keyBackspace, 2)
	v := pickerView(p)
	if !strings.Contains(v, "/w▌") || !strings.Contains(v, "1 of 3 match") {
		t.Errorf("query should read w and still match web alone:\n%s", v)
	}
	p = pressN(p, keyBackspace, 5) // past empty is harmless
	if v := pickerView(p); !strings.Contains(v, "/▌") || !strings.Contains(v, "type to narrow") {
		t.Errorf("empty query should invite typing:\n%s", v)
	}
}

// esc peels back one layer at a time: it leaves typing and clears the
// query first, and only closes the picker when there is no query left.
func TestRepoPickerEscClearsTheQueryBeforeCancelling(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p = typeQuery(p, "zzz")
	if v := pickerView(p); !strings.Contains(v, "no repos match") {
		t.Fatalf("a query with no matches should say so:\n%s", v)
	}
	p, msg := pickerKey(p, keyEsc)
	if msg != nil {
		t.Fatalf("esc while typing emitted %#v, want it to clear the query", msg)
	}
	// The query line is the only place "match" appears; repo names and the
	// footer both carry slashes, so a slash proves nothing.
	if v := pickerView(p); strings.Count(v, "[x]") != 3 || strings.Contains(v, "match") {
		t.Errorf("query not cleared:\n%s", v)
	}
	if _, msg = pickerKey(p, keyEsc); msg != (ReposCancelledMsg{}) {
		t.Errorf("second esc emitted %#v, want ReposCancelledMsg", msg)
	}

	// The same two steps after the query was accepted with enter.
	p = newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p, _ = pickerKey(typeQuery(p, "api"), keyEnter)
	p, msg = pickerKey(p, keyEsc)
	if msg != nil || strings.Contains(pickerView(p), "match") || strings.Count(pickerView(p), "[x]") != 3 {
		t.Errorf("esc on an accepted query emitted %#v and left the view:\n%s", msg, pickerView(p))
	}
	if _, msg = pickerKey(p, keyEsc); msg != (ReposCancelledMsg{}) {
		t.Errorf("second esc emitted %#v, want ReposCancelledMsg", msg)
	}
}

// a and n act on the rows the query shows, which is how a hundred-repo
// team gets to three: n, then / and a name, then a.
func TestRepoPickerAllAndNoneApplyToTheMatches(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keyNone)
	if v := pickerView(p); !strings.Contains(v, "0 of 3") {
		t.Fatalf("n did not clear the selection:\n%s", v)
	}
	p, _ = pickerKey(p, keySlash)
	p, _ = pickerKey(typeQuery(p, "web"), keyEnter)
	p, _ = pickerKey(p, keyAll)
	_, msg := pickerKey(p, keyEnter)
	if chosen, ok := msg.(ReposChosenMsg); !ok || !slices.Equal(chosen.IDs, []int64{2}) {
		t.Errorf("applied %#v, want web alone", msg)
	}

	// And the other way: n under a query unchecks only what it shows.
	p = newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p, _ = pickerKey(typeQuery(p, "web"), keyEnter)
	p, _ = pickerKey(p, keyNone)
	_, msg = pickerKey(p, keyEnter)
	if chosen, ok := msg.(ReposChosenMsg); !ok || !slices.Equal(chosen.IDs, []int64{1, 3}) {
		t.Errorf("applied %#v, want api and infra", msg)
	}
}

// A new query puts the cursor on its first match: the row it sat on may
// not be in the list any more.
func TestRepoPickerQueryResetsTheCursor(t *testing.T) {
	p := bigPicker(100, 20)
	p = pressN(p, keyDown, 60)
	p, _ = pickerKey(p, keySlash)
	p = typeQuery(p, "-01")
	v := pickerView(p)
	if !strings.Contains(v, "▸ [x] acme/repo-010") {
		t.Errorf("cursor should sit on the first match:\n%s", v)
	}
	if !strings.Contains(v, "10 of 100 match") {
		t.Errorf("repo-010 to repo-019 should match:\n%s", v)
	}
}

// An empty match list takes every key without effect and without panic.
func TestRepoPickerNoMatchesIsSafe(t *testing.T) {
	p := newRepoPicker()
	p, _ = pickerKey(p, keySlash)
	p, _ = pickerKey(typeQuery(p, "zzz"), keyEnter)
	for _, k := range []tea.KeyPressMsg{keySpace, keyAll, keyNone, keyDown, keyUp} {
		var msg tea.Msg
		if p, msg = pickerKey(p, k); msg != nil {
			t.Errorf("%q with no matches emitted %#v", k.String(), msg)
		}
	}
	if v := pickerView(p); !strings.Contains(v, "3 of 3") {
		t.Errorf("keys with no matches changed the selection:\n%s", v)
	}
}
