package metrics

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

func defaultFilter(teamID int64) Filter {
	return Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}
}

// The one-shot load must fill every dataset the dashboard renders, all
// from the same window and filter.
func TestLoadDashboardFillsEveryDataset(t *testing.T) {
	store, teamID, _ := fixture(t)
	w := LastDays(30, fixedNow)
	d, err := LoadDashboard(store.DB, defaultFilter(teamID), w, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Rows) != 2 {
		t.Errorf("rows = %d, want 2 (alice and bob; hidden and bot members excluded)", len(d.Rows))
	}
	if d.BucketDur != BucketSize(w) {
		t.Errorf("BucketDur = %v, want %v", d.BucketDur, BucketSize(w))
	}
	if len(d.Buckets) == 0 || len(d.Trend) == 0 || len(d.Matrix.Logins) == 0 || d.Punch.Total == 0 {
		t.Errorf("a chart dataset came back empty: %+v", d)
	}
	if d.Aging.Total != 1 { // PR2 is the only visible open PR
		t.Errorf("Aging.Total = %d, want 1", d.Aging.Total)
	}
	// Current-window medians: cycle over PR1 (1d) and PR3 (2d), TTFR over
	// bob@+12h, bob@+1d, alice@+1d — lower-middle medians.
	if d.Tiles.Cycle != 24*time.Hour || d.Tiles.TTFR != 24*time.Hour {
		t.Errorf("medians = %v / %v, want 24h / 24h", d.Tiles.Cycle, d.Tiles.TTFR)
	}
}

// The previous-window comparison must stay honest: it switches on only
// when the backfill provably covers the previous window (CoverageFloor),
// never on missing or too-shallow sync coverage.
func TestLoadDashboardPrevWindowGating(t *testing.T) {
	w := LastDays(30, fixedNow)
	prevW := PrevWindow(w)
	shallow := prevW.Start + day // sync never reached the previous window
	deep := prevW.Start - day    // sync covers it whole

	cases := []struct {
		name     string
		backfill *int64 // nil = no completed sync recorded
		hasPrev  bool
	}{
		{"no sync state", nil, false},
		{"backfill misses the previous window", &shallow, false},
		{"backfill covers the previous window", &deep, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store, teamID, repoID := fixture(t)
			if tc.backfill != nil {
				now := fixedNow.Unix()
				st := db.SyncState{RepoID: repoID, WatermarkUpdated: &now,
					BackfillUntil: tc.backfill, LastSyncedAt: &now}
				if err := store.SetSyncState(st); err != nil {
					t.Fatal(err)
				}
			}
			d, err := LoadDashboard(store.DB, defaultFilter(teamID), w, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if d.Tiles.HasPrev != tc.hasPrev {
				t.Fatalf("HasPrev = %v, want %v", d.Tiles.HasPrev, tc.hasPrev)
			}
			if !tc.hasPrev {
				if d.Tiles.PrevOpened != 0 || d.Tiles.PrevCycle != 0 {
					t.Errorf("prev stats set without coverage: %+v", d.Tiles)
				}
				return
			}
			// The previous window holds exactly PR4: alice opened it, merged
			// after 5 days, no reviews or comments.
			if d.Tiles.PrevOpened != 1 || d.Tiles.PrevMerged != 1 ||
				d.Tiles.PrevReviews != 0 || d.Tiles.PrevComments != 0 {
				t.Errorf("prev counts = %+v, want 1 opened, 1 merged, 0 reviews, 0 comments", d.Tiles)
			}
			if d.Tiles.PrevCycle != 120*time.Hour {
				t.Errorf("PrevCycle = %v, want 120h (PR4's 5-day cycle)", d.Tiles.PrevCycle)
			}
			if d.Tiles.PrevTTFR != 0 {
				t.Errorf("PrevTTFR = %v, want 0 (no reviews back then)", d.Tiles.PrevTTFR)
			}
		})
	}
}
