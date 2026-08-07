package metrics

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

const day = int64(86400)

// fixture builds a deterministic scenario around "now":
//
//	PR1 alice, opened now-10d, merged now-9d (cycle 1d), size 120
//	    reviews: dependabot[bot] COMMENTED early (must not count as TTFR),
//	             bob APPROVED at open+12h with 2 thread comments
//	    comments: bob 1, alice 1 (self — never counts)
//	PR2 alice, opened now-5d, open, size 10
//	    reviews: renovate-gtw (User, matches glob) early — must not count,
//	             bob CHANGES_REQUESTED at open+1d with 1 thread comment
//	PR3 bob, opened now-3d, merged now-1d (cycle 2d), size 60
//	    reviews: alice COMMENTED at open+1d, 0 thread comments
//	    comments: alice 1
//	PR4 alice, opened now-40d, merged now-35d — outside the 30d window
//	carol is a hidden member and must not appear in rows.
func fixture(t *testing.T) (*db.Store, int64, int64) {
	t.Helper()
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)

	team := config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}, {Login: "carol", Hidden: true}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	}
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	repoID := repoIDs["acme/api"]

	now := time.Now().Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }

	prs := []db.PullRequest{
		{
			ID: "PR1", RepoID: repoID, Number: 1, Author: "alice", Title: "one",
			State: "MERGED", CreatedAt: at(10 * day), MergedAt: i64(at(9 * day)),
			UpdatedAt: at(9 * day), Additions: 100, Deletions: 20, ChangedFiles: 3,
			Reviews: []db.Review{
				{ID: "R1a", Author: "dependabot[bot]", AuthorIsBot: true, State: "COMMENTED",
					SubmittedAt: at(10*day) + 1000, CommentCount: 5},
				{ID: "R1b", Author: "bob", State: "APPROVED",
					SubmittedAt: at(10*day) + day/2, CommentCount: 2},
			},
			Comments: []db.IssueComment{
				{ID: "C1a", Author: "bob", CreatedAt: at(9 * day)},
				{ID: "C1b", Author: "alice", CreatedAt: at(9 * day)}, // self
			},
		},
		{
			ID: "PR2", RepoID: repoID, Number: 2, Author: "alice", Title: "two",
			State: "OPEN", CreatedAt: at(5 * day), UpdatedAt: at(4 * day),
			Additions: 7, Deletions: 3, ChangedFiles: 1,
			Reviews: []db.Review{
				{ID: "R2a", Author: "renovate-gtw", State: "COMMENTED",
					SubmittedAt: at(5*day) + 100, CommentCount: 1},
				{ID: "R2b", Author: "bob", State: "CHANGES_REQUESTED",
					SubmittedAt: at(4 * day), CommentCount: 1},
			},
		},
		{
			ID: "PR3", RepoID: repoID, Number: 3, Author: "bob", Title: "three",
			State: "MERGED", CreatedAt: at(3 * day), MergedAt: i64(at(1 * day)),
			UpdatedAt: at(1 * day), Additions: 50, Deletions: 10, ChangedFiles: 2,
			Reviews: []db.Review{
				{ID: "R3a", Author: "alice", State: "COMMENTED",
					SubmittedAt: at(2 * day), CommentCount: 0},
			},
			Comments: []db.IssueComment{
				{ID: "C3a", Author: "alice", CreatedAt: at(2 * day)},
			},
		},
		{
			ID: "PR4", RepoID: repoID, Number: 4, Author: "alice", Title: "old",
			State: "MERGED", CreatedAt: at(40 * day), MergedAt: i64(at(35 * day)),
			UpdatedAt: at(35 * day), Additions: 1, Deletions: 1, ChangedFiles: 1,
		},
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}
	return store, teamID, repoID
}

func TestLeaderboardGoldenValues(t *testing.T) {
	store, teamID, _ := fixture(t)
	rows, err := Leaderboard(store.DB, Filter{
		TeamID: teamID,
		Bots:   config.NewBotMatcher(config.Default().ExcludeBots),
	}, LastDays(30))
	if err != nil {
		t.Fatal(err)
	}

	if len(rows) != 2 {
		t.Fatalf("rows = %d, want 2 (carol is hidden): %+v", len(rows), rows)
	}
	alice, bob := rows[0], rows[1]
	if alice.Login != "alice" || bob.Login != "bob" {
		t.Fatalf("unexpected order: %s, %s", alice.Login, bob.Login)
	}

	check := func(name string, got, want any) {
		t.Helper()
		if got != want {
			t.Errorf("%s = %v, want %v", name, got, want)
		}
	}

	check("alice.PRsOpened", alice.PRsOpened, 2) // PR4 outside window
	check("alice.PRsMerged", alice.PRsMerged, 1)
	check("alice.Commented", alice.Commented, 1) // R3a
	check("alice.ReviewsGiven", alice.ReviewsGiven, 1)
	check("alice.CommentsGiven", alice.CommentsGiven, 1) // C3a; R3a had 0
	check("alice.CommentsRecv", alice.CommentsRecv, 4)   // R1b(2)+C1a(1)+R2b(1); self C1b excluded
	check("alice.CycleTimeP50", alice.CycleTimeP50, 24*time.Hour)
	// TTFR candidates: PR1 12h (bot review earlier is skipped),
	// PR2 24h (glob-matched renovate-gtw skipped) → lower-middle = 12h.
	check("alice.TTFRP50", alice.TTFRP50, 12*time.Hour)
	check("alice.SizeP50", alice.SizeP50, 10) // sizes [10,120], lower-middle

	check("bob.PRsOpened", bob.PRsOpened, 1)
	check("bob.PRsMerged", bob.PRsMerged, 1)
	check("bob.Approved", bob.Approved, 1)
	check("bob.ChangesReq", bob.ChangesReq, 1)
	check("bob.ReviewsGiven", bob.ReviewsGiven, 2)
	check("bob.CommentsGiven", bob.CommentsGiven, 4) // R1b(2)+R2b(1)+C1a(1)
	check("bob.CommentsRecv", bob.CommentsRecv, 1)   // C3a; R3a had 0
	check("bob.CycleTimeP50", bob.CycleTimeP50, 48*time.Hour)
	check("bob.TTFRP50", bob.TTFRP50, 24*time.Hour)
	check("bob.SizeP50", bob.SizeP50, 60)
}

func TestWindowBoundaries(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}

	// A 50-day window picks up PR4 as well.
	rows, err := Leaderboard(store.DB, f, LastDays(50))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].PRsOpened != 3 || rows[0].PRsMerged != 2 {
		t.Errorf("50d alice opened/merged = %d/%d, want 3/2", rows[0].PRsOpened, rows[0].PRsMerged)
	}

	// A 2-day window sees only PR3's merge (created outside).
	rows, err = Leaderboard(store.DB, f, LastDays(2))
	if err != nil {
		t.Fatal(err)
	}
	if rows[1].PRsOpened != 0 || rows[1].PRsMerged != 1 {
		t.Errorf("2d bob opened/merged = %d/%d, want 0/1", rows[1].PRsOpened, rows[1].PRsMerged)
	}
}

func TestMedianEdgeCases(t *testing.T) {
	if _, ok := median(nil); ok {
		t.Error("median(nil) reported data")
	}
	if v, _ := median([]int64{5}); v != 5 {
		t.Errorf("median single = %d", v)
	}
	if v, _ := median([]int64{4, 1, 3, 2}); v != 2 {
		t.Errorf("median even = %d, want lower-middle 2", v)
	}
	if v, _ := median([]int64{3, 1, 2}); v != 2 {
		t.Errorf("median odd = %d, want 2", v)
	}
}

func TestPersonBreakdownAndThroughput(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}
	w := LastDays(30)

	repos, err := PersonRepos(store.DB, f, w, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if len(repos) != 1 || repos[0].Repo != "acme/api" {
		t.Fatalf("repos = %+v", repos)
	}
	if repos[0].PRsOpened != 2 || repos[0].ReviewsGiven != 1 || repos[0].CommentsGiven != 1 {
		t.Errorf("alice breakdown = %+v", repos[0])
	}

	buckets, err := Throughput(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	var opened, merged int
	for _, b := range buckets {
		opened += b.Opened
		merged += b.Merged
	}
	if opened != 3 || merged != 2 {
		t.Errorf("throughput totals opened/merged = %d/%d, want 3/2", opened, merged)
	}
}
