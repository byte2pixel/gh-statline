// Package keys defines Statline's keybindings, shared by the input loop and
// the help footer so the two can never drift apart.
package keys

import "charm.land/bubbles/v2/key"

type KeyMap struct {
	Quit        key.Binding
	Help        key.Binding
	Sync        key.Binding
	CycleWindow key.Binding
	SortLeft    key.Binding
	SortRight   key.Binding
	FlipSort    key.Binding
	Up          key.Binding
	Down        key.Binding
}

func Default() KeyMap {
	return KeyMap{
		Quit: key.NewBinding(
			key.WithKeys("q", "ctrl+c"),
			key.WithHelp("q", "quit")),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help")),
		Sync: key.NewBinding(
			key.WithKeys("s"),
			key.WithHelp("s", "sync")),
		CycleWindow: key.NewBinding(
			key.WithKeys("w"),
			key.WithHelp("w", "time window")),
		SortLeft: key.NewBinding(
			key.WithKeys("h", "left"),
			key.WithHelp("←/h", "sort col")),
		SortRight: key.NewBinding(
			key.WithKeys("l", "right"),
			key.WithHelp("→/l", "sort col")),
		FlipSort: key.NewBinding(
			key.WithKeys("-"),
			key.WithHelp("-", "flip sort")),
		Up: key.NewBinding(
			key.WithKeys("k", "up"),
			key.WithHelp("↑/k", "up")),
		Down: key.NewBinding(
			key.WithKeys("j", "down"),
			key.WithHelp("↓/j", "down")),
	}
}

// ShortHelp implements help.KeyMap.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.CycleWindow, k.SortLeft, k.SortRight, k.FlipSort, k.Sync, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.SortLeft, k.SortRight, k.FlipSort},
		{k.CycleWindow, k.Sync, k.Help, k.Quit},
	}
}
