// Package keys defines Statline's keybindings, shared by the input loop and
// the help footer so the two can never drift apart.
package keys

import "charm.land/bubbles/v2/key"

type KeyMap struct {
	Quit        key.Binding
	Help        key.Binding
	Sync        key.Binding
	CycleWindow key.Binding
	Range       key.Binding
	Team        key.Binding
	SortLeft    key.Binding
	SortRight   key.Binding
	FlipSort    key.Binding
	Up          key.Binding
	Down        key.Binding
	Page        key.Binding
	Tab         key.Binding
	Board       key.Binding
	Charts      key.Binding
	Drill       key.Binding
	Back        key.Binding
	Export      key.Binding
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
		Range: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "custom range")),
		Team: key.NewBinding(
			key.WithKeys("t"),
			key.WithHelp("t", "switch team")),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch view")),
		Board: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "leaderboard")),
		Charts: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "charts")),
		Drill: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "drill in")),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back")),
		Export: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy markdown")),
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
		Page: key.NewBinding(
			key.WithKeys("pgup", "pgdown"),
			key.WithHelp("pgup/pgdn", "scroll")),
	}
}

// ShortHelp implements help.KeyMap.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tab, k.Drill, k.CycleWindow, k.Sync, k.Export, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.Page, k.SortLeft, k.SortRight, k.FlipSort},
		{k.Tab, k.Board, k.Charts, k.Drill, k.Back},
		{k.CycleWindow, k.Range, k.Team, k.Sync, k.Export, k.Quit},
	}
}
