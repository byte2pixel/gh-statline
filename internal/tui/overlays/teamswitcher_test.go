package overlays

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

func press(ts TeamSwitcher, k string) (TeamSwitcher, tea.Msg) {
	var key tea.KeyPressMsg
	switch k {
	case "esc":
		key = tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		key = tea.KeyPressMsg{Code: tea.KeyEnter}
	default:
		key = tea.KeyPressMsg{Code: rune(k[0]), Text: k}
	}
	ts, cmd := ts.Update(key)
	if cmd == nil {
		return ts, nil
	}
	return ts, cmd()
}

func newSwitcher(names ...string) TeamSwitcher {
	th := theme.New(true)
	return NewTeamSwitcher(&th, names, names[0])
}

func TestDeleteArmsAndConfirms(t *testing.T) {
	ts := newSwitcher("a", "b")
	ts, msg := press(ts, "d")
	if msg != nil {
		t.Fatalf("d alone emitted %#v", msg)
	}
	if !strings.Contains(ts.View(), "delete a? y/n") {
		t.Fatalf("armed footer missing:\n%s", ts.View())
	}
	if _, msg = press(ts, "y"); msg != (TeamDeleteMsg{Name: "a"}) {
		t.Fatalf("y emitted %#v, want TeamDeleteMsg{a}", msg)
	}
}

func TestDeleteTargetsCursorNotActive(t *testing.T) {
	ts := newSwitcher("a", "b")
	ts, _ = press(ts, "j")
	ts, _ = press(ts, "d")
	if _, msg := press(ts, "y"); msg != (TeamDeleteMsg{Name: "b"}) {
		t.Fatalf("got %#v, want TeamDeleteMsg{b}", msg)
	}
}

func TestArmedDisarmsAndSwallowsKey(t *testing.T) {
	ts := newSwitcher("a", "b")
	ts, _ = press(ts, "d")
	ts, msg := press(ts, "j")
	if msg != nil {
		t.Fatalf("j while armed emitted %#v", msg)
	}
	if ts.cursor != 0 {
		t.Fatalf("j while armed moved cursor to %d", ts.cursor)
	}
	if strings.Contains(ts.View(), "y/n") {
		t.Fatal("still armed after non-y key")
	}
	if _, msg = press(ts, "esc"); msg != (TeamCancelledMsg{}) {
		t.Fatalf("esc after disarm emitted %#v, want TeamCancelledMsg", msg)
	}
}

func TestArmedEscOnlyDisarms(t *testing.T) {
	ts := newSwitcher("a", "b")
	ts, _ = press(ts, "d")
	ts, msg := press(ts, "esc")
	if msg != nil {
		t.Fatalf("esc while armed emitted %#v", msg)
	}
	if _, msg = press(ts, "esc"); msg != (TeamCancelledMsg{}) {
		t.Fatalf("second esc emitted %#v, want TeamCancelledMsg", msg)
	}
}

// Team names come from the config file; even though Validate rejects control
// characters at load, the modal must not trust its input (gh #43).
func TestViewSanitizesNames(t *testing.T) {
	ts := newSwitcher("evil\x1b]0;pwned\aname", "b")
	list := ts.View()
	ts, _ = press(ts, "d") // armed footer echoes the name too
	for _, view := range []string{list, ts.View()} {
		if strings.Contains(view, "pwned") || strings.Contains(view, "\a") || strings.Contains(view, "\x1b]") {
			t.Errorf("hostile name leaked into the render:\n%q", view)
		}
		if !strings.Contains(view, "evilname") {
			t.Errorf("printable part of the name missing:\n%s", view)
		}
	}
}

func TestDeleteBlockedForLastTeam(t *testing.T) {
	ts := newSwitcher("only")
	ts, msg := press(ts, "d")
	if msg != nil {
		t.Fatalf("d on last team emitted %#v", msg)
	}
	if !strings.Contains(ts.View(), "can't delete the only team") {
		t.Fatalf("blocked note missing:\n%s", ts.View())
	}
	if ts, _ = press(ts, "j"); strings.Contains(ts.View(), "can't delete") {
		t.Fatal("note not cleared by next key")
	}
}
