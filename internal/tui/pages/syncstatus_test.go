package pages

import (
	"strings"
	"testing"
	"time"

	lipgloss "charm.land/lipgloss/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/doctor"
	"github.com/byte2pixel/gh-statline/internal/metrics"
	"github.com/byte2pixel/gh-statline/internal/text"
	"github.com/byte2pixel/gh-statline/internal/tui/keys"
	"github.com/byte2pixel/gh-statline/internal/tui/theme"
)

var syncNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

func syncReport(t *testing.T) doctor.Report {
	t.Helper()
	synced := syncNow.Add(-4 * time.Minute).Unix()
	covers := time.Date(2026, time.January, 12, 0, 0, 0, 0, time.UTC).Unix()
	boom := "fetching acme/web: Could not resolve to a Repository with the name 'acme/web'"
	return doctor.Build(config.Team{Name: "testers"}, []db.RepoSyncState{
		{SyncState: db.SyncState{RepoID: 1, LastSyncedAt: &synced,
			WatermarkUpdated: &synced, BackfillUntil: &covers}, Owner: "acme", Name: "api"},
		{SyncState: db.SyncState{RepoID: 2, LastSyncedAt: &synced,
			WatermarkUpdated: &synced, BackfillUntil: &covers, LastError: &boom},
			Owner: "acme", Name: "web"},
		{SyncState: db.SyncState{RepoID: 3}, Owner: "acme", Name: "zebra"},
	}, covers, true, syncNow)
}

func newSyncPage(t *testing.T, w, h int) *SyncStatus {
	t.Helper()
	th := theme.New(true)
	p := NewSyncStatus(&th, keys.Default())
	p.SetSize(w, h)
	p.SetData(syncReport(t))
	return p
}

func TestSyncStatusRendersEveryRepoAndItsFailure(t *testing.T) {
	view := plain(newSyncPage(t, 110, 24).View())

	for _, want := range []string{
		"acme/api", "acme/web", "acme/zebra",
		"3 repos", "1 failing", "1 never synced",
		"4m ago", "2026-01-12", "never",
		"Could not resolve to a Repository",
		"fix the repo entry in config.yml",
	} {
		if !strings.Contains(view, want) {
			t.Errorf("view missing %q:\n%s", want, view)
		}
	}
}

// The error is the reason the view exists, so it wraps rather than being
// cut to a cell — and its continuation lines are indented so a wrapped
// error still reads as one block hanging off its row.
func TestSyncStatusWrapsAndIndentsLongErrors(t *testing.T) {
	th := theme.New(true)
	p := NewSyncStatus(&th, keys.Default())
	p.SetSize(60, 24)
	long := strings.Repeat("this error is very long and will not fit on one line. ", 4)
	rep := doctor.Build(config.Team{Name: "t"}, []db.RepoSyncState{
		{SyncState: db.SyncState{RepoID: 1, LastError: &long}, Owner: "acme", Name: "api"},
	}, 0, false, syncNow)
	p.SetData(rep)

	var errLines []string
	for _, l := range strings.Split(plain(p.View()), "\n") {
		if strings.Contains(l, "this error is very long") {
			errLines = append(errLines, l)
		}
	}
	if len(errLines) < 2 {
		t.Fatalf("expected the error to wrap over several lines, got %d:\n%s", len(errLines), p.View())
	}
	for i, l := range errLines {
		if !strings.HasPrefix(l, "   ") {
			t.Errorf("error line %d is not indented under its row: %q", i, l)
		}
		if w := lipgloss.Width(l); w > 60 {
			t.Errorf("error line %d is %d cells wide, past the page width: %q", i, w, l)
		}
	}
}

// Narrow terminals drop columns rather than wrapping rows: which repo and
// whether it is broken are what survive.
func TestSyncStatusDropsColumnsBeforeWrapping(t *testing.T) {
	for _, tc := range []struct {
		width               int
		wantCovers, wantNew bool
	}{
		{110, true, true},
		{60, true, false},
		{50, false, false},
	} {
		p := newSyncPage(t, tc.width, 24)
		view := plain(p.View())
		if got := strings.Contains(view, "COVERS SINCE"); got != tc.wantCovers {
			t.Errorf("width %d: covers column shown = %v, want %v\n%s", tc.width, got, tc.wantCovers, view)
		}
		if got := strings.Contains(view, "NEWEST PR"); got != tc.wantNew {
			t.Errorf("width %d: newest column shown = %v, want %v\n%s", tc.width, got, tc.wantNew, view)
		}
		// The repo name and the verdict never drop.
		for _, want := range []string{"acme/api", "failing"} {
			if !strings.Contains(view, want) {
				t.Errorf("width %d: view lost %q:\n%s", tc.width, want, view)
			}
		}
	}
}

// Every rendered line has to fit the page, or the frame tears on the rows
// that overflow.
func TestSyncStatusLinesFitTheWidth(t *testing.T) {
	const width = 80
	for _, l := range strings.Split(newSyncPage(t, width, 24).View(), "\n") {
		if w := lipgloss.Width(l); w > width {
			t.Errorf("line is %d cells wide, want <= %d: %q", w, width, plain(l))
		}
	}
}

// A wide repo name must not shear the columns to its right.
func TestCellFitIsExactInDisplayCells(t *testing.T) {
	for _, tc := range []struct{ in string }{
		{"acme/api"}, {"acme/a-very-long-repository-name-indeed"}, {"漢字/リポジトリ"},
	} {
		if got := lipgloss.Width(cellFit(tc.in, 12)); got != 12 {
			t.Errorf("cellFit(%q, 12) is %d cells, want 12", tc.in, got)
		}
	}
}

func TestSyncStatusEmptyBeforeData(t *testing.T) {
	th := theme.New(true)
	p := NewSyncStatus(&th, keys.Default())
	p.SetSize(80, 24)
	if got := p.View(); got != "" {
		t.Errorf("view before data = %q, want empty", got)
	}
	if got := p.Failing(); got != 0 {
		t.Errorf("Failing() = %d before data, want 0", got)
	}
}

// The export carries the errors in full: a table cell would truncate the
// one thing worth pasting into an issue.
func TestSyncStatusExportCarriesTheFailures(t *testing.T) {
	md := newSyncPage(t, 110, 24).Export("testers", metrics.Window{})
	for _, want := range []string{
		"## Sync status — testers", "| acme/api |", "| acme/zebra |",
		"### Failures", "Could not resolve to a Repository", "config.yml",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("export missing %q:\n%s", want, md)
		}
	}
}

// The layout used to forget the one-column left margin, so the viewport
// clipped the right edge and the widest status — "never synced", on the
// repo most worth noticing — arrived as "never synce". The frame test
// above cannot catch it: the viewport clips to the page width, so a line
// budgeted one cell too wide comes back the right size with a character
// missing.
func TestSyncStatusStatusColumnSurvivesEveryWidth(t *testing.T) {
	for w := 41; w <= 140; w++ {
		if view := plain(newSyncPage(t, w, 24).View()); !strings.Contains(view, "never synced") {
			t.Fatalf("width %d clipped the status column:\n%s", w, view)
		}
	}
}

// layout reserves colStatus for the status column, and the renderer fits
// every cell to it, so a status wider than the reservation would be
// truncated rather than tearing the line. Nothing should ever reach that
// truncation: a fourth Status value that does not fit fails here instead,
// where the message says what to do about it.
func TestStatusStringsFitTheirColumn(t *testing.T) {
	for _, st := range []doctor.Status{doctor.StatusOK, doctor.StatusNeverSynced, doctor.StatusFailing} {
		if w := text.Width(st.String()); w > colStatus {
			t.Errorf("status %q is %d cells, past the %d colStatus reserves; widen the constant",
				st, w, colStatus)
		}
	}
}
