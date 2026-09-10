package app

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/byte2pixel/gh-statline/internal/syncer"
)

var keySync = tea.KeyPressMsg{Code: 's', Text: "s"}

// s during a sync cancels it. The engine's context is cancelled at once,
// the footer says what s now does, and the run is left to wind down: its
// end reloads the pages, flashes the cancellation, and does not count as a
// sync for the freshness line.
func TestSyncKeyCancelsRunningSync(t *testing.T) {
	deps := testDeps(t)
	doer := newBlockingDoer()
	deps.Doer = doer
	m := startInFlightSync(t, New(deps), doer)
	if h := m.keys.Sync.Help(); h.Desc != "cancel sync" {
		t.Errorf("footer while syncing = %q, want cancel sync", h.Desc)
	}
	stream := m.syncCh

	model, cmd := m.Update(keySync)
	m = model.(Model)
	if cmd != nil {
		t.Fatal("s during a sync returned a command: a second engine launched")
	}
	if !m.cancelling || m.syncStatus != "cancelling sync…" {
		t.Fatalf("after s: cancelling=%v status=%q", m.cancelling, m.syncStatus)
	}
	select {
	case <-doer.cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("s did not cancel the in-flight request's context")
	}

	// Progress the run still reports must not overwrite the cancel line.
	model, _ = m.Update(syncEvMsg{ev: syncer.RepoPage{Repo: "acme/api", PRs: 3}, ch: stream})
	m = model.(Model)
	if m.syncStatus != "cancelling sync…" {
		t.Errorf("a late progress event rewrote the status to %q", m.syncStatus)
	}

	m = pump(t, m, waitForSync(m.syncCh), func(m Model) bool { return !m.syncing })
	if m.flash != "sync cancelled" {
		t.Errorf("flash = %q, want sync cancelled", m.flash)
	}
	if !m.lastSyncDone.IsZero() {
		t.Error("a cancelled run set the freshness line")
	}
	if m.cancelling || m.keys.Sync.Help().Desc != "sync" {
		t.Errorf("after the run ended: cancelling=%v footer=%q", m.cancelling, m.keys.Sync.Help().Desc)
	}
}

// Pressing s again while the run winds down neither restarts it nor
// cancels twice: the stream stays the one being drained.
func TestSecondSyncKeyWhileCancellingIsNoOp(t *testing.T) {
	deps := testDeps(t)
	doer := newBlockingDoer()
	deps.Doer = doer
	m := startInFlightSync(t, New(deps), doer)
	model, _ := m.Update(keySync)
	m = model.(Model)
	stream := m.syncCh

	model, cmd := m.Update(keySync)
	m = model.(Model)
	if cmd != nil || m.syncCh != stream || !m.syncing {
		t.Fatalf("second s: cmd=%v sameStream=%v syncing=%v; want no command, same stream, still winding down",
			cmd != nil, m.syncCh == stream, m.syncing)
	}
	pump(t, m, waitForSync(m.syncCh), func(m Model) bool { return !m.syncing })
}

// A run that completes on its own still sets the freshness line and shows
// no cancellation, so the cancelled path has not leaked into the normal
// one.
func TestNaturalCompletionStillCountsAsSync(t *testing.T) {
	m := New(testDeps(t))
	model, cmd := m.Update(keySync)
	m = pump(t, model.(Model), cmd, func(m Model) bool { return !m.syncing })
	if m.lastSyncDone.IsZero() {
		t.Error("a completed run did not set the freshness line")
	}
	if m.flash != "" {
		t.Errorf("flash = %q after a normal completion, want none", m.flash)
	}
}
