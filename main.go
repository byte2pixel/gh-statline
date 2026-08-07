package main

import "github.com/byte2pixel/gh-statline/internal/cmd"

// version is injected at release time via -ldflags "-X main.version=<tag>"
// by the cli/gh-extension-precompile action.
var version = ""

func main() {
	cmd.Execute(version)
}
