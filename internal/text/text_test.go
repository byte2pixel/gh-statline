package text

import (
	"strings"
	"testing"
	"unicode/utf8"
)

// hostile is the canonical injection fixture: an OSC title-set sequence with
// BEL terminator, a CSI color, and wide characters.
const hostile = "evil \x1b]0;pwned\x07 \x1b[31mtitle 你好"

func TestSanitizeStripsEscapeSequences(t *testing.T) {
	got := Sanitize(hostile)
	for _, bad := range []string{"\x1b", "\x07", "pwned", "31m"} {
		if strings.Contains(got, bad) {
			t.Errorf("Sanitize left %q in %q", bad, got)
		}
	}
	for _, want := range []string{"evil", "title", "你好"} {
		if !strings.Contains(got, want) {
			t.Errorf("Sanitize dropped printable %q from %q", want, got)
		}
	}
	if HasControls(got) {
		t.Errorf("sanitized string still has controls: %q", got)
	}
}

func TestSanitizeCases(t *testing.T) {
	cases := []struct{ in, want string }{
		{"plain title", "plain title"},                 // untouched
		{"tab\there\nand there", "tab here and there"}, // whitespace flattens
		{"x\x1b]0;stolen", "x"},                        // unterminated OSC: payload gone
		{"a\x1b[2Jb", "ab"},                            // clear-screen CSI
		{"bell\x07only", "bellonly"},                   // bare C0 dropped
		{"a\u009bXb", "aXb"},                           // C1 CSI code point dropped
		{"你好, world", "你好, world"},                     // non-ASCII is not a control
	}
	for _, c := range cases {
		if got := Sanitize(c.in); got != c.want {
			t.Errorf("Sanitize(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestHasControls(t *testing.T) {
	if HasControls("plain 你好") {
		t.Error("plain text flagged")
	}
	for _, s := range []string{"a\x1bb", "a\tb", "a\u009bb", "a\x7fb"} {
		if !HasControls(s) {
			t.Errorf("HasControls(%q) = false", s)
		}
	}
}

func TestWidthCountsCells(t *testing.T) {
	cases := []struct {
		in   string
		want int
	}{
		{"abc", 3},
		{"你好", 4},       // CJK: 2 cells each
		{"✓ synced", 8}, // ambiguous-width check mark counts 1
		{"", 0},
	}
	for _, c := range cases {
		if got := Width(c.in); got != c.want {
			t.Errorf("Width(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestTruncateIsWidthAndUTF8Safe(t *testing.T) {
	if got := Truncate("abc", 5); got != "abc" {
		t.Errorf("fitting string changed: %q", got)
	}
	if got := Truncate("你好世界", 5); got != "你好…" {
		t.Errorf("Truncate(你好世界, 5) = %q, want 你好…", got)
	}
	// A wide char that cannot be split leaves a one-cell hole, never a
	// broken sequence or an overflow.
	for w := 0; w <= 8; w++ {
		got := Truncate("a你b好c", w)
		if !utf8.ValidString(got) {
			t.Errorf("w=%d: invalid UTF-8 %q", w, got)
		}
		if Width(got) > w {
			t.Errorf("w=%d: width %d overflows", w, Width(got))
		}
	}
	if got := Clip("你好", 3); got != "你" {
		t.Errorf("Clip(你好, 3) = %q, want 你", got)
	}
}
