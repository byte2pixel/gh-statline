package pages

import (
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

var (
	chartsLayout = [][]int{{0, 1, 2}, {3, 4, 5}, {6, 7, 8}}
	trendsLayout = [][]int{{0, 1, 2}, {3, 4, 5}, {6}}
)

// stubCard renders a fixed number of wide lines: enough to exercise the
// shell without any metrics behind it.
type stubCard struct {
	k     string
	lines int
}

func (s stubCard) key() string              { return s.k }
func (s stubCard) title() string            { return "Card " + s.k }
func (s stubCard) headline(struct{}) string { return "headline " + s.k }
func (s stubCard) body(_ struct{}, _, _ int, _ bool) string {
	rows := make([]string, s.lines)
	for i := range rows {
		rows[i] = fmt.Sprintf("%s line %02d %s", s.k, i, strings.Repeat("·", 120))
	}
	return strings.Join(rows, "\n")
}
func (s stubCard) export(struct{}) ([]string, [][]string) {
	return []string{"Key"}, [][]string{{s.k}}
}

// stubGrid builds a 90×20 grid of stub cards over the given layout.
func stubGrid(t *testing.T, layout [][]int, lines int) *cardGrid[struct{}] {
	t.Helper()
	th := theme.New(true)
	n := 0
	for _, row := range layout {
		n += len(row)
	}
	cards := make([]card[struct{}], n)
	for i := range cards {
		cards[i] = stubCard{k: fmt.Sprintf("c%d", i), lines: lines}
	}
	g := newCardGrid(cards, layout, "test:", keys.Default(), &th)
	g.ctx = func() struct{} { return struct{}{} }
	g.setSize(90, 20)
	return &g
}

func TestSplitRows(t *testing.T) {
	for _, c := range []struct {
		avail, n int
		want     []int
	}{
		{15, 3, []int{5, 5, 5}},
		{16, 3, []int{6, 5, 5}}, // the remainder goes to the top rows
		{17, 3, []int{6, 6, 5}},
		{60, 3, []int{14, 14, 14}}, // capped at maxCardOuterH
	} {
		if got := splitRows(c.avail, c.n); !slices.Equal(got, c.want) {
			t.Errorf("splitRows(%d, %d) = %v, want %v", c.avail, c.n, got, c.want)
		}
	}
}

// Focus moves within the layout: left/right clamp at the row edges, up
// and down land on the card under the center of the current one.
func TestGridNavigation(t *testing.T) {
	walk := func(t *testing.T, g *cardGrid[struct{}], seq string, want []int) {
		t.Helper()
		for i, k := range strings.Split(seq, ",") {
			if !g.handleKey(press(k)) {
				t.Fatalf("%q not handled", k)
			}
			if g.focus != want[i] {
				t.Errorf("%s: after step %d (%q) focus = %d, want %d", seq, i, k, g.focus, want[i])
			}
		}
	}
	t.Run("3x3 clamps at the row edges", func(t *testing.T) {
		g := stubGrid(t, chartsLayout, 3)
		walk(t, g, "l,l,l,j,h,j,j,k,k,k", []int{1, 2, 2, 5, 4, 7, 7, 4, 1, 1})
		g.focus = 3
		walk(t, g, "h", []int{3}) // no wrap onto the row above
	})
	t.Run("full-width row lands on the center card above", func(t *testing.T) {
		g := stubGrid(t, trendsLayout, 3)
		walk(t, g, "j,j,k,l,j,h,l,k", []int{3, 6, 4, 5, 6, 6, 6, 4})
	})
	t.Run("unbound keys fall through", func(t *testing.T) {
		g := stubGrid(t, chartsLayout, 3)
		if g.handleKey(press("x")) || g.handleKey(press("q")) {
			t.Error("grid claimed a key it has no binding for")
		}
	})
}

// Every fullscreen binding moves the viewport, and expand/back toggle the
// card from either side.
func TestFullscreenKeys(t *testing.T) {
	g := stubGrid(t, chartsLayout, 100)
	if !g.handleKey(press("f")) || g.full != "c0" {
		t.Fatal("f did not expand the focused card")
	}
	vpH := g.vp.Height()
	if vpH != 18 { // 20 minus the title and indicator lines
		t.Fatalf("viewport height = %d, want 18", vpH)
	}
	at := func(msg tea.KeyPressMsg, want int) {
		t.Helper()
		if !g.handleKey(msg) {
			t.Fatalf("%q not handled in fullscreen", msg.String())
		}
		if got := g.vp.YOffset(); got != want {
			t.Errorf("after %q: y offset = %d, want %d", msg.String(), got, want)
		}
	}
	at(press("j"), 1)
	at(press("k"), 0)
	at(press("d"), vpH/2)
	at(press("u"), 0)
	at(tea.KeyPressMsg{Code: tea.KeyPgDown}, vpH)
	at(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, 2*vpH)
	at(tea.KeyPressMsg{Code: tea.KeyPgUp}, vpH)
	at(press("G"), 100-vpH)
	at(press("g"), 0)
	at(tea.KeyPressMsg{Code: tea.KeyEnd}, 100-vpH)
	at(tea.KeyPressMsg{Code: tea.KeyHome}, 0)

	if !g.handleKey(press("l")) || g.vp.XOffset() != 4 {
		t.Errorf("l did not pan right: x offset = %d", g.vp.XOffset())
	}
	if !g.handleKey(press("h")) || g.vp.XOffset() != 0 {
		t.Errorf("h did not pan back: x offset = %d", g.vp.XOffset())
	}
	if g.handleKey(press("x")) {
		t.Error("fullscreen claimed an unbound key")
	}

	if !g.handleKey(tea.KeyPressMsg{Code: tea.KeyEscape}) || g.fullscreen() {
		t.Error("esc did not return to the grid")
	}
	if !g.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter}) || !g.fullscreen() {
		t.Error("enter did not expand the card")
	}
	if !g.handleKey(press("f")) || g.fullscreen() {
		t.Error("f did not collapse the card")
	}
}

// Fullscreen resets the scroll on every open and re-renders on resize, and
// the mouse wheel only moves a fullscreen body.
func TestGridScrollAndResize(t *testing.T) {
	g := stubGrid(t, chartsLayout, 100)
	g.scroll(3)
	if g.fullscreen() || g.vp.YOffset() != 0 {
		t.Error("wheel in the grid must be a no-op")
	}
	g.openFull("c4")
	g.scroll(3)
	if g.vp.YOffset() != 3 {
		t.Errorf("wheel down: y offset = %d, want 3", g.vp.YOffset())
	}
	g.scroll(-3)
	if g.vp.YOffset() != 0 {
		t.Errorf("wheel up: y offset = %d, want 0", g.vp.YOffset())
	}
	g.handleKey(press("G"))
	g.setSize(90, 30)
	if got, want := g.vp.YOffset(), 100-28; got != want {
		t.Errorf("after growing the viewport: y offset = %d, want %d (clamped to the new bottom)", got, want)
	}
	g.closeFull()
	g.openFull("c4")
	if g.vp.YOffset() != 0 {
		t.Error("reopening a card did not start at the top")
	}
}

// A page with nothing to draw must not render a fullscreen body: its cards
// index data that is not there.
func TestFullscreenWaitsForReady(t *testing.T) {
	g := stubGrid(t, chartsLayout, 50)
	ready := false
	g.ready = func() bool { return ready }
	g.openFull("c0")
	if n := g.vp.TotalLineCount(); n != 0 {
		t.Fatalf("body rendered before the page was ready: %d lines", n)
	}
	if !g.fullscreen() {
		t.Fatal("the card must stay open, ready to render once data lands")
	}
	ready = true
	g.refreshFull()
	if n := g.vp.TotalLineCount(); n != 50 {
		t.Errorf("body after ready: %d lines, want 50", n)
	}
}

// The fullscreen frame is exactly the page height: title, viewport, and an
// indicator line that is present even when blank.
func TestFullscreenFrameHeight(t *testing.T) {
	for _, lines := range []int{3, 100} {
		g := stubGrid(t, chartsLayout, lines)
		g.openFull("c2")
		view := g.viewFull(struct{}{})
		if got := lipgloss.Height(view); got != 20 {
			t.Errorf("%d-line card: fullscreen view is %d lines, want 20", lines, got)
		}
		if got := lipgloss.Width(view); got > 90 {
			t.Errorf("%d-line card: fullscreen view is %d cols wide, budget 90", lines, got)
		}
	}
	g := stubGrid(t, chartsLayout, 100)
	g.openFull("c2")
	if v := plain(g.viewFull(struct{}{})); !strings.Contains(v, "rows 1–18 of 100") {
		t.Errorf("scroll indicator missing:\n%s", v)
	}
}

// A click focuses a card; a click on the focused card expands it.
func TestClickFocusesThenExpands(t *testing.T) {
	g := stubGrid(t, chartsLayout, 3)
	z := zone.New()
	view := lipgloss.JoinVertical(lipgloss.Left, g.gridRows(struct{}{}, splitRows(20, 3), z)...)
	z.Scan(view)
	click := tea.MouseClickMsg{X: 45, Y: 2, Button: tea.MouseLeft} // inside card 1's box
	// Scan hands the bounds to a worker goroutine; wait for card 1's zone.
	for deadline := time.Now().Add(2 * time.Second); !z.Get("test:c1").InBounds(click); {
		if time.Now().After(deadline) {
			t.Fatal("zone for card 1 never resolved")
		}
		time.Sleep(5 * time.Millisecond)
	}
	g.handleClick(click, z)
	if g.focus != 1 || g.fullscreen() {
		t.Fatalf("first click: focus = %d, fullscreen = %v; want 1, false", g.focus, g.fullscreen())
	}
	g.handleClick(click, z)
	if g.full != "c1" {
		t.Errorf("second click did not expand card 1: full = %q", g.full)
	}
	g.handleClick(click, z)
	if g.full != "c1" {
		t.Error("clicks must fall through while fullscreen: no card zones are on screen")
	}
}

// The export follows the view: the fullscreen card's table, or nothing
// from the grid so the page can fall back to its own summary.
func TestGridExportFollowsView(t *testing.T) {
	g := stubGrid(t, chartsLayout, 3)
	if _, _, _, ok := g.exportTable(struct{}{}); ok {
		t.Error("grid must not export a card table")
	}
	g.openFull("c5")
	title, headers, rows, ok := g.exportTable(struct{}{})
	if !ok || title != "Card c5" || len(headers) != 1 || len(rows) != 1 || rows[0][0] != "c5" {
		t.Errorf("fullscreen export = %q %v %v %v", title, headers, rows, ok)
	}
}
