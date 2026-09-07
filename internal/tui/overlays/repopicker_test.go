package overlays

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

var (
	keySpace = tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	keyDown  = tea.KeyPressMsg{Code: 'j', Text: "j"}
)

// newRepoPicker offers three repos; selected is the filter already in force.
func newRepoPicker(selected ...int64) RepoPicker {
	th := theme.New(true)
	return NewRepoPicker(&th, []RepoChoice{
		{ID: 1, Name: "acme/api"}, {ID: 2, Name: "acme/web"}, {ID: 3, Name: "acme/infra"},
	}, selected)
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

func pickerView(p RepoPicker) string { return ansiRE.ReplaceAllString(p.View(), "") }

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
	p, _ = pickerKey(p, tea.KeyPressMsg{Code: 'a', Text: "a"})
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
	for _, k := range []tea.KeyPressMsg{keyEsc, {Code: 'R', Text: "R", Mod: tea.ModShift}} {
		if _, msg := pickerKey(newRepoPicker(), k); msg != (ReposCancelledMsg{}) {
			t.Errorf("%q emitted %#v, want ReposCancelledMsg", k.String(), msg)
		}
	}
}

func TestRepoPickerCursorStaysInRange(t *testing.T) {
	p := newRepoPicker()
	if p, _ = pickerKey(p, tea.KeyPressMsg{Code: 'k', Text: "k"}); p.cursor != 0 {
		t.Errorf("k at the top moved the cursor to %d", p.cursor)
	}
	for i := 0; i < 5; i++ {
		p, _ = pickerKey(p, keyDown)
	}
	if p.cursor != 2 {
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
