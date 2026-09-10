package metrics

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
)

// The drill-down lists repos busiest first and breaks ties by name, so a
// member whose work is spread evenly reads the same way on every load.
func TestSortBreakdownsTiebreak(t *testing.T) {
	bs := []RepoBreakdown{
		{Repo: "acme/web", PRsOpened: 2},
		{Repo: "acme/api", PRsOpened: 2},
		{Repo: "acme/infra", PRsOpened: 5},
		{Repo: "acme/app", PRsOpened: 2},
		{Repo: "acme/docs"},
	}
	sortBreakdowns(bs)
	want := []string{"acme/infra", "acme/api", "acme/app", "acme/web", "acme/docs"}
	for i, b := range bs {
		if b.Repo != want[i] {
			t.Errorf("position %d = %s, want %s", i, b.Repo, want[i])
		}
	}
}

// The same order has to come out of the query path, which gathers the
// repos in a map: without the sort, the page would get a different order
// on every load. Five draws make a missing sort hard to get away with.
func TestPersonReposOrderAndCounterpartyCounts(t *testing.T) {
	store, teamID, _ := twoRepoFixture(t)
	f := repoFilter(teamID)
	w := LastDays(30, fixedNow)

	for i := 0; i < 5; i++ {
		repos, err := PersonRepos(store.DB, f, w, "alice")
		if err != nil {
			t.Fatal(err)
		}
		if len(repos) != 2 || repos[0].Repo != "acme/api" || repos[1].Repo != "acme/web" {
			t.Fatalf("alice's repos = %+v, want acme/api then acme/web (equal counts, by name)", repos)
		}
	}

	// bob authored nothing, so his rows are reviews and comments alone.
	// Comments given counts review-thread comments plus conversation
	// comments, the team table's definition.
	repos, err := PersonRepos(store.DB, f, w, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 2 {
		t.Fatalf("bob's repos = %+v, want both", repos)
	}
	for _, r := range repos {
		if r.PRsOpened != 0 || r.PRsMerged != 0 || r.ReviewsGiven != 1 || r.CommentsGiven != 2 {
			t.Errorf("bob in %s = %+v, want 0 opened, 0 merged, 1 review, 2 comments", r.Repo, r)
		}
	}
}

// Activity is one bucket per calendar day of the window: PRs opened plus
// reviews given, dismissed reviews included, each on the day of its own
// timestamp. The fixture's window runs Feb 8 12:00 to Mar 10 12:00, which
// touches 31 calendar days.
func TestPersonActivityDayBuckets(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}
	w := LastDays(30, fixedNow)

	for login, want := range map[string]map[int]float64{
		// PR1 opened now-10d (Feb 28), PR2 now-5d (Mar 5), the review on
		// PR3 now-2d (Mar 8). PR4 is outside the window.
		"alice": {20: 1, 25: 1, 28: 1},
		// The approval at open+12h and the dismissed re-review at now-9d
		// both land on Mar 1; changes requested now-4d (Mar 6); PR3
		// opened now-3d (Mar 7). His conversation comment is not activity.
		"bob": {21: 2, 26: 1, 27: 1},
	} {
		days, err := PersonActivity(store.DB, f, w, login)
		if err != nil {
			t.Fatal(err)
		}
		if len(days) != 31 {
			t.Fatalf("%s: %d buckets, want 31", login, len(days))
		}
		for i, d := range days {
			if d != want[i] {
				t.Errorf("%s day %d = %v, want %v", login, i, d, want[i])
			}
		}
	}
}

// A window with no room in it still yields one bucket, so the sparkline
// has something to draw.
func TestPersonActivityEmptyWindowHasOneBucket(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}
	midnight := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC).Unix()

	days, err := PersonActivity(store.DB, f, Window{Start: midnight, End: midnight}, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0] != 0 {
		t.Errorf("empty window activity = %v, want one empty bucket", days)
	}
}

// The loaders surface a broken cache instead of an empty page. A closed
// handle fails the first query of each; the later queries have no seam of
// their own and stay unpinned.
func TestPersonLoadersSurfaceDBErrors(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}
	w := LastDays(30, fixedNow)
	if err := store.DB.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := PersonRepos(store.DB, f, w, "alice"); err == nil {
		t.Error("PersonRepos: nil error from a closed database")
	}
	if _, err := PersonActivity(store.DB, f, w, "alice"); err == nil {
		t.Error("PersonActivity: nil error from a closed database")
	}
	if _, err := LoadPerson(store.DB, f, w, "alice"); err == nil {
		t.Error("LoadPerson: nil error from a closed database")
	}
}
