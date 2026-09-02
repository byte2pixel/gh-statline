package app

import (
	"context"
	"slices"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/syncer"
	"github.com/byte2pixel/gh-statline/internal/tui/overlays"
)

// blockingDoer parks every request until release closes or the request's
// context is cancelled, making "sync in flight" a deterministic state.
type blockingDoer struct {
	started   chan struct{} // one send per request as it begins
	release   chan struct{} // close to let requests answer with an empty page
	cancelled chan struct{} // one send per request that died by context
}

func newBlockingDoer() *blockingDoer {
	return &blockingDoer{
		started:   make(chan struct{}, 8),
		release:   make(chan struct{}),
		cancelled: make(chan struct{}, 8),
	}
}

func (d *blockingDoer) DoWithContext(ctx context.Context, query string, vars map[string]interface{}, resp interface{}) error {
	d.started <- struct{}{}
	select {
	case <-ctx.Done():
		d.cancelled <- struct{}{}
		return ctx.Err()
	case <-d.release:
		return emptyPageDoer{}.DoWithContext(ctx, query, vars, resp)
	}
}

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

// pumpUntilQuit executes commands until one yields tea.QuitMsg, failing the
// test if the queue drains or the deadline passes first.
func pumpUntilQuit(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	queue := []tea.Cmd{cmd}
	deadline := time.Now().Add(10 * time.Second)
	for len(queue) > 0 {
		if time.Now().After(deadline) {
			t.Fatal("pumpUntilQuit: no QuitMsg before the deadline")
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
		if _, ok := msg.(tea.QuitMsg); ok {
			return m
		}
		model, next := m.Update(msg)
		m = model.(Model)
		if next != nil {
			queue = append(queue, next)
		}
	}
	t.Fatal("pumpUntilQuit: command queue drained without a QuitMsg")
	return m
}

// startInFlightSync drives Init's commands through Update the way the
// runtime would and waits until the engine has a request on the wire. The
// blocked doer keeps the sync in flight until the caller decides its fate.
func startInFlightSync(t *testing.T, m Model, doer *blockingDoer) Model {
	t.Helper()
	m = pump(t, m, m.Init(), func(m Model) bool { return m.syncing })
	select {
	case <-doer.started:
	case <-time.After(5 * time.Second):
		t.Fatal("engine never issued a request")
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

// The spinner chain lives exactly as long as a sync: startSync begins it on
// the live model (issue #36 removed the restart-on-RepoStarted hack that
// compensated for Init's discarded copy), ticks reschedule while syncing,
// and an idle tick lets the chain die.
func TestSpinnerTickLifecycle(t *testing.T) {
	m := New(testDeps(t))

	// Idle: the tick advances the frame but does not reschedule.
	model, cmd := m.Update(m.spin.Tick())
	m2 := model.(Model)
	if cmd != nil {
		t.Error("idle tick should not reschedule")
	}

	if cmd = m2.startSync(); cmd == nil {
		t.Fatal("startSync returned no command")
	}
	if !m2.syncing {
		t.Fatal("startSync did not mark the live model syncing")
	}

	// While syncing, ticks reschedule.
	_, cmd = m2.Update(m2.spin.Tick())
	if cmd == nil {
		t.Error("tick while syncing must reschedule")
	}
}

// Regression for gh issue #36 (double-start window): Init used to flip
// m.syncing on a discarded copy of the model, so pressing s before the first
// RepoStarted event launched a second engine over the same store.
func TestInitStartsSyncOnLiveModel(t *testing.T) {
	deps := testDeps(t)
	doer := newBlockingDoer()
	deps.Doer = doer
	m := startInFlightSync(t, New(deps), doer)

	model, cmd := m.Update(tea.KeyPressMsg{Code: 's', Text: "s"})
	m = model.(Model)
	if cmd != nil {
		t.Fatal("s during the startup window returned a command: a second engine launched")
	}
	if !m.syncing {
		t.Fatal("the s press ended the running sync")
	}

	// Let the sync finish before the test closes the database.
	close(doer.release)
	pump(t, m, waitForSync(m.syncCh), func(m Model) bool { return !m.syncing })
}

// Regression for gh issue #36 (no cancellation): quitting mid-sync must
// cancel the engine's context and hold the quit until the event stream
// closes, so the deferred DB close in cmd/root never races a write.
func TestQuitCancelsRunningSync(t *testing.T) {
	deps := testDeps(t)
	doer := newBlockingDoer()
	deps.Doer = doer
	m := startInFlightSync(t, New(deps), doer)

	model, cmd := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = model.(Model)
	if !m.quitting {
		t.Fatal("q during a sync did not enter the quitting state")
	}
	if m.syncStatus != "cancelling sync…" {
		t.Errorf("status = %q, want cancelling sync…", m.syncStatus)
	}
	_ = cmd // the fallback timer; left unexecuted so the test stays fast

	select {
	case <-doer.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("quit did not cancel the in-flight request's context")
	}

	// Drain the stream the way the runtime would; the quit must arrive once
	// the run ends.
	pumpUntilQuit(t, m, waitForSync(m.syncCh))
}

// Regression for gh issue #36: switching teams used to leave the old team's
// sync running, and the syncing guard then blocked the new team's sync
// entirely. The switch must cancel the old run, start a fresh one, and
// ignore whatever the old stream still delivers.
func TestTeamSwitchCancelsOldSyncAndStartsNew(t *testing.T) {
	deps := testDeps(t)
	doer := newBlockingDoer()
	deps.Doer = doer
	deps.Cfg.Teams = append(deps.Cfg.Teams, config.Team{
		Name:    "others",
		Org:     "acme",
		Members: []config.Member{{Login: "carol"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "web"}},
	})
	m := startInFlightSync(t, New(deps), doer)
	oldCh := m.syncCh

	model, cmd := m.Update(overlays.TeamChosenMsg{Name: "others"})
	m = model.(Model)
	if cmd == nil {
		t.Fatal("team switch returned no command")
	}
	select {
	case <-doer.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("team switch did not cancel the old team's sync")
	}
	if !m.syncing {
		t.Fatal("team switch did not start a sync for the new team")
	}
	if m.syncCh == oldCh {
		t.Fatal("team switch kept the old event stream")
	}

	// Late messages from the cancelled stream must not flip state under the
	// new run.
	model, _ = m.Update(syncEvMsg{ev: syncer.Complete{}, ch: oldCh})
	m = model.(Model)
	model, _ = m.Update(syncClosedMsg{ch: oldCh})
	m = model.(Model)
	if !m.syncing {
		t.Fatal("a stale event from the cancelled sync ended the new run")
	}

	close(doer.release)
	pump(t, m, cmd, func(m Model) bool { return !m.syncing })
}
