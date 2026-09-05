package cmd

import (
	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/gh"
)

// The two calls that would otherwise make the command layer untestable:
// running a full-screen Bubble Tea program and building a real GitHub
// client. Variables so tests swap in a headless runner and a fake Doer.
var (
	runProgram = func(m tea.Model) (tea.Model, error) { return tea.NewProgram(m).Run() }
	newClient  = gh.NewClient
)
