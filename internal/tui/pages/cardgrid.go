package pages

import (
	"fmt"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	lipgloss "charm.land/lipgloss/v2"
	zone "github.com/lrstanley/bubblezone/v2"

	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

// card is one card in a grid, drawn from the page's render context C.
// Bodies must fit within (w, h) in the grid; fullscreen bodies may run
// long, the viewport scrolls them.
type card[C any] interface {
	key() string
	title() string
	headline(ctx C) string
	body(ctx C, w, h int, full bool) string
	export(ctx C) (headers []string, rows [][]string)
}

// styleCtx is the style vocabulary every card shares; each page embeds it
// in its render context. Labels and values wear text tokens, marks wear
// series color.
type styleCtx struct {
	th *theme.Theme
}

func (s styleCtx) label() lipgloss.Style { return s.th.Header }
func (s styleCtx) value() lipgloss.Style { return lipgloss.NewStyle().Foreground(s.th.Text) }
func (s styleCtx) faint() lipgloss.Style { return lipgloss.NewStyle().Foreground(s.th.Faint) }
func (s styleCtx) good() lipgloss.Style  { return lipgloss.NewStyle().Foreground(s.th.Good) }
func (s styleCtx) bad() lipgloss.Style   { return lipgloss.NewStyle().Foreground(s.th.Bad) }

// mark styles a series slot: the four fixed series roles, then the chrome
// colors for anything past them (the trend latency cards).
func (s styleCtx) mark(slot int) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(s.markColor(slot))
}

func (s styleCtx) markColor(slot int) color.Color {
	if slot < len(s.th.Series) {
		return s.th.Series[slot]
	}
	if slot == len(s.th.Series) {
		return s.th.Primary
	}
	return s.th.Accent
}

const (
	maxCardOuterH = 14
	fullHint      = "esc back · j/k scroll · h/l pan · y copy"
)

// cardGrid is the shell the charts and trends pages share: a grid of cards
// with one focused, any of which expands to a scrollable fullscreen body.
// The page owns the data and the cards; the grid owns focus, fullscreen
// state, the viewport, key and click handling, and the card frames.
type cardGrid[C any] struct {
	cards      []card[C]
	layout     [][]int // rows of card indices; a row's cards share the width
	zonePrefix string
	km         keys.KeyMap
	th         *theme.Theme

	// ctx builds the page's render context. ready gates fullscreen
	// rendering while the page has nothing to draw: a card indexing an
	// empty series would panic. styleHead styles a card's headline for the
	// grid or the fullscreen title; nil keeps the card's own styling.
	ctx       func() C
	ready     func() bool
	styleHead func(s string, full bool) string

	focus         int
	full          string         // fullscreen card key, "" = grid
	vp            viewport.Model // scrolls the fullscreen card body
	width, height int
}

func newCardGrid[C any](cards []card[C], layout [][]int, zonePrefix string, km keys.KeyMap, th *theme.Theme) cardGrid[C] {
	return cardGrid[C]{
		cards: cards, layout: layout, zonePrefix: zonePrefix, km: km, th: th,
		vp: viewport.New(),
	}
}

func (g *cardGrid[C]) setTheme(th *theme.Theme) {
	g.th = th
	g.refreshFull()
}

func (g *cardGrid[C]) setSize(w, h int) {
	g.width, g.height = w, h
	g.refreshFull()
}

func (g *cardGrid[C]) fullscreen() bool { return g.full != "" }

func (g *cardGrid[C]) activeCard() card[C] {
	for _, cd := range g.cards {
		if cd.key() == g.full {
			return cd
		}
	}
	return nil
}

// handleClick resolves a click against the card zones. Only the grid has
// card zones on screen, so fullscreen clicks fall through.
func (g *cardGrid[C]) handleClick(msg tea.MouseClickMsg, z *zone.Manager) tea.Cmd {
	if z == nil || g.fullscreen() {
		return nil
	}
	for i, cd := range g.cards {
		if z.Get(g.zonePrefix + cd.key()).InBounds(msg) {
			g.clickCard(i)
			break
		}
	}
	return nil
}

// clickCard focuses the clicked card; clicking the focused card expands it.
func (g *cardGrid[C]) clickCard(i int) {
	if g.focus == i {
		g.openFull(g.cards[i].key())
		return
	}
	g.focus = i
}

// openFull expands a card to fullscreen with the viewport reset to the top.
func (g *cardGrid[C]) openFull(key string) {
	g.full = key
	g.vp.SetYOffset(0)
	g.vp.SetXOffset(0)
	g.refreshFull()
}

func (g *cardGrid[C]) closeFull() { g.full = "" }

// refreshFull re-renders the fullscreen card into the viewport, preserving
// the scroll offset (SetContent clamps it if the content shrank).
func (g *cardGrid[C]) refreshFull() {
	if g.full == "" || (g.ready != nil && !g.ready()) {
		return
	}
	active := g.activeCard()
	if active == nil {
		return
	}
	h := max(g.height-2, 1) // title line + scroll indicator line
	g.vp.SetWidth(g.width)
	g.vp.SetHeight(h)
	g.vp.SetContent(active.body(g.ctx(), g.width-2, h, true))
}

// scroll moves the fullscreen view; wired to the mouse wheel.
func (g *cardGrid[C]) scroll(delta int) {
	switch {
	case g.full == "":
	case delta < 0:
		g.vp.ScrollUp(-delta)
	default:
		g.vp.ScrollDown(delta)
	}
}

// handleKey claims grid navigation and fullscreen scrolling; false leaves
// the key to the global keymap.
func (g *cardGrid[C]) handleKey(msg tea.KeyPressMsg) bool {
	if g.fullscreen() {
		return g.handleFullKey(msg)
	}
	km := g.km
	switch {
	case key.Matches(msg, km.Left):
		g.moveCol(-1)
	case key.Matches(msg, km.Right):
		g.moveCol(1)
	case key.Matches(msg, km.Up):
		g.moveRow(-1)
	case key.Matches(msg, km.Down):
		g.moveRow(1)
	case key.Matches(msg, km.Expand, km.Drill):
		g.openFull(g.cards[g.focus].key())
	default:
		return false
	}
	return true
}

func (g *cardGrid[C]) handleFullKey(msg tea.KeyPressMsg) bool {
	km := g.km
	switch {
	case key.Matches(msg, km.Back, km.Expand, km.Drill):
		g.closeFull()
	case key.Matches(msg, km.Down):
		g.vp.ScrollDown(1)
	case key.Matches(msg, km.Up):
		g.vp.ScrollUp(1)
	case key.Matches(msg, km.PageDown):
		g.vp.PageDown()
	case key.Matches(msg, km.PageUp):
		g.vp.PageUp()
	case key.Matches(msg, km.HalfDown):
		g.vp.HalfPageDown()
	case key.Matches(msg, km.HalfUp):
		g.vp.HalfPageUp()
	case key.Matches(msg, km.Top):
		g.vp.SetYOffset(0)
	case key.Matches(msg, km.Bottom):
		g.vp.GotoBottom()
	case key.Matches(msg, km.Left):
		g.vp.ScrollLeft(4) // pan wide content, e.g. per-member rows
	case key.Matches(msg, km.Right):
		g.vp.ScrollRight(4)
	default:
		return false
	}
	return true
}

// position locates card i in the layout.
func (g *cardGrid[C]) position(i int) (row, col int) {
	for r, cells := range g.layout {
		for c, idx := range cells {
			if idx == i {
				return r, c
			}
		}
	}
	return 0, 0
}

// moveCol moves the focus along its row; the row edges clamp.
func (g *cardGrid[C]) moveCol(delta int) {
	row, col := g.position(g.focus)
	cells := g.layout[row]
	g.focus = cells[min(max(col+delta, 0), len(cells)-1)]
}

// moveRow moves the focus to the row above or below, landing on the card
// under the center of the current one: the same column between equal
// rows, the middle card above a full-width one.
func (g *cardGrid[C]) moveRow(delta int) {
	row, col := g.position(g.focus)
	next := row + delta
	if next < 0 || next >= len(g.layout) {
		return
	}
	from, to := g.layout[row], g.layout[next]
	col = ((2*col + 1) * len(to)) / (2 * len(from))
	g.focus = to[min(col, len(to)-1)]
}

func (g *cardGrid[C]) head(s string, full bool) string {
	if g.styleHead == nil {
		return s
	}
	return g.styleHead(s, full)
}

// viewFull renders the fullscreen card: title line, scrolled body, and a
// scroll indicator that is always present (even blank) so the view is
// exactly height lines.
func (g *cardGrid[C]) viewFull(ctx C) string {
	active := g.activeCard()
	if active == nil {
		return ""
	}
	title := lipgloss.NewStyle().Bold(true).Foreground(g.th.Primary).Render(active.title()) +
		"  " + g.head(active.headline(ctx), true) +
		"   " + g.th.HelpDesc.Render(fullHint)
	title = lipgloss.NewStyle().MaxWidth(g.width).Render(title)

	indicator := ""
	if total := g.vp.TotalLineCount(); total > g.vp.Height() {
		top := g.vp.YOffset() + 1
		bottom := min(top+g.vp.VisibleLineCount()-1, total)
		indicator = g.th.HelpDesc.Render(fmt.Sprintf(
			"  rows %d–%d of %d · %d%%", top, bottom, total, int(g.vp.ScrollPercent()*100)))
	}
	return lipgloss.JoinVertical(lipgloss.Left, title, g.vp.View(), indicator)
}

// splitRows divides avail lines into n card rows, spreading the remainder
// over the top rows and capping each at maxCardOuterH.
func splitRows(avail, n int) []int {
	base, rem := avail/n, avail%n
	rows := make([]int, n)
	for i := range rows {
		rows[i] = base
		if i < rem {
			rows[i]++
		}
		if rows[i] > maxCardOuterH {
			rows[i] = maxCardOuterH
		}
	}
	return rows
}

// gridRows renders one line of boxes per layout row at the given outer
// heights; the cards in a row split the width evenly.
func (g *cardGrid[C]) gridRows(ctx C, rowHs []int, z *zone.Manager) []string {
	parts := make([]string, 0, len(g.layout))
	for r, cells := range g.layout {
		innerW := g.width/len(cells) - 4 // border + padding
		innerH := rowHs[r] - 3           // border + title line
		row := make([]string, 0, len(cells))
		for _, i := range cells {
			row = append(row, g.renderCard(ctx, i, innerW, innerH, z))
		}
		parts = append(parts, lipgloss.JoinHorizontal(lipgloss.Top, row...))
	}
	return parts
}

func (g *cardGrid[C]) renderCard(ctx C, i, innerW, innerH int, z *zone.Manager) string {
	cd := g.cards[i]
	border := g.th.Faint
	titleStyle := g.th.Header
	if i == g.focus {
		border = g.th.Accent
		titleStyle = lipgloss.NewStyle().Bold(true).Foreground(g.th.Accent)
	}
	title := titleStyle.Render(cd.title())
	head := g.head(" "+cd.headline(ctx), false)
	// MaxWidth/MaxHeight guard against wrapping: a wrapped title or legend
	// line would make the card taller than budgeted and clip the footer.
	guard := lipgloss.NewStyle().MaxWidth(innerW)
	body := guard.MaxHeight(innerH).Render(cd.body(ctx, innerW, innerH, false))
	// Pad the body to exactly innerH lines so cards in a row box equally.
	if n := strings.Count(body, "\n") + 1; n < innerH {
		body += strings.Repeat("\n", innerH-n)
	}
	content := lipgloss.JoinVertical(lipgloss.Left, guard.Render(title+head), body)
	// Width includes border and padding (lipgloss v2), so innerW+4 makes the
	// box wrap exactly at innerW, the width the body was rendered for.
	box := lipgloss.NewStyle().
		BorderStyle(lipgloss.RoundedBorder()).BorderForeground(border).
		Padding(0, 1).Width(innerW + 4).
		Render(content)
	if z != nil {
		box = z.Mark(g.zonePrefix+cd.key(), box)
	}
	return box
}

// exportTable returns the fullscreen card's data for Markdown export;
// ok=false from the grid.
func (g *cardGrid[C]) exportTable(ctx C) (title string, headers []string, rows [][]string, ok bool) {
	active := g.activeCard()
	if active == nil {
		return "", nil, nil, false
	}
	h, r := active.export(ctx)
	return active.title(), h, r, true
}
