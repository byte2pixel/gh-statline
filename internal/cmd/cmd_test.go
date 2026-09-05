package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/cli/go-gh/v2/pkg/api"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/gh"
	"github.com/byte2pixel/gh-statline/internal/tui/app"
	"github.com/byte2pixel/gh-statline/internal/tui/wizard"
)

// isolate points config and cache at temp paths and restores the seams
// and the cobra flag variables afterwards; every command test starts here.
func isolate(t *testing.T) {
	t.Helper()
	t.Setenv("STATLINE_CONFIG", filepath.Join(t.TempDir(), "config.yml"))
	t.Setenv("STATLINE_DB", filepath.Join(t.TempDir(), "statline.db"))
	origRun, origClient := runProgram, newClient
	t.Cleanup(func() {
		runProgram, newClient = origRun, origClient
		rootTeam, syncTeam, syncBackfill = "", "", 0
	})
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Teams = []config.Team{{
		Name: "testers", Org: "acme",
		Members: []config.Member{{Login: "alice"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	}}
	cfg.DefaultTeam = "testers"
	return cfg
}

func writeConfig(t *testing.T, cfg config.Config) {
	t.Helper()
	if err := config.Save(cfg); err != nil {
		t.Fatal(err)
	}
}

// emptyDoer answers every GraphQL call with one empty PR page, so a sync
// completes at once without a network.
type emptyDoer struct{}

func (emptyDoer) DoWithContext(_ context.Context, _ string, _ map[string]interface{}, resp interface{}) error {
	return json.Unmarshal([]byte(`{
		"rateLimit": {"cost": 1, "remaining": 5000, "resetAt": "2030-01-01T00:00:00Z"},
		"repository": {"pullRequests": {"pageInfo": {"hasNextPage": false, "endCursor": ""}, "nodes": []}}
	}`), resp)
}

// failingDoer answers every call with a permanent error, so the walk fails
// without entering the retry ladder.
type failingDoer struct{}

func (failingDoer) DoWithContext(context.Context, string, map[string]interface{}, interface{}) error {
	return &api.HTTPError{StatusCode: 404, Message: "repository not found"}
}

func fakeClient(d gh.Doer) func() (gh.Doer, error) {
	return func() (gh.Doer, error) { return d, nil }
}

func TestBootstrap(t *testing.T) {
	t.Run("missing config routes to first run", func(t *testing.T) {
		isolate(t)
		if _, err := bootstrap(""); !errors.Is(err, config.ErrNotFound) {
			t.Fatalf("err = %v, want config.ErrNotFound", err)
		}
	})

	t.Run("unknown team", func(t *testing.T) {
		isolate(t)
		writeConfig(t, testConfig())
		_, err := bootstrap("nope")
		if err == nil || !strings.Contains(err.Error(), `"nope"`) {
			t.Fatalf("err = %v, want the unknown team named", err)
		}
	})

	t.Run("mirrors the default team and builds sync targets", func(t *testing.T) {
		isolate(t)
		writeConfig(t, testConfig())
		env, err := bootstrap("")
		if err != nil {
			t.Fatal(err)
		}
		defer env.Close()
		if env.Team.Name != "testers" || env.TeamID == 0 {
			t.Errorf("team = %q (id %d), want testers with a mirrored id", env.Team.Name, env.TeamID)
		}
		if len(env.Targets) != 1 || env.Targets[0].String() != "acme/api" || env.Targets[0].RepoID == 0 {
			t.Errorf("targets = %+v, want acme/api with a repo id", env.Targets)
		}
		var members int
		if err := env.DB.QueryRow(`SELECT COUNT(*) FROM team_members WHERE team_id = ?`, env.TeamID).Scan(&members); err != nil {
			t.Fatal(err)
		}
		if members != 1 {
			t.Errorf("mirrored members = %d, want 1", members)
		}
	})
}

// First run: no config yet, so the root command runs the wizard, saves the
// team it produced, and only then launches the dashboard.
func TestRootFirstRunWizardThenApp(t *testing.T) {
	isolate(t)
	newClient = fakeClient(emptyDoer{})
	var launched []string
	runProgram = func(m tea.Model) (tea.Model, error) {
		switch m := m.(type) {
		case wizard.Model:
			launched = append(launched, "wizard")
			m.Result = &config.Team{
				Name: "fresh", Org: "acme",
				Members: []config.Member{{Login: "alice"}},
				Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
			}
			return m, nil
		case app.Model:
			launched = append(launched, "app")
			return m, nil
		}
		t.Fatalf("unexpected program model %T", m)
		return m, nil
	}

	rootCmd.SetArgs([]string{})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(launched, ","); got != "wizard,app" {
		t.Fatalf("programs launched = %q, want wizard,app", got)
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.DefaultTeam != "fresh" || len(cfg.Teams) != 1 || cfg.Teams[0].Name != "fresh" {
		t.Errorf("saved config = %+v, want the wizard's team as the default", cfg)
	}
}

func TestRootAbortedWizardSavesNothing(t *testing.T) {
	isolate(t)
	newClient = fakeClient(emptyDoer{})
	runProgram = func(m tea.Model) (tea.Model, error) { return m, nil } // quit without a team

	rootCmd.SetArgs([]string{})
	err := rootCmd.Execute()
	if err == nil || err.Error() != "setup aborted" {
		t.Fatalf("err = %v, want \"setup aborted\"", err)
	}
	if _, err := config.Load(); !errors.Is(err, config.ErrNotFound) {
		t.Errorf("config after an aborted setup: err = %v, want none written", err)
	}
}

func TestRootLaunchesAppWithExistingConfig(t *testing.T) {
	isolate(t)
	writeConfig(t, testConfig())
	newClient = fakeClient(emptyDoer{})
	var got tea.Model
	runProgram = func(m tea.Model) (tea.Model, error) {
		got = m
		return m, nil
	}

	rootCmd.SetArgs([]string{"--team", "testers"})
	if err := rootCmd.Execute(); err != nil {
		t.Fatal(err)
	}
	if _, ok := got.(app.Model); !ok {
		t.Fatalf("launched %T, want the dashboard", got)
	}
}

// The headless sync must exit non-zero when any repo fails, so cron and
// scripts notice a stale cache instead of trusting old numbers.
func TestSyncExitStatus(t *testing.T) {
	t.Run("a failed repo fails the command", func(t *testing.T) {
		isolate(t)
		writeConfig(t, testConfig())
		newClient = fakeClient(failingDoer{})
		rootCmd.SetArgs([]string{"sync"})
		err := rootCmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "1 repo(s) failed to sync") {
			t.Fatalf("err = %v, want the failed-repo count", err)
		}
	})

	t.Run("a clean walk succeeds", func(t *testing.T) {
		isolate(t)
		writeConfig(t, testConfig())
		newClient = fakeClient(emptyDoer{})
		rootCmd.SetArgs([]string{"sync"})
		if err := rootCmd.Execute(); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("a local-only team is refused before any client is built", func(t *testing.T) {
		isolate(t)
		cfg := testConfig()
		cfg.Teams[0].NoSync = true
		writeConfig(t, cfg)
		newClient = func() (gh.Doer, error) {
			t.Fatal("client built for a no_sync team")
			return nil, nil
		}
		rootCmd.SetArgs([]string{"sync"})
		err := rootCmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "no_sync") {
			t.Fatalf("err = %v, want the no_sync refusal", err)
		}
	})
}
