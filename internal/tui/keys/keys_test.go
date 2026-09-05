package keys

import (
	"reflect"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

func TestBindingsMatchKeyPresses(t *testing.T) {
	km := Default()
	cases := []struct {
		msg  tea.KeyPressMsg
		bind key.Binding
		name string
	}{
		{tea.KeyPressMsg{Code: 'q', Text: "q"}, km.Quit, "quit"},
		{tea.KeyPressMsg{Code: tea.KeyEnter}, km.Drill, "drill"},
		{tea.KeyPressMsg{Code: tea.KeyEscape}, km.Back, "back"},
		{tea.KeyPressMsg{Code: tea.KeyTab}, km.Tab, "tab"},
		{tea.KeyPressMsg{Code: 'w', Text: "w"}, km.CycleWindow, "window"},
		{tea.KeyPressMsg{Code: '2', Text: "2"}, km.Charts, "charts"},
		{tea.KeyPressMsg{Code: 'y', Text: "y"}, km.Export, "export"},
		{tea.KeyPressMsg{Code: 'f', Text: "f"}, km.Expand, "expand"},
		{tea.KeyPressMsg{Code: 'h', Text: "h"}, km.Left, "left"},
		{tea.KeyPressMsg{Code: tea.KeyRight}, km.Right, "right arrow"},
		{tea.KeyPressMsg{Code: 'j', Text: "j"}, km.Down, "down"},
		{tea.KeyPressMsg{Code: tea.KeyPgDown}, km.PageDown, "page down"},
		{tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, km.PageDown, "space"},
		{tea.KeyPressMsg{Code: tea.KeyPgUp}, km.PageUp, "page up"},
		{tea.KeyPressMsg{Code: 'd', Text: "d"}, km.HalfDown, "half down"},
		{tea.KeyPressMsg{Code: 'u', Text: "u"}, km.HalfUp, "half up"},
		{tea.KeyPressMsg{Code: 'g', Text: "g"}, km.Top, "top"},
		{tea.KeyPressMsg{Code: tea.KeyHome}, km.Top, "home"},
		// A shifted letter arrives as its text, so "G" must match on its own.
		{tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift}, km.Bottom, "bottom"},
		{tea.KeyPressMsg{Code: tea.KeyEnd}, km.Bottom, "end"},
	}
	for _, c := range cases {
		if !key.Matches(c.msg, c.bind) {
			t.Errorf("%s: %q did not match binding %v", c.name, c.msg.String(), c.bind.Keys())
		}
	}
}

// Every binding in the map shows up in the full help, so a key added to the
// map can never be invisible to ? again.
func TestFullHelpListsEveryBinding(t *testing.T) {
	km := Default()
	shown := map[string]bool{}
	for _, col := range km.FullHelp() {
		if len(col) > 6 {
			t.Errorf("help column of %d rows would grow the footer past its budget", len(col))
		}
		for _, b := range col {
			shown[b.Help().Key] = true
		}
	}
	v := reflect.ValueOf(km)
	for i := 0; i < v.NumField(); i++ {
		b := v.Field(i).Interface().(key.Binding)
		name := v.Type().Field(i).Name
		if name == "Help" {
			continue // the toggle itself sits in the short help only
		}
		if !shown[b.Help().Key] {
			t.Errorf("%s (%s) is missing from the full help", name, b.Help().Key)
		}
	}
}
