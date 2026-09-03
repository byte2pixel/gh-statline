package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// Page is one routed full-screen view. The app drives every page through
// this interface — theme and size fan-out, key and message dispatch,
// scrolling, rendering — so a new page plugs into those concerns by being
// registered, not by growing another arm on a switch. Typed data wiring
// (SetData and friends) stays on the concrete page types.
type Page interface {
	SetTheme(*theme.Theme)
	SetSize(w, h int)
	// HandleKey runs before the global keymap; true means the page claimed
	// the key and it must not fall through.
	HandleKey(k string) bool
	// Update receives messages no global handler claimed.
	Update(msg tea.Msg) tea.Cmd
	// Scroll moves the page's scrollable region by delta rows (mouse
	// wheel). Pages that only scroll in some states gate that themselves.
	Scroll(delta int)
	View() string
}
