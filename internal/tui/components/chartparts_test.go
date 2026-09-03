package components

import (
	"testing"
	"unicode/utf8"

	"github.com/byte2pixel/gh-statline/internal/text"
)

// Pad promises exactly w cells — rune counting broke that for CJK and emoji,
// shifting every bar and matrix column after a wide label.
func TestPadIsDisplayWidthAware(t *testing.T) {
	cases := []struct {
		in   string
		w    int
		want string
	}{
		{"abc", 5, "abc  "},
		{"你好", 5, "你好 "},   // 4 cells + 1 space
		{"你好世界", 5, "你好…"}, // truncated: 4 cells + ellipsis
		{"abc", 2, "a…"},
		{"", 3, "   "},
	}
	for _, c := range cases {
		got := Pad(c.in, c.w)
		if got != c.want {
			t.Errorf("Pad(%q, %d) = %q, want %q", c.in, c.w, got, c.want)
		}
	}
	// A wide char straddling the cut can leave a hole before the ellipsis;
	// the padding must still land on exactly w cells, and never split UTF-8.
	for _, s := range []string{"你好世", "a你b好c", "naïve🎉name"} {
		for w := 2; w <= 8; w++ {
			got := Pad(s, w)
			if !utf8.ValidString(got) {
				t.Errorf("Pad(%q, %d) produced invalid UTF-8 %q", s, w, got)
			}
			if text.Width(got) != w {
				t.Errorf("Pad(%q, %d) is %d cells, want %d", s, w, text.Width(got), w)
			}
		}
	}
}
