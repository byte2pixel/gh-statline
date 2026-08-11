package app

import (
	"errors"
	"io/fs"
	"os"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
	"github.com/byte2pixel/gh-statline/internal/tui/pages"
)

// loadSaved reads the config the app persisted to the STATLINE_CONFIG path
// set by testDeps. Going through config.Load also proves the written file
// passes Validate.
func loadSaved(t *testing.T) config.Config {
	t.Helper()
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("loading persisted config: %v", err)
	}
	return cfg
}

func assertNoConfig(t *testing.T) {
	t.Helper()
	if _, err := os.Stat(os.Getenv("STATLINE_CONFIG")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("config file should not exist, stat err = %v", err)
	}
}

func TestWindowCyclePersists(t *testing.T) {
	m := New(testDeps(t))
	model, _ := m.Update(tea.KeyPressMsg{Code: 'w', Text: "w"})
	m2 := model.(Model)

	saved := loadSaved(t)
	if saved.UI.Window != "90d" { // default 30d → next preset
		t.Errorf("saved ui.window = %q, want 90d", saved.UI.Window)
	}
	// The whole config must round-trip, not just the changed field.
	if saved.DefaultTeam != "testers" || len(saved.Teams) != 1 {
		t.Errorf("saved config lost data: default_team=%q teams=%d", saved.DefaultTeam, len(saved.Teams))
	}
	if m2.deps.Cfg.UI.Window != "90d" {
		t.Errorf("in-memory ui.window = %q, want 90d", m2.deps.Cfg.UI.Window)
	}
}

func TestSortKeyPersists(t *testing.T) {
	m := New(testDeps(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m2 := model.(Model)

	model, cmd := m2.Update(tea.KeyPressMsg{Code: 'l', Text: "l"})
	m2 = model.(Model)
	if cmd == nil {
		t.Fatal("sort key change returned no command")
	}
	raw := cmd()
	msg, ok := raw.(pages.SortChangedMsg)
	if !ok {
		t.Fatalf("sort key change emitted %T, want pages.SortChangedMsg", raw)
	}
	model, _ = m2.Update(msg)
	m2 = model.(Model)

	if saved := loadSaved(t); saved.UI.Sort != "reviews" { // right of prs_merged
		t.Errorf("saved ui.sort = %q, want reviews", saved.UI.Sort)
	}
	if m2.deps.Cfg.UI.Sort != "reviews" {
		t.Errorf("in-memory ui.sort = %q, want reviews", m2.deps.Cfg.UI.Sort)
	}
}

func TestFlipSortDoesNotSave(t *testing.T) {
	m := New(testDeps(t))
	model, _ := m.Update(tea.WindowSizeMsg{Width: 110, Height: 30})
	m2 := model.(Model)

	if _, cmd := m2.Update(tea.KeyPressMsg{Code: '-', Text: "-"}); cmd != nil {
		t.Error("flip sort should not emit a command")
	}
	assertNoConfig(t)
}

func TestTeamSwitchPersistsDefaultTeam(t *testing.T) {
	deps := testDeps(t)
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
	})
	m := New(deps)

	model, _ := m.Update(overlays.TeamChosenMsg{Name: "others"})
	m2 := model.(Model)
	if m2.deps.Team.Name != "others" {
		t.Fatalf("active team = %q, want others", m2.deps.Team.Name)
	}

	if saved := loadSaved(t); saved.DefaultTeam != "others" {
		t.Errorf("saved default_team = %q, want others", saved.DefaultTeam)
	}
}

func TestCustomRangeDoesNotPersist(t *testing.T) {
	m := New(testDeps(t))
	now := time.Now()
	model, _ := m.Update(overlays.RangeChosenMsg{From: now.AddDate(0, 0, -5), To: now})
	m2 := model.(Model)
	if !m2.custom {
		t.Fatal("range message did not enter custom mode")
	}
	assertNoConfig(t)
}
