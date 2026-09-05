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
	// Left and Right change the sort column on the team page and move the
	// focus or pan the fullscreen body on the card grids.
	Left      key.Binding
	Right     key.Binding
	FlipSort  key.Binding
	Up        key.Binding
	Down      key.Binding
	PageUp    key.Binding
	PageDown  key.Binding
	HalfUp    key.Binding
	HalfDown  key.Binding
	Top       key.Binding
	Bottom    key.Binding
	Tab       key.Binding
	TeamStats key.Binding
	Charts    key.Binding
	Trends    key.Binding
	Drill     key.Binding
	Expand    key.Binding
	Back      key.Binding
	Export    key.Binding
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
		TeamStats: key.NewBinding(
			key.WithKeys("1"),
			key.WithHelp("1", "team stats")),
		Charts: key.NewBinding(
			key.WithKeys("2"),
			key.WithHelp("2", "charts")),
		Trends: key.NewBinding(
			key.WithKeys("3"),
			key.WithHelp("3", "trends")),
		Drill: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "drill in")),
		Expand: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "expand card")),
		Back: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "back")),
		Export: key.NewBinding(
			key.WithKeys("y"),
			key.WithHelp("y", "copy markdown")),
		Left: key.NewBinding(
			key.WithKeys("h", "left"),
			key.WithHelp("←/h", "sort col")),
		Right: key.NewBinding(
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
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "page up")),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown", "space"),
			key.WithHelp("pgdn/space", "page down")),
		HalfUp: key.NewBinding(
			key.WithKeys("u"),
			key.WithHelp("u", "half page up")),
		HalfDown: key.NewBinding(
			key.WithKeys("d"),
			key.WithHelp("d", "half page down")),
		Top: key.NewBinding(
			key.WithKeys("g", "home"),
			key.WithHelp("g/home", "top")),
		Bottom: key.NewBinding(
			key.WithKeys("G", "shift+g", "end"),
			key.WithHelp("G/end", "bottom")),
	}
}

// ShortHelp implements help.KeyMap.
func (k KeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Tab, k.Drill, k.CycleWindow, k.Sync, k.Export, k.Help, k.Quit}
}

// FullHelp implements help.KeyMap. Columns stay at six rows so the footer
// keeps its height; the help model drops trailing columns that do not fit
// the width.
func (k KeyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{k.Up, k.Down, k.PageUp, k.PageDown, k.HalfUp, k.HalfDown},
		{k.Top, k.Bottom, k.Left, k.Right, k.FlipSort, k.Expand},
		{k.Tab, k.TeamStats, k.Charts, k.Trends, k.Drill, k.Back},
		{k.CycleWindow, k.Range, k.Team, k.Sync, k.Export, k.Quit},
	}
}
