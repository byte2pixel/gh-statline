package app

import (
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/db"
)

// fakeBrowser records the URLs handed to the browser seam and answers with
// a scripted error, so no test launches a real browser.
type fakeBrowser struct {
	urls []string
	err  error
}

func (b *fakeBrowser) open(url string) error {
	b.urls = append(b.urls, url)
	return b.err
}

// openPRDeps adds two open, non-draft PRs to the fixture so the open-PR
// card has a list to pick from; #7 is the older, so it leads the list.
// GH_HOST is pinned so the URL does not depend on the developer's gh login.
func openPRDeps(t *testing.T) (Deps, *fakeBrowser) {
	t.Helper()
	t.Setenv("GH_HOST", "github.com")
	deps := testDeps(t)
	now := fixedNow.Unix()
	repoID := deps.Targets[0].RepoID
	if err := deps.Store.SavePullRequests([]db.PullRequest{
		{ID: "PR_7", RepoID: repoID, Number: 7, Author: "alice", Title: "old one", State: "OPEN",
			CreatedAt: now - 5*86400, UpdatedAt: now},
		{ID: "PR_8", RepoID: repoID, Number: 8, Author: "bob", Title: "newer one", State: "OPEN",
			CreatedAt: now - 2*86400, UpdatedAt: now},
	}); err != nil {
		t.Fatal(err)
	}
	b := &fakeBrowser{}
	deps.Browser = b.open
	return deps, b
}

var keyOpen = tea.KeyPressMsg{Code: 'o', Text: "o"}

// chartsWithAging loads the dashboard and lands on the charts tab with the
// open-PR card focused: row two, column one of the grid.
func chartsWithAging(t *testing.T, deps Deps) Model {
	t.Helper()
	m := New(deps)
	model, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m = model.(Model)
	// dataMsg feeds the team table and the charts in one step, so a row in
	// the table means the charts have their dashboard too.
	m = pump(t, m, m.loadData(), func(m Model) bool { return m.teamStats.SelectedLogin() != "" })
	for _, k := range []tea.KeyPressMsg{
		{Code: '2', Text: "2"}, {Code: 'j', Text: "j"}, {Code: 'j', Text: "j"}, {Code: 'l', Text: "l"},
	} {
		model, _ = m.Update(k)
		m = model.(Model)
	}
	if m.charts.Focus() != 7 {
		t.Fatalf("focus = %d, want the open-PR card (7)", m.charts.Focus())
	}
	return m
}

// pressOpen sends the open key and pumps until done reports the app has
// reacted.
func pressOpen(t *testing.T, m Model, done func(Model) bool) Model {
	t.Helper()
	model, cmd := m.Update(keyOpen)
	return pump(t, model.(Model), cmd, done)
}

// Off the card, o has nothing to open and says where to find one.
func TestOpenKeyOffTheCardExplains(t *testing.T) {
	deps, b := openPRDeps(t)
	model, _ := New(deps).Update(keyOpen)
	m := model.(Model)
	if len(b.urls) != 0 {
		t.Errorf("o on the team tab opened %v", b.urls)
	}
	if !strings.Contains(m.flash, "Open PRs") {
		t.Errorf("flash = %q, want a pointer to the open-PR card", m.flash)
	}
}

// With the card focused on the grid, o opens the oldest open PR at the
// host gh is configured for, and the flash names it.
func TestOpenKeyOpensOldestFromGrid(t *testing.T) {
	deps, b := openPRDeps(t)
	m := pressOpen(t, chartsWithAging(t, deps), func(m Model) bool { return m.flash != "" })
	if want := []string{"https://github.com/acme/api/pull/7"}; !slices.Equal(b.urls, want) {
		t.Errorf("browser got %v, want %v", b.urls, want)
	}
	if !strings.Contains(m.flash, "acme/api#7") {
		t.Errorf("flash = %q, want it to name the PR", m.flash)
	}
}

// Fullscreen, j/k move a cursor over the stalest list and o opens the
// marked row. Closing the card puts the cursor back on the oldest.
func TestOpenKeyFollowsFullscreenCursor(t *testing.T) {
	deps, b := openPRDeps(t)
	m := chartsWithAging(t, deps)
	press := func(k tea.KeyPressMsg) {
		model, _ := m.Update(k)
		m = model.(Model)
	}
	press(tea.KeyPressMsg{Code: 'f', Text: "f"})
	press(tea.KeyPressMsg{Code: 'j', Text: "j"})
	if v := stripANSI(m.View().Content); !strings.Contains(v, "▸ 2d acme/api#8") {
		t.Errorf("cursor row not marked after j:\n%s", v)
	}
	m = pressOpen(t, m, func(m Model) bool { return len(b.urls) == 1 })
	press(tea.KeyPressMsg{Code: 'k', Text: "k"})
	m = pressOpen(t, m, func(m Model) bool { return len(b.urls) == 2 })
	if want := []string{"https://github.com/acme/api/pull/8", "https://github.com/acme/api/pull/7"}; !slices.Equal(b.urls, want) {
		t.Errorf("browser got %v, want %v", b.urls, want)
	}

	press(tea.KeyPressMsg{Code: 'G', Text: "G", Mod: tea.ModShift})
	press(tea.KeyPressMsg{Code: tea.KeyEscape})
	press(tea.KeyPressMsg{Code: 'f', Text: "f"})
	if pr, ok := m.charts.SelectedPR(); !ok || pr.Number != 7 {
		t.Errorf("after reopening, selected = %+v (ok=%v), want the oldest (#7)", pr, ok)
	}
}

// A launcher failure lands in the status bar as an error, with no flash
// in front of it.
func TestOpenKeyErrorSurfaces(t *testing.T) {
	deps, b := openPRDeps(t)
	b.err = errors.New("no browser here")
	m := pressOpen(t, chartsWithAging(t, deps), func(m Model) bool { return m.err != nil })
	if m.flash != "" {
		t.Errorf("flash = %q, want none on failure", m.flash)
	}
	if !strings.Contains(m.err.Error(), "no browser here") {
		t.Errorf("err = %v, want the launcher failure", m.err)
	}
}
