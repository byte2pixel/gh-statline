package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
)

// multiRepoDeps extends testDeps to three repos: acme/web holds one more
// merged PR by alice, so a working api-only filter shows her one PR where
// the unfiltered table showed two, and acme/infra holds nothing, so a
// partial selection is possible. Returns the deps and the api and web ids.
func multiRepoDeps(t *testing.T) (Deps, int64, int64) {
	t.Helper()
	deps := testDeps(t)
	team := deps.Team
	team.Repos = append(team.Repos,
		config.Repo{Owner: "acme", Name: "web"},
		config.Repo{Owner: "acme", Name: "infra"})
	teamID, repoIDs, err := deps.Store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	now := fixedNow.Unix()
	merged := now - 7200
	if err := deps.Store.SavePullRequests([]db.PullRequest{{
		ID: "PR_WEB", RepoID: repoIDs["acme/web"], Number: 1, Author: "alice", Title: "web thing",
		State: "MERGED", CreatedAt: now - 86400, MergedAt: &merged, UpdatedAt: now,
		Additions: 4, Deletions: 1, ChangedFiles: 1,
	}}); err != nil {
		t.Fatal(err)
	}
	deps.Team = team
	deps.TeamID = teamID
	deps.Cfg.Teams = []config.Team{team}
	deps.Targets = nil
	for _, r := range team.Repos {
		deps.Targets = append(deps.Targets,
			syncer.Target{Owner: r.Owner, Name: r.Name, RepoID: repoIDs[r.String()]})
	}
	return deps, repoIDs["acme/api"], repoIDs["acme/web"]
}

var keyRepos = tea.KeyPressMsg{Code: 'R', Text: "R", Mod: tea.ModShift}

// R opens the picker over the team's repos; esc closes it and leaves the
// filter alone.
func TestRepoKeyOpensAndClosesPicker(t *testing.T) {
	deps, _, _ := multiRepoDeps(t)
	model, _ := New(deps).Update(keyRepos)
	m := model.(Model)
	if m.overlay != overlayRepos {
		t.Fatalf("overlay = %d, want the repo picker", m.overlay)
	}
	if v := stripANSI(m.picker.View()); !strings.Contains(v, "acme/web") || !strings.Contains(v, "acme/infra") {
		t.Errorf("picker does not list the team's repos:\n%s", v)
	}
	model, cmd := m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("esc under the picker produced no message")
	}
	model, _ = m.Update(cmd())
	m = model.(Model)
	if m.overlay != overlayNone || m.repoIDs != nil {
		t.Errorf("after esc: overlay %d, repoIDs %v; want closed and unfiltered", m.overlay, m.repoIDs)
	}
}

// A team with no repos has nothing to pick; the key says so instead of
// opening an empty modal.
func TestRepoKeyWithoutReposFlashes(t *testing.T) {
	deps := testDeps(t)
	deps.Targets = nil
	model, _ := New(deps).Update(keyRepos)
	m := model.(Model)
	if m.overlay != overlayNone || m.flash == "" {
		t.Errorf("overlay %d, flash %q; want no modal and a note", m.overlay, m.flash)
	}
}

// A chosen selection scopes the next load. alice has one PR in each of api
// and web, so the api-only table shows one where the unfiltered showed two.
// It never reaches the config file.
func TestReposChosenFiltersTheLoad(t *testing.T) {
	deps, apiID, _ := multiRepoDeps(t)
	m := New(deps)
	m = pump(t, m, m.loadData(), func(m Model) bool { return m.teamStats.RowFor("alice").PRsOpened == 2 })

	model, cmd := m.Update(overlays.ReposChosenMsg{IDs: []int64{apiID}})
	m = model.(Model)
	if got := m.filter().RepoIDs; len(got) != 1 || got[0] != apiID {
		t.Fatalf("filter().RepoIDs = %v, want [%d]", got, apiID)
	}
	if m.overlay != overlayNone {
		t.Errorf("overlay = %d after choosing, want closed", m.overlay)
	}
	m = pump(t, m, cmd, func(m Model) bool { return m.teamStats.RowFor("alice").PRsOpened == 1 })
	assertNoConfig(t)
}

// Trends ignore the window but not the repo filter, and reloadAll leaves
// them out on purpose, so a chosen selection must reload them by name. The
// page reads "no data" until a trends load lands, whatever the load holds.
func TestReposChosenReloadsTrends(t *testing.T) {
	deps, apiID, _ := multiRepoDeps(t)
	m := New(deps)
	if v := stripANSI(m.trends.View()); !strings.Contains(v, "No data yet") {
		t.Fatalf("trends page has data before any load:\n%s", v)
	}
	model, cmd := m.Update(overlays.ReposChosenMsg{IDs: []int64{apiID}})
	pump(t, model.(Model), cmd, func(m Model) bool {
		return !strings.Contains(stripANSI(m.trends.View()), "No data yet")
	})
}

// A team switch drops the filter: the ids named the old team's repos.
func TestTeamSwitchClearsRepoFilter(t *testing.T) {
	deps, apiID, _ := multiRepoDeps(t)
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "docs"}},
	})
	model, _ := New(deps).Update(overlays.ReposChosenMsg{IDs: []int64{apiID}})
	m := model.(Model)
	if m.repoIDs == nil {
		t.Fatal("the filter did not take before the switch")
	}
	model, _ = m.activateTeam("others")
	m = model.(Model)
	if m.repoIDs != nil {
		t.Errorf("repoIDs = %v after a team switch, want nil", m.repoIDs)
	}
}

// The header names the scope: the repo when one is in, a count otherwise,
// and nothing at all while the filter is off.
func TestHeaderShowsRepoScope(t *testing.T) {
	deps, apiID, webID := multiRepoDeps(t)
	m := New(deps)
	header := func(m Model) string { return stripANSI(m.headerLine()) }
	if h := header(m); strings.Contains(h, "acme/") || strings.Contains(h, "repos") {
		t.Errorf("unfiltered header names a scope: %q", h)
	}
	model, _ := m.Update(overlays.ReposChosenMsg{IDs: []int64{apiID}})
	m = model.(Model)
	if h := header(m); !strings.Contains(h, "acme/api") {
		t.Errorf("one-repo header = %q, want acme/api in it", h)
	}
	model, _ = m.Update(overlays.ReposChosenMsg{IDs: []int64{apiID, webID}})
	m = model.(Model)
	if h := header(m); !strings.Contains(h, "2/3 repos") {
		t.Errorf("two-repo header = %q, want 2/3 repos", h)
	}
	model, _ = m.Update(overlays.ReposChosenMsg{IDs: nil})
	m = model.(Model)
	if h := header(m); strings.Contains(h, "acme/") || strings.Contains(h, "repos") {
		t.Errorf("header still names a scope after the filter came off: %q", h)
	}
}

// The export heading carries the scope: a pasted table that covers one
// repo must not read as the whole team's numbers.
func TestExportHeadingNamesFilteredRepos(t *testing.T) {
	deps, apiID, _ := multiRepoDeps(t)
	clip := &fakeClipboard{native: true}
	deps.Clipboard = clip.write
	model, _ := New(deps).Update(overlays.ReposChosenMsg{IDs: []int64{apiID}})
	pressExport(t, model.(Model), func(m Model) bool { return m.flash != "" })
	if !strings.HasPrefix(clip.text, "## testers · acme/api — ") {
		t.Errorf("export heading:\n%s\nwant it to open with the team and the repo", clip.text)
	}
}
