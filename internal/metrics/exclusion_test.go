package metrics

import (
	"reflect"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// Exclusion is one rule, enforced once (#106): every entry point reads the
// visible_members and bot_actors views, so a hidden member, a member
// GitHub typed as a Bot, and a member matching an exclude_bots glob leave
// every number, whatever they do. These pins mirror repofilter_test.go:
// one fixture, every entry point, and a snapshot of every dataset before
// and after the excluded actors get busy. The filter carries no matcher:
// the views alone must do it.

// exclusionFixture is alice and bob doing ordinary work, with three
// excluded members on the roster: carol hidden, svc-bot typed Bot, and
// renovate-team matching the default globs. The two bots each own one PR
// far outside every window, so the cache knows them the way a real sync
// would, without any in-window activity.
func exclusionFixture(t *testing.T) (*db.Store, Filter, int64) {
	t.Helper()
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)

	teamID, repoIDs, err := store.MirrorTeam(config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{
			{Login: "alice"}, {Login: "bob"},
			{Login: "carol", Hidden: true},
			{Login: "svc-bot"},
			{Login: "renovate-team"},
		},
		Repos: []config.Repo{{Owner: "acme", Name: "api"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MirrorBotGlobs(config.Default().ExcludeBots); err != nil {
		t.Fatal(err)
	}
	repoID := repoIDs["acme/api"]
	setFloor(t, store, repoID, fixedNow.AddDate(0, 0, -120).Unix())

	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }
	if err := store.SavePullRequests([]db.PullRequest{
		exclusionPRA(repoID),
		{
			ID: "PR_B", RepoID: repoID, Number: 2, Author: "bob", Title: "b",
			State: "MERGED", CreatedAt: at(5 * day), MergedAt: i64(at(3 * day)),
			UpdatedAt: at(3 * day), Additions: 50, Deletions: 10, ChangedFiles: 2,
			Reviews: []db.Review{{ID: "RB1", Author: "alice", State: "COMMENTED",
				SubmittedAt: at(4 * day), CommentCount: 1}},
			Comments: []db.IssueComment{{ID: "CB1", Author: "alice", CreatedAt: at(4 * day)}},
		},
		{
			ID: "PR_OLD_SVC", RepoID: repoID, Number: 90, Author: "svc-bot", AuthorIsBot: true,
			Title: "ancient", State: "MERGED", CreatedAt: at(200 * day), MergedAt: i64(at(199 * day)),
			UpdatedAt: at(199 * day), Additions: 1, Deletions: 1, ChangedFiles: 1,
		},
		{
			ID: "PR_OLD_REN", RepoID: repoID, Number: 91, Author: "renovate-team",
			Title: "ancient", State: "MERGED", CreatedAt: at(200 * day), MergedAt: i64(at(199 * day)),
			UpdatedAt: at(199 * day), Additions: 1, Deletions: 1, ChangedFiles: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
	return store, Filter{TeamID: teamID}, repoID
}

// exclusionPRA is the merged PR alice owns: opened now-10d, merged now-9d,
// one approval from bob at open+12h with two thread comments, one
// conversation comment from bob. Callers append the noise of the excluded
// actors to it.
func exclusionPRA(repoID int64) db.PullRequest {
	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	merged := at(9 * day)
	return db.PullRequest{
		ID: "PR_A", RepoID: repoID, Number: 1, Author: "alice", Title: "a",
		State: "MERGED", CreatedAt: at(10 * day), MergedAt: &merged,
		UpdatedAt: merged, Additions: 100, Deletions: 20, ChangedFiles: 3,
		Reviews: []db.Review{{ID: "RA1", Author: "bob", State: "APPROVED",
			SubmittedAt: at(10*day) + day/2, CommentCount: 2}},
		Comments: []db.IssueComment{{ID: "CA1", Author: "bob", CreatedAt: at(9 * day)}},
	}
}

// noisyPRA is exclusionPRA with the two bots reviewing and commenting on
// it before bob does: an earlier review that must not become the time to
// first review, and comments that must not be comments alice received.
func noisyPRA(repoID int64) db.PullRequest {
	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	prA := exclusionPRA(repoID)
	prA.Reviews = append(prA.Reviews,
		db.Review{ID: "RA_svc", Author: "svc-bot", AuthorIsBot: true, State: "COMMENTED",
			SubmittedAt: at(10*day) + 3600, CommentCount: 3},
		db.Review{ID: "RA_ren", Author: "renovate-team", State: "CHANGES_REQUESTED",
			SubmittedAt: at(10*day) + 7200, CommentCount: 2},
	)
	prA.Comments = append(prA.Comments,
		db.IssueComment{ID: "CA_svc", Author: "svc-bot", AuthorIsBot: true, CreatedAt: at(9 * day)},
		db.IssueComment{ID: "CA_ren", Author: "renovate-team", CreatedAt: at(9 * day)},
	)
	return prA
}

// snapshot is every dataset the app renders, from every entry point.
type snapshot struct {
	Dash     Dashboard
	Trend    TrendData
	Repos    []RepoBreakdown
	Activity []float64
}

func takeSnapshot(t *testing.T, store *db.Store, f Filter) snapshot {
	t.Helper()
	w := LastDays(30, fixedNow)
	var s snapshot
	var err error
	if s.Dash, err = LoadDashboard(store.DB, f, w, fixedNow); err != nil {
		t.Fatal(err)
	}
	if s.Trend, err = TrendSeries(store.DB, f, TrendWeeks, fixedNow); err != nil {
		t.Fatal(err)
	}
	if s.Repos, err = PersonRepos(store.DB, f, w, "alice"); err != nil {
		t.Fatal(err)
	}
	if s.Activity, err = PersonActivity(store.DB, f, w, "alice"); err != nil {
		t.Fatal(err)
	}
	return s
}

// addExcludedNoise makes the three excluded members busy: each authors an
// in-window PR (two of them open, for the aging list), each reviews and
// comments on the others, and the two bots review and comment on the PR
// alice owns before bob does. Activity by a hidden member towards a
// visible one is the one thing that still counts;
// TestHiddenMembersStillCountTowardsVisibleOnes adds that separately.
func addExcludedNoise(t *testing.T, store *db.Store, repoID int64) {
	t.Helper()
	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }

	if err := store.SavePullRequests([]db.PullRequest{
		noisyPRA(repoID),
		{
			ID: "PR_C", RepoID: repoID, Number: 3, Author: "carol", Title: "hidden work",
			State: "MERGED", CreatedAt: at(6 * day), MergedAt: i64(at(5 * day)),
			UpdatedAt: at(5 * day), Additions: 200, Deletions: 100, ChangedFiles: 4,
			Reviews: []db.Review{
				{ID: "RC1", Author: "svc-bot", AuthorIsBot: true, State: "COMMENTED", SubmittedAt: at(6*day) + 600, CommentCount: 1},
				{ID: "RC2", Author: "renovate-team", State: "APPROVED", SubmittedAt: at(6*day) + 1200, CommentCount: 1},
			},
			Comments: []db.IssueComment{{ID: "CC1", Author: "renovate-team", CreatedAt: at(5 * day)}},
		},
		{
			ID: "PR_D", RepoID: repoID, Number: 4, Author: "svc-bot", AuthorIsBot: true, Title: "bot work",
			State: "MERGED", CreatedAt: at(8 * day), MergedAt: i64(at(7 * day)),
			UpdatedAt: at(7 * day), Additions: 600, Deletions: 300, ChangedFiles: 9,
			Reviews:  []db.Review{{ID: "RD1", Author: "carol", State: "APPROVED", SubmittedAt: at(8*day) + 600, CommentCount: 2}},
			Comments: []db.IssueComment{{ID: "CD1", Author: "carol", CreatedAt: at(7 * day)}},
		},
		{
			ID: "PR_E", RepoID: repoID, Number: 5, Author: "renovate-team", Title: "glob work",
			State: "OPEN", CreatedAt: at(4 * day), UpdatedAt: at(4 * day),
			Additions: 3, Deletions: 2, ChangedFiles: 1,
			Reviews: []db.Review{{ID: "RE1", Author: "carol", State: "COMMENTED", SubmittedAt: at(3 * day), CommentCount: 1}},
		},
		{
			ID: "PR_F", RepoID: repoID, Number: 6, Author: "carol", Title: "hidden and open",
			State: "OPEN", CreatedAt: at(2 * day), UpdatedAt: at(2 * day),
			Additions: 30, Deletions: 2, ChangedFiles: 1,
		},
	}); err != nil {
		t.Fatal(err)
	}
}

// Before the noise, the roster reads as the two visible members and the
// numbers are the ones every later assertion compares against.
func TestExclusionFixtureBaseline(t *testing.T) {
	store, f, _ := exclusionFixture(t)
	s := takeSnapshot(t, store, f)

	var logins []string
	for _, r := range s.Dash.Rows {
		logins = append(logins, r.Login)
	}
	if want := []string{"alice", "bob"}; !reflect.DeepEqual(logins, want) {
		t.Fatalf("rows = %q, want %q", logins, want)
	}
	alice := s.Dash.Rows[0]
	if alice.PRsOpened != 1 || alice.PRsMerged != 1 || alice.CommentsRecv != 3 ||
		alice.TTFRP50 != 12*time.Hour || alice.CycleTimeP50 != 24*time.Hour {
		t.Errorf("alice baseline = %+v", alice)
	}
	if !s.Dash.Tiles.HasPrev {
		t.Error("120 days of coverage should enable the previous-window tiles")
	}
	if len(s.Trend.Weeks) != TrendWeeks || len(s.Trend.Members) != 2 {
		t.Errorf("trend baseline: %d weeks, %d members", len(s.Trend.Weeks), len(s.Trend.Members))
	}
	if s.Dash.Aging.Total != 0 {
		t.Errorf("aging baseline lists %d open PRs, want none", s.Dash.Aging.Total)
	}
}

// Every dataset behind every view is identical with the excluded actors
// busy: the team table and its medians, the tiles including the previous
// window, throughput, cycle trend, the TTFR and size distributions, the
// review matrix, the punch card, open-PR aging, the weekly trends with
// every member series, and the person drill-down.
func TestExcludedActorsLeaveEveryNumber(t *testing.T) {
	store, f, repoID := exclusionFixture(t)
	before := takeSnapshot(t, store, f)
	addExcludedNoise(t, store, repoID)
	after := takeSnapshot(t, store, f)

	if !reflect.DeepEqual(before.Dash.Rows, after.Dash.Rows) {
		t.Errorf("team stats moved:\n before %+v\n after  %+v", before.Dash.Rows, after.Dash.Rows)
	}
	if !reflect.DeepEqual(before.Dash.Tiles, after.Dash.Tiles) {
		t.Errorf("tiles moved:\n before %+v\n after  %+v", before.Dash.Tiles, after.Dash.Tiles)
	}
	if !reflect.DeepEqual(before.Dash.Buckets, after.Dash.Buckets) {
		t.Errorf("throughput moved")
	}
	if !reflect.DeepEqual(before.Dash.Trend, after.Dash.Trend) {
		t.Errorf("cycle trend moved:\n before %+v\n after  %+v", before.Dash.Trend, after.Dash.Trend)
	}
	if !reflect.DeepEqual(before.Dash.TTFR, after.Dash.TTFR) {
		t.Errorf("TTFR distribution moved: %v -> %v", before.Dash.TTFR.Counts, after.Dash.TTFR.Counts)
	}
	if !reflect.DeepEqual(before.Dash.Sizes, after.Dash.Sizes) {
		t.Errorf("size distribution moved: %v -> %v", before.Dash.Sizes.Counts, after.Dash.Sizes.Counts)
	}
	if !reflect.DeepEqual(before.Dash.Matrix, after.Dash.Matrix) {
		t.Errorf("review matrix moved:\n before %+v\n after  %+v", before.Dash.Matrix, after.Dash.Matrix)
	}
	if !reflect.DeepEqual(before.Dash.Punch, after.Dash.Punch) {
		t.Errorf("punch card moved: %d -> %d events", before.Dash.Punch.Total, after.Dash.Punch.Total)
	}
	if !reflect.DeepEqual(before.Dash.Aging, after.Dash.Aging) {
		t.Errorf("open aging moved:\n before %+v\n after  %+v", before.Dash.Aging, after.Dash.Aging)
	}
	if !reflect.DeepEqual(before.Trend, after.Trend) {
		t.Errorf("trend series moved:\n before %+v\n after  %+v", before.Trend.Team, after.Trend.Team)
	}
	if !reflect.DeepEqual(before.Repos, after.Repos) || !reflect.DeepEqual(before.Activity, after.Activity) {
		t.Errorf("person drill-down moved")
	}
}

// The excluded members are excluded as actors; the roster itself is
// unchanged, and reviews the bots gave leave the matrix rather than
// landing in the (others) column.
func TestExcludedActorsAreNotRowsOrReviewers(t *testing.T) {
	store, f, repoID := exclusionFixture(t)
	addExcludedNoise(t, store, repoID)
	s := takeSnapshot(t, store, f)

	for _, r := range s.Dash.Rows {
		if r.Login != "alice" && r.Login != "bob" {
			t.Errorf("%q has a stat line", r.Login)
		}
	}
	for _, m := range s.Trend.Members {
		if m.Login != "alice" && m.Login != "bob" {
			t.Errorf("%q has a trend series", m.Login)
		}
	}
	if !reflect.DeepEqual(s.Dash.Matrix.Logins, []string{"alice", "bob"}) ||
		!reflect.DeepEqual(s.Dash.Matrix.Authors, []string{"alice", "bob"}) {
		t.Errorf("matrix axes = %q x %q, want alice and bob with no (others) column",
			s.Dash.Matrix.Logins, s.Dash.Matrix.Authors)
	}
	for _, p := range s.Dash.Aging.Stalest {
		if p.Author != "alice" && p.Author != "bob" {
			t.Errorf("open PR by %q is in the aging list", p.Author)
		}
	}
}

// A hidden member is hidden, not gone: their comment on a PR a visible
// member owns is still a comment that member received. The same holds
// after the excluded noise, so the two are added together.
func TestHiddenMembersStillCountTowardsVisibleOnes(t *testing.T) {
	store, f, repoID := exclusionFixture(t)
	addExcludedNoise(t, store, repoID)
	before := takeSnapshot(t, store, f)

	prA := noisyPRA(repoID)
	prA.Comments = append(prA.Comments,
		db.IssueComment{ID: "CA_carol", Author: "carol", CreatedAt: fixedNow.Unix() - 9*day})
	if err := store.SavePullRequests([]db.PullRequest{prA}); err != nil {
		t.Fatal(err)
	}
	after := takeSnapshot(t, store, f)

	if got, want := after.Dash.Rows[0].CommentsRecv, before.Dash.Rows[0].CommentsRecv+1; got != want {
		t.Errorf("alice.CommentsRecv = %d, want %d (the comment from the hidden member counts)", got, want)
	}
	// Received is the only number that moves; the hidden member gave it,
	// and giving is not counted for them anywhere.
	after.Dash.Rows[0].CommentsRecv = before.Dash.Rows[0].CommentsRecv
	if !reflect.DeepEqual(before.Dash.Rows, after.Dash.Rows) {
		t.Errorf("more than CommentsRecv moved:\n before %+v\n after  %+v", before.Dash.Rows, after.Dash.Rows)
	}
	if !reflect.DeepEqual(before.Trend, after.Trend) {
		t.Errorf("a conversation comment received is not a trend series, yet the trends moved")
	}
}
