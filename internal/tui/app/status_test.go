package app

import (
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// The status bar used to byte-slice its text on narrow windows, shearing the
// leading ✓ (3 bytes, 1 cell) into mojibake (gh #43).
func TestStatusLineTruncatesByCells(t *testing.T) {
	m := Model{width: 10, flash: "copied as Markdown", theme: theme.New(true)}
	line := m.statusLine()
	if !utf8.ValidString(line) {
		t.Errorf("status line is invalid UTF-8: %q", line)
	}
	if got := lipgloss.Width(line); got != 10 {
		t.Errorf("status line is %d cells, want 10: %q", got, line)
	}
	if plain := strings.TrimSpace(line); !strings.Contains(plain, "✓") || !strings.Contains(plain, "…") {
		t.Errorf("expected truncated flash with intact ✓: %q", line)
	}
}

// Errors wrap API and config strings; the status bar sanitizes them.
func TestStatusLineSanitizesErrors(t *testing.T) {
	m := Model{width: 80, theme: theme.New(true), err: errors.New("boom \x1b]0;pwned\a")}
	line := m.statusLine()
	if strings.Contains(line, "pwned") || strings.Contains(line, "\a") {
		t.Errorf("error text leaked an escape sequence: %q", line)
	}
	if !strings.Contains(line, "error: boom") {
		t.Errorf("error text missing: %q", line)
	}
}
