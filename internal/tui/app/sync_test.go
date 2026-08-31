package app

import (
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
)

// pump executes commands and feeds their messages back into Update until
// done(m) reports true, simulating the Bubble Tea runtime deterministically.
func pump(t *testing.T, m Model, cmd tea.Cmd, done func(Model) bool) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	deadline := time.Now().Add(10 * time.Second)
	for len(queue) > 0 && !done(m) {
		if time.Now().After(deadline) {
			t.Fatal("pump: expected state not reached before deadline")
		}
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if msg == nil {
			continue
		}
		if batch, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, batch...)
			continue
		}
		model, next := m.Update(msg)
		m = model.(Model)
		if next != nil {
			queue = append(queue, next)
		}
	}
	if !done(m) {
		t.Fatal("pump: command queue drained before the expected state was reached")
	}
	return m
}

func TestStartSyncOwnsGuard(t *testing.T) {
	m := New(testDeps(t))
	cmd := m.startSync()
	if cmd == nil {
		t.Fatal("first startSync returned nil cmd")
	}
	if !m.syncing || m.syncStatus != "starting sync…" {
		t.Errorf("startSync did not set state: syncing=%v status=%q", m.syncing, m.syncStatus)
	}
	if again := m.startSync(); again != nil {
		t.Error("second startSync while syncing should return nil")
	}
}

func TestStartSyncRespectsNoSync(t *testing.T) {
	deps := testDeps(t)
	deps.Team.NoSync = true
	m := New(deps)
	if cmd := m.startSync(); cmd != nil {
		t.Error("startSync on a no_sync team should return nil")
	}
	if m.syncing {
		t.Error("no_sync team must never enter syncing state")
	}
}

// Regression for gh issue #1: pressing s must launch a real sync that
// completes, not wedge at "starting sync…" forever.
func TestManualSyncKeyCompletes(t *testing.T) {
	m := New(testDeps(t))
	model, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m2 := model.(Model)
	if !m2.syncing {
		t.Fatal("s key did not start a sync")
	}
	if cmd == nil {
		t.Fatal("s key returned no command — sync goroutine never launched")
	}
	m2 = pump(t, m2, cmd, func(m Model) bool { return !m.syncing })
	if m2.syncStatus != "" {
		t.Errorf("after completion syncStatus = %q, want empty", m2.syncStatus)
	}
}

func TestNoSyncKeyFlashes(t *testing.T) {
	deps := testDeps(t)
	deps.Team.NoSync = true
	m := New(deps)
	model, _ := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m2 := model.(Model)
	if m2.syncing {
		t.Error("s on a no_sync team must not sync")
	}
	if m2.flash == "" {
		t.Error("expected a flash message explaining sync is disabled")
	}
}

// Regression for the latent variant of gh issue #1: switching teams must
// start a sync that completes.
func TestTeamSwitchStartsSync(t *testing.T) {
	deps := testDeps(t)
	other := config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
	}
	deps.Cfg.Teams = append(deps.Cfg.Teams, other)
	m := New(deps)
	model, cmd := m.Update(overlays.TeamChosenMsg{Name: "others"})
	m2 := model.(Model)
	if !m2.syncing {
		t.Fatal("team switch did not start a sync")
	}
	if cmd == nil {
		t.Fatal("team switch returned no command")
	}
	m2 = pump(t, m2, cmd, func(m Model) bool { return !m.syncing })
	if m2.deps.Team.Name != "others" {
		t.Errorf("active team = %q, want others", m2.deps.Team.Name)
	}
}

// Regression for gh issue #32: startSync hands its goroutine the Targets
// slice, so activateTeam must build a new one. Rebuilding in place rewrites
// the elements a running sync is ranging, pairing the new team's repo names
// with the old team's repo IDs.
func TestTeamSwitchLeavesRunningSyncTargetsIntact(t *testing.T) {
	deps := testDeps(t)
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
	})
	m := New(deps)

	// What a sync in flight for the current team would still be iterating.
	inFlight := m.deps.Targets
	want := slices.Clone(inFlight)

	model, cmd := m.Update(overlays.TeamChosenMsg{Name: "others"})
	m2 := model.(Model)

	if !slices.Equal(inFlight, want) {
		t.Errorf("team switch rewrote the running sync's targets: got %+v, want %+v", inFlight, want)
	}
	if len(m2.deps.Targets) != 1 || m2.deps.Targets[0].Name != "web" {
		t.Errorf("targets after switch = %+v, want one entry for acme/web", m2.deps.Targets)
	}
	// Let the sync finish before the test closes the database.
	pump(t, m2, cmd, func(m Model) bool { return !m.syncing })
}

// Regression for gh issue #3: at startup the first spinner tick arrives
// before any sync event flips m.syncing; the chain must be restartable so
// the spinner animates during the initial sync.
func TestSpinnerTickLifecycle(t *testing.T) {
	m := New(testDeps(t))

	// Idle: the tick advances the frame but does not reschedule.
	model, cmd := m.Update(m.spin.Tick())
	m2 := model.(Model)
	if cmd != nil {
		t.Error("idle tick should not reschedule")
	}

	// A RepoStarted event on an idle model restarts the chain.
	ch := make(chan syncer.Event, 1)
	model, cmd = m2.Update(syncEvMsg{ev: syncer.RepoStarted{Repo: "acme/api"}, ch: ch})
	m2 = model.(Model)
	if !m2.syncing {
		t.Fatal("RepoStarted did not mark syncing")
	}
	if cmd == nil {
		t.Fatal("RepoStarted on an idle model must restart the spinner tick chain")
	}

	// While syncing, ticks reschedule.
	_, cmd = m2.Update(m2.spin.Tick())
	if cmd == nil {
		t.Error("tick while syncing must reschedule")
	}
}
