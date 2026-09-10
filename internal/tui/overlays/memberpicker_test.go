package overlays

import (
	"maps"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// newMemberPicker offers three members, bob hidden in the config, with the
// cursor asked for on login.
func newMemberPicker(login string) MemberPicker {
	th := theme.New(true)
	return NewMemberPicker(&th, []MemberChoice{
		{Login: "alice"}, {Login: "bob", Hidden: true}, {Login: "carol"},
	}, login)
}

func memberKey(p MemberPicker, key tea.KeyPressMsg) (MemberPicker, tea.Msg) {
	p, cmd := p.Update(key)
	if cmd == nil {
		return p, nil
	}
	return p, cmd()
}

func memberView(p MemberPicker) string { return ansiRE.ReplaceAllString(p.View(), "") }

// The picker opens on the row the table had selected, and the boxes read
// the config: checked is shown, so bob, hidden in the file, starts empty.
func TestMemberPickerOpensOnTheSelectedRow(t *testing.T) {
	v := memberView(newMemberPicker("carol"))
	for _, want := range []string{"  [x] alice", "  [ ] bob", "▸ [x] carol", "Members · 2 of 3 shown"} {
		if !strings.Contains(v, want) {
			t.Errorf("view is missing %q:\n%s", want, v)
		}
	}
	if v := memberView(newMemberPicker("nobody")); !strings.Contains(v, "▸ [x] alice") {
		t.Errorf("an unknown login should leave the cursor on the first row:\n%s", v)
	}
}

// space flips a row and enter reports every member's flag by login, the
// unchanged ones included, so the app has one map to write from.
func TestMemberPickerAppliesHiddenFlags(t *testing.T) {
	p := newMemberPicker("alice")
	p, _ = memberKey(p, keySpace) // hide alice
	p, _ = memberKey(p, keyDown)
	p, _ = memberKey(p, keySpace) // show bob again
	_, msg := memberKey(p, keyEnter)
	chosen, ok := msg.(MembersChosenMsg)
	if !ok {
		t.Fatalf("enter emitted %#v, want MembersChosenMsg", msg)
	}
	want := map[string]bool{"alice": true, "bob": false, "carol": false}
	if !maps.Equal(chosen.Hidden, want) {
		t.Errorf("Hidden = %v, want %v", chosen.Hidden, want)
	}
}

// Hiding everyone would render an empty table that reads like a team with
// nothing to show. The picker refuses, says why, and clears the note on
// the next key.
func TestMemberPickerRefusesToHideEveryone(t *testing.T) {
	p := newMemberPicker("")
	p, _ = memberKey(p, keyNone)
	p, msg := memberKey(p, keyEnter)
	if msg != nil {
		t.Fatalf("enter with nobody shown emitted %#v", msg)
	}
	if !strings.Contains(memberView(p), "keep at least one member shown") {
		t.Fatalf("refusal note missing:\n%s", memberView(p))
	}
	if p, _ = memberKey(p, keyDown); strings.Contains(memberView(p), "keep at least") {
		t.Error("note not cleared by the next key")
	}
}

// esc closes, and so does the key that opened the picker. The shared list
// still gets first refusal: a query is cleared before anything closes.
func TestMemberPickerEscAndMCancel(t *testing.T) {
	keyM := tea.KeyPressMsg{Code: 'm', Text: "m"}
	for _, k := range []tea.KeyPressMsg{keyEsc, keyM} {
		if _, msg := memberKey(newMemberPicker(""), k); msg != (MembersCancelledMsg{}) {
			t.Errorf("%q emitted %#v, want MembersCancelledMsg", k.String(), msg)
		}
	}

	p := newMemberPicker("")
	p, _ = memberKey(p, keySlash)
	for _, r := range "bo" {
		p, _ = memberKey(p, tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if v := memberView(p); !strings.Contains(v, "bob") || strings.Contains(v, "alice") {
		t.Errorf("query did not narrow to bob:\n%s", v)
	}
	p, msg := memberKey(p, keyEsc)
	if msg != nil {
		t.Fatalf("esc while typing emitted %#v, want it to clear the query", msg)
	}
	if _, msg = memberKey(p, keyEsc); msg != (MembersCancelledMsg{}) {
		t.Errorf("second esc emitted %#v, want MembersCancelledMsg", msg)
	}
}
