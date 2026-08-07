package keys

import (
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
	}
	for _, c := range cases {
		if !key.Matches(c.msg, c.bind) {
			t.Errorf("%s: %q did not match binding %v", c.name, c.msg.String(), c.bind.Keys())
		}
	}
}
