// Package text guards Statline's presentation boundaries. PR titles are
// written by anyone who can open a PR against a watched repo, so anything
// that reaches the terminal or the clipboard is sanitized here first — the
// cache keeps the original bytes, consistent with bot exclusion being
// read-time policy. The width helpers exist because byte- and rune-based
// truncation split UTF-8 sequences and miscount wide characters.
package text

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// controlRune spots C0 controls, DEL, and C1 controls (U+0080–U+009F) — the
// characters that can introduce escape sequences or move the cursor.
func controlRune(r rune) bool {
	return r < 0x20 || r == 0x7f || (0x80 <= r && r <= 0x9f)
}

// HasControls reports whether Sanitize would alter s. Config validation uses
// it to reject hand-edited values outright instead of silently rewriting.
func HasControls(s string) bool { return strings.ContainsFunc(s, controlRune) }

// Sanitize makes one line of untrusted text safe to render or export:
// well-formed escape sequences (CSI, OSC, DCS, …) disappear whole — payload
// included — tabs and newlines flatten to spaces, and any remaining control
// characters are dropped. Clean strings return unchanged without allocating.
func Sanitize(s string) string {
	if !HasControls(s) {
		return s
	}
	// ansi.Strip consumes escape sequences but passes bare control bytes
	// through (its ExecuteAction); the Map pass catches those.
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			return ' '
		case controlRune(r):
			return -1
		}
		return r
	}, ansi.Strip(s))
}

// Width returns the display width of s in terminal cells; CJK and emoji
// occupy 2 where len([]rune) says 1.
func Width(s string) int { return ansi.StringWidth(s) }

// Truncate caps s at w cells, ending in … when it has to cut. Never splits
// a UTF-8 sequence or a wide character; a fitting s comes back unchanged.
func Truncate(s string, w int) string { return ansi.Truncate(s, w, "…") }

// Clip caps s at w cells with no ellipsis.
func Clip(s string, w int) string { return ansi.Truncate(s, w, "") }
