package app

import (
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/syncer"
)

func shiftS() tea.KeyPressMsg {
	return tea.KeyPressMsg{Code: 'S', Text: "S", Mod: tea.ModShift}
}

func failingReport() doctor.Report {
	synced := fixedNow.Unix() - 300
	msg := "fetching acme/api: Could not resolve to a Repository with the name 'acme/api'"
	return doctor.Build(config.Team{Name: "testers"}, []db.RepoSyncState{{
		SyncState: db.SyncState{RepoID: 1, LastSyncedAt: &synced, LastError: &msg},
		Owner:     "acme", Name: "api",
	}}, 0, false, fixedNow)
}

// S is not a tab, so it has to remember where it came from: opened from
// charts it returns to charts, not to the team tab.
func TestSyncStatusOpensAndReturnsToTheRouteItCameFrom(t *testing.T) {
	m := New(testDeps(t))
	m.nav.cur = routeCharts

	model, _ := m.Update(shiftS())
	m = model.(Model)
	if m.nav.cur != routeSyncStatus {
		t.Fatalf("route = %d, want sync status", m.nav.cur)
	}

	model, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = model.(Model)
	if m.nav.cur != routeCharts {
		t.Errorf("esc: route = %d, want charts", m.nav.cur)
	}
}

// The key that opens it closes it, the way t closes the team switcher.
func TestSyncStatusKeyToggles(t *testing.T) {
	m := New(testDeps(t))
	m.nav.cur = routeTrends

	for _, want := range []route{routeSyncStatus, routeTrends, routeSyncStatus, routeTrends} {
		model, _ := m.Update(shiftS())
		m = model.(Model)
		if m.nav.cur != want {
			t.Fatalf("route = %d, want %d", m.nav.cur, want)
		}
	}
}

// The badge is what makes a stale cache visible at all, so it must outrank
// the freshness line: "synced 4m ago" next to three dead repos is the exact
// reassurance this feature exists to withdraw.
func TestBadgeOutranksTheFreshnessLine(t *testing.T) {
	m := New(testDeps(t))
	m.width = 80
	m.lastSyncDone = fixedNow

	model, _ := m.Update(syncHealthMsg{rep: failingReport()})
	m = model.(Model)

	line := m.statusLine()
	if !strings.Contains(line, "1 repo(s) failing") {
		t.Errorf("status line = %q, want the failing count", line)
	}
	if !strings.Contains(line, "S") {
		t.Errorf("status line = %q, want it to name the key that opens the view", line)
	}
	if strings.Contains(line, "synced") {
		t.Errorf("status line = %q, must not claim freshness while repos are failing", line)
	}
}

// A healthy report leaves the status bar alone.
func TestNoBadgeWhenHealthy(t *testing.T) {
	m := New(testDeps(t))
	m.width = 80
	m.lastSyncDone = fixedNow

	rep := doctor.Build(config.Team{Name: "testers"}, nil, 0, false, fixedNow)
	model, _ := m.Update(syncHealthMsg{rep: rep})
	m = model.(Model)

	if line := m.statusLine(); strings.Contains(line, "failing") {
		t.Errorf("status line = %q, want no badge for a healthy report", line)
	}
}

// A repo failure no longer commandeers the status bar: the engine has
// already recorded it, and the view shows it whole. Raising it here hid the
// freshness line behind one truncated error until the next sync.
func TestRepoFailureDoesNotSetTheAppError(t *testing.T) {
	m := New(testDeps(t))
	ch := make(chan syncer.Event)
	m.syncCh = ch
	m.syncing = true

	model, _ := m.Update(syncEvMsg{
		ev: syncer.RepoDone{Repo: "acme/api", Err: errors.New("boom")},
		ch: ch,
	})
	if got := model.(Model).err; got != nil {
		t.Errorf("app error = %v, want nil: repo failures belong to the sync-status view", got)
	}
}

// The count comes from the database, not from this session's sync events,
// so a repo that has been failing since yesterday is a warning the moment
// the app opens.
func TestSyncHealthLoadsAtStartup(t *testing.T) {
	deps := testDeps(t)
	if err := deps.Store.SetSyncError(1, "stale failure from a previous run"); err != nil {
		t.Fatal(err)
	}
	m := New(deps)
	m = pump(t, m, m.loadSyncHealth(), func(m Model) bool { return m.syncHealth.Failing() > 0 })
	if got := m.syncHealth.Failing(); got != 1 {
		t.Errorf("failing = %d, want 1 from the recorded state", got)
	}
}

// A team switch invalidates the person drill-down, so the route esc would
// return to must not survive it.
func TestTeamSwitchResetsTheReturnRoute(t *testing.T) {
	deps := testDeps(t)
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name: "other", Org: "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "infra"}},
	})
	m := New(deps)
	m.nav.cur = routePerson
	model, _ := m.Update(shiftS()) // prev = person
	m = model.(Model)

	model, _ = m.activateTeam("other")
	m = model.(Model)
	if m.nav.cur != routeTeam || m.nav.prev != routeTeam {
		t.Errorf("router = (cur %d, prev %d), want both at the team tab", m.nav.cur, m.nav.prev)
	}
}

// The freshness line reads the injected clock like the rest of the app, so
// a pinned clock renders a pinned age. It used to call time.Now and
// time.Since directly, which left one line of the status bar following the
// wall clock while everything beside it followed Deps.Now.
func TestFreshnessLineUsesTheInjectedClock(t *testing.T) {
	m := New(testDeps(t))
	m.width = 80
	m.lastSyncDone = fixedNow.Add(-5 * time.Minute)

	if line := stripANSI(m.statusLine()); !strings.Contains(line, "synced 5m ago") {
		t.Errorf("status line = %q, want \"synced 5m ago\" from the pinned clock", line)
	}
}
