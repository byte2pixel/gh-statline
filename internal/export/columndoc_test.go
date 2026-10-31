package export

import (
	"strings"
	"testing"
	"unicode"
)

// Every team column carries a definition the status bar can show whole
// (#121): non-empty, one line, ASCII so a byte is a cell, and under the
// width the bar guarantees.
func TestEveryTeamColumnHasAShortDoc(t *testing.T) {
	for _, c := range teamColumns {
		if c.Doc == "" {
			t.Errorf("%s has no Doc", c.Key)
			continue
		}
		if strings.ContainsAny(c.Doc, "\n\t") {
			t.Errorf("%s Doc spans lines: %q", c.Key, c.Doc)
		}
		for _, r := range c.Doc {
			if r > unicode.MaxASCII {
				t.Errorf("%s Doc is not ASCII, so its width is not its length: %q", c.Key, c.Doc)
				break
			}
		}
		if len(c.Doc) > maxDocWidth {
			t.Errorf("%s Doc is %d cells, over %d: %q", c.Key, len(c.Doc), maxDocWidth, c.Doc)
		}
	}
}

func TestTeamColumnDocLookup(t *testing.T) {
	if got := TeamColumnDoc("commented"); !strings.Contains(got, "thread reply") {
		t.Errorf("commented doc = %q, want the thread-reply rule from #105", got)
	}
	if got := TeamColumnDoc("no_such_column"); got != "" {
		t.Errorf("unknown key doc = %q, want empty", got)
	}
}
