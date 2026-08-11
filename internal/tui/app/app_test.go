package app

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/syncer"
)

// emptyPageDoer answers every GraphQL call with a single empty PR page so
// the auto-sync on startup completes instantly without a network.
type emptyPageDoer struct{}

func (emptyPageDoer) DoWithContext(_ context.Context, _ string, _ map[string]interface{}, resp interface{}) error {
	return json.Unmarshal([]byte(`{
		"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": "2030-01-01T00:00:00Z"},
		"repository": {"pullRequests": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}}
	}`), resp)
}

func testDeps(t *testing.T) Deps {
	t.Helper()
	// The app persists team/window/sort changes to the config path; keep
	// tests away from the developer's real config.
	t.Setenv("STATLINE_CONFIG", filepath.Join(t.TempDir(), "config.yml"))
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })

	store := db.NewStore(sqldb)
	team := config.Team{
		Name:    "testers",
		Org:     "acme",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	}
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	repoID := repoIDs["acme/api"]

	now := time.Now().Unix()
	merged := now - 3600
	err = store.SavePullRequests([]db.PullRequest{
		{
			ID: "PR_1", RepoID: repoID, Number: 1, Author: "alice", Title: "add thing",
			State: "MERGED", CreatedAt: now - 86400, MergedAt: &merged, UpdatedAt: now,
			Additions: 10, Deletions: 2, ChangedFiles: 1,
			Reviews: []db.Review{
				{ID: "RV_1", Author: "bob", State: "APPROVED", SubmittedAt: now - 7200, CommentCount: 3},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Teams = []config.Team{team}
	cfg.DefaultTeam = "testers"

	return Deps{
		DB:      sqldb,
		Store:   store,
		Cfg:     cfg,
		Team:    team,
		TeamID:  teamID,
		Targets: []syncer.Target{{Owner: "acme", Name: "api", RepoID: repoID}},
		Doer:    emptyPageDoer{},
	}
}

func TestChartsGridNavigation(t *testing.T) {
	tm := teatest.NewTestModel(t, New(testDeps(t)), teatest.WithInitialTermSize(110, 30))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alice"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: '2', Text: "2"}) // charts tab
	tm.Send(tea.KeyPressMsg{Code: 'l', Text: "l"}) // focus card 1
	tm.Send(tea.KeyPressMsg{Code: 'j', Text: "j"}) // focus card 4 (3-col grid)
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEnter})   // fullscreen
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if !ok {
		t.Fatal("unexpected final model type")
	}
	if final.route != routeCharts {
		t.Errorf("route = %d, want charts", final.route)
	}
	if got := final.charts.Focus(); got != 4 {
		t.Errorf("focus = %d, want 4", got)
	}
	if !final.charts.Fullscreen() {
		t.Error("enter did not fullscreen the focused card")
	}
}

func TestChartsFullscreenEscReturnsToGrid(t *testing.T) {
	tm := teatest.NewTestModel(t, New(testDeps(t)), teatest.WithInitialTermSize(110, 30))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alice"))
	}, teatest.WithDuration(5*time.Second))

	tm.Send(tea.KeyPressMsg{Code: '2', Text: "2"})
	tm.Send(tea.KeyPressMsg{Code: 'f', Text: "f"})
	tm.Send(tea.KeyPressMsg{Code: tea.KeyEscape})
	tm.Send(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl})

	final := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if final.charts.Fullscreen() {
		t.Error("esc did not return to the grid")
	}
	if final.route != routeCharts {
		t.Errorf("route = %d, want charts (esc must not leave the tab)", final.route)
	}
}

func TestTeamStatsRendersData(t *testing.T) {
	tm := teatest.NewTestModel(t, New(testDeps(t)), teatest.WithInitialTermSize(110, 30))

	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("alice")) && bytes.Contains(b, []byte("bob"))
	}, teatest.WithDuration(5*time.Second))

	// Cycle the window and flip sort to exercise the key path.
	tm.Send(tea.KeyPressMsg{Code: 'w', Text: "w"})
	tm.Send(tea.KeyPressMsg{Code: '-', Text: "-"})

	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})
	tm.WaitFinished(t, teatest.WithFinalTimeout(5*time.Second))
}

// TestNewWiresZones guards issue #16: every page doing zone hit-testing
// must receive the shared zone manager, or its clicks silently miss.
func TestNewWiresZones(t *testing.T) {
	m := New(testDeps(t))
	if m.z == nil {
		t.Fatal("app has no zone manager")
	}
	if m.teamStats.Zones != m.z {
		t.Error("team stats page missing the zone manager")
	}
	if m.charts.Zones != m.z {
		t.Error("charts page missing the zone manager")
	}
	if m.trends.Zones != m.z {
		t.Error("trends page missing the zone manager")
	}
}
