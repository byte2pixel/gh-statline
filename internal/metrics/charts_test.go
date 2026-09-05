package metrics

import (
	"testing"
	"time"
)

// FmtDur is the one duration formatter every surface prints: the team
// table, the chart labels, the trend cards, and the Markdown export.
func TestFmtDur(t *testing.T) {
	for _, c := range []struct {
		d    time.Duration
		want string
	}{
		{0, "–"},
		{45 * time.Second, "45s"},
		{30 * time.Minute, "30m"},
		{5 * time.Hour, "5.0h"},
		{48 * time.Hour, "2.0d"},
	} {
		if got := FmtDur(c.d); got != c.want {
			t.Errorf("FmtDur(%v) = %q, want %q", c.d, got, c.want)
		}
	}
}
