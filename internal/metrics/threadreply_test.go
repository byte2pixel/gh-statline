package metrics

import (
	"reflect"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// A reply inside a review thread is a comment, not a review (#105). It
// reaches the cache as a COMMENTED review holding one comment, and until
// #105 every review count included it while every comment sum did too.
// The thread_replies view (migration 0003) names the replies, and every
// query that counts reviews leaves them out through query.go; every
// comment sum keeps them. These pins mirror exclusion_test.go: one
// fixture, every entry point, hand-computed values before and after the
// facts arrive.

// replyFixture is alice and bob reviewing each other, with four
// single-comment COMMENTED reviews by alice on bob's PR that the pass will
// classify four different ways, and one by bob on alice's PR.
//
//	PR_A, alice, opened now-10d, merged now-9d (cycle 24h):
//	  RA1 bob APPROVED  at open+12h, 2 comments      → the first review
//	  RA2 bob COMMENTED at open+24h, 1 comment       → thread reply
//	  CA1 bob conversation comment at now-9d
//	PR_B, bob, opened now-5d, merged now-3d (cycle 48h):
//	  RB1 alice COMMENTED at open+24h, 1 comment     → thread reply
//	  RB2 alice COMMENTED at open+48h, 1 comment     → has a body: a review
//	  RB3 alice COMMENTED at open+49h, 1 comment     → never classified
//	  RB4 alice COMMENTED at open+50h, 1 comment     → gone from GitHub
func replyFixture(t *testing.T) (*db.Store, Filter) {
	t.Helper()
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)

	teamID, repoIDs, err := store.MirrorTeam(config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
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
	hour := int64(3600)
	if err := store.SavePullRequests([]db.PullRequest{
		{
			ID: "PR_A", RepoID: repoID, Number: 1, Author: "alice", Title: "a",
			State: "MERGED", CreatedAt: at(10 * day), MergedAt: i64(at(9 * day)),
			UpdatedAt: at(9 * day), Additions: 100, Deletions: 20, ChangedFiles: 3,
			Reviews: []db.Review{
				{ID: "RA1", Author: "bob", State: "APPROVED", SubmittedAt: at(10*day) + 12*hour, CommentCount: 2},
				{ID: "RA2", Author: "bob", State: "COMMENTED", SubmittedAt: at(10*day) + day, CommentCount: 1},
			},
			Comments: []db.IssueComment{{ID: "CA1", Author: "bob", CreatedAt: at(9 * day)}},
		},
		{
			ID: "PR_B", RepoID: repoID, Number: 2, Author: "bob", Title: "b",
			State: "MERGED", CreatedAt: at(5 * day), MergedAt: i64(at(3 * day)),
			UpdatedAt: at(3 * day), Additions: 50, Deletions: 10, ChangedFiles: 2,
			Reviews: []db.Review{
				{ID: "RB1", Author: "alice", State: "COMMENTED", SubmittedAt: at(5*day) + day, CommentCount: 1},
				{ID: "RB2", Author: "alice", State: "COMMENTED", SubmittedAt: at(5*day) + 2*day, CommentCount: 1},
				{ID: "RB3", Author: "alice", State: "COMMENTED", SubmittedAt: at(5*day) + 2*day + hour, CommentCount: 1},
				{ID: "RB4", Author: "alice", State: "COMMENTED", SubmittedAt: at(5*day) + 2*day + 2*hour, CommentCount: 1},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return store, Filter{TeamID: teamID}
}

// classify writes the facts the pass would have fetched. RB3 gets none.
func classify(t *testing.T, store *db.Store) {
	t.Helper()
	if err := store.SaveReviewFacts([]db.ReviewFacts{
		{ReviewID: "RA2", Known: true, HasBody: false, FirstReply: true},
		{ReviewID: "RB1", Known: true, HasBody: false, FirstReply: true},
		{ReviewID: "RB2", Known: true, HasBody: true, FirstReply: true},
		{ReviewID: "RB4", Known: false},
	}); err != nil {
		t.Fatal(err)
	}
}

func rowFor(t *testing.T, rows []Row, login string) Row {
	t.Helper()
	for _, r := range rows {
		if r.Login == login {
			return r
		}
	}
	t.Fatalf("no row for %s", login)
	return Row{}
}

func sumF(xs []float64) float64 {
	n := 0.0
	for _, x := range xs {
		n += x
	}
	return n
}

// Before the facts arrive every candidate counts as the review it always
// was; nothing is guessed.
func TestUnclassifiedRepliesStillCountAsReviews(t *testing.T) {
	store, f := replyFixture(t)
	s := takeSnapshot(t, store, f)

	alice, bob := rowFor(t, s.Dash.Rows, "alice"), rowFor(t, s.Dash.Rows, "bob")
	if alice.Commented != 4 || alice.ReviewsGiven != 4 || alice.CommentsGiven != 4 || alice.CommentsRecv != 4 {
		t.Errorf("alice before = %+v", alice)
	}
	if bob.Approved != 1 || bob.Commented != 1 || bob.ReviewsGiven != 2 || bob.CommentsGiven != 4 || bob.CommentsRecv != 4 {
		t.Errorf("bob before = %+v", bob)
	}
	// RB1 is the first review on PR_B until it is known to be a reply.
	if bob.TTFRP50 != 24*time.Hour || alice.TTFRP50 != 12*time.Hour {
		t.Errorf("TTFR before: alice %v, bob %v; want 12h, 24h", alice.TTFRP50, bob.TTFRP50)
	}
}

// With the facts in, a reply leaves every review count and stays in every
// comment sum: the team table, the review matrix, the punch card, the
// team medians, the weekly trends and the person drill-down.
func TestThreadRepliesCountOnceAsComments(t *testing.T) {
	store, f := replyFixture(t)
	before := takeSnapshot(t, store, f)
	classify(t, store)
	after := takeSnapshot(t, store, f)

	alice, bob := rowFor(t, after.Dash.Rows, "alice"), rowFor(t, after.Dash.Rows, "bob")
	// RB1 is the reply; RB2 has a body, RB3 is unclassified, RB4 unknown.
	if alice.Commented != 3 || alice.ReviewsGiven != 3 || alice.Approved != 0 {
		t.Errorf("alice reviews after = %+v, want 3 commented", alice)
	}
	if alice.CommentsGiven != 4 || alice.CommentsRecv != 4 {
		t.Errorf("alice comments after = %+v, want 4 given, 4 received: a reply is still a comment", alice)
	}
	if bob.Commented != 0 || bob.ReviewsGiven != 1 || bob.Approved != 1 {
		t.Errorf("bob reviews after = %+v, want the approval alone", bob)
	}
	if bob.CommentsGiven != 4 || bob.CommentsRecv != 4 {
		t.Errorf("bob comments after = %+v, want 4 given, 4 received", bob)
	}
	// A reply is not a first review: PR_B's is now RB2 at open+48h.
	if bob.TTFRP50 != 48*time.Hour || alice.TTFRP50 != 12*time.Hour {
		t.Errorf("TTFR after: alice %v, bob %v; want 12h, 48h", alice.TTFRP50, bob.TTFRP50)
	}
	if after.Dash.Tiles.TTFR != 12*time.Hour {
		t.Errorf("team TTFR = %v, want 12h (lower middle of 12h, 48h)", after.Dash.Tiles.TTFR)
	}

	// Matrix: alice→bob 4→3, bob→alice 2→1.
	ai, bi := matrixIdx(t, after.Dash.Matrix, "alice"), matrixIdx(t, after.Dash.Matrix, "bob")
	if before.Dash.Matrix.Counts[ai][bi] != 4 || after.Dash.Matrix.Counts[ai][bi] != 3 {
		t.Errorf("matrix alice→bob = %d → %d, want 4 → 3", before.Dash.Matrix.Counts[ai][bi], after.Dash.Matrix.Counts[ai][bi])
	}
	if before.Dash.Matrix.Counts[bi][ai] != 2 || after.Dash.Matrix.Counts[bi][ai] != 1 {
		t.Errorf("matrix bob→alice = %d → %d, want 2 → 1", before.Dash.Matrix.Counts[bi][ai], after.Dash.Matrix.Counts[bi][ai])
	}
	// Punch card: 2 opens + 6 reviews, then 2 + 4.
	if before.Dash.Punch.Total != 8 || after.Dash.Punch.Total != 6 {
		t.Errorf("punch total = %d → %d, want 8 → 6", before.Dash.Punch.Total, after.Dash.Punch.Total)
	}

	// Trends: reviews drop by the replies, comments do not move.
	ta, tb := trendFor(t, after.Trend, "alice"), trendFor(t, after.Trend, "bob")
	ba, bb := trendFor(t, before.Trend, "alice"), trendFor(t, before.Trend, "bob")
	if sum(ba.Reviews) != 4 || sum(ta.Reviews) != 3 || sum(bb.Reviews) != 2 || sum(tb.Reviews) != 1 {
		t.Errorf("trend reviews: alice %d → %d, bob %d → %d; want 4 → 3, 2 → 1",
			sum(ba.Reviews), sum(ta.Reviews), sum(bb.Reviews), sum(tb.Reviews))
	}
	if sum(ta.Comments) != 4 || sum(tb.Comments) != 4 || !reflect.DeepEqual(ba.Comments, ta.Comments) || !reflect.DeepEqual(bb.Comments, tb.Comments) {
		t.Errorf("trend comments moved: alice %v → %v, bob %v → %v", ba.Comments, ta.Comments, bb.Comments, tb.Comments)
	}

	// Person drill-down for alice: one repo, 4 → 3 reviews, 4 comments both
	// times; the activity pulse is 1 open + reviews.
	if len(after.Repos) != 1 || after.Repos[0].ReviewsGiven != 3 || after.Repos[0].CommentsGiven != 4 {
		t.Errorf("person repos after = %+v, want 3 reviews, 4 comments", after.Repos)
	}
	if before.Repos[0].ReviewsGiven != 4 || before.Repos[0].CommentsGiven != 4 {
		t.Errorf("person repos before = %+v, want 4 reviews, 4 comments", before.Repos)
	}
	if sumF(before.Activity) != 5 || sumF(after.Activity) != 4 {
		t.Errorf("activity = %v → %v, want 5 → 4", sumF(before.Activity), sumF(after.Activity))
	}
}

// Replying in a thread on your own PR was already excluded as a
// self-review; classifying it changes nothing.
func TestSelfReplyStaysExcluded(t *testing.T) {
	store, f := replyFixture(t)
	var repoID int64
	if err := store.DB.QueryRow(`SELECT repo_id FROM team_repos WHERE team_id = ?`, f.TeamID).Scan(&repoID); err != nil {
		t.Fatal(err)
	}
	now := fixedNow.Unix()
	if err := store.SavePullRequests([]db.PullRequest{{
		ID: "PR_C", RepoID: repoID, Number: 3, Author: "alice", Title: "c",
		State: "OPEN", CreatedAt: now - 2*day, UpdatedAt: now - day,
		Additions: 1, Deletions: 1, ChangedFiles: 1,
		Reviews: []db.Review{{ID: "RC_self", Author: "alice", State: "COMMENTED", SubmittedAt: now - day, CommentCount: 1}},
	}}); err != nil {
		t.Fatal(err)
	}
	before := takeSnapshot(t, store, f)
	if err := store.SaveReviewFacts([]db.ReviewFacts{{ReviewID: "RC_self", Known: true, FirstReply: true}}); err != nil {
		t.Fatal(err)
	}
	after := takeSnapshot(t, store, f)
	if !reflect.DeepEqual(before.Dash.Rows, after.Dash.Rows) {
		t.Errorf("rows moved on a self-reply:\n before %+v\n after  %+v", before.Dash.Rows, after.Dash.Rows)
	}
}

func matrixIdx(t *testing.T, m Matrix, login string) int {
	t.Helper()
	for i, l := range m.Logins {
		if l == login {
			return i
		}
	}
	t.Fatalf("%s not in matrix %v", login, m.Logins)
	return -1
}

func trendFor(t *testing.T, td TrendData, login string) MemberTrend {
	t.Helper()
	for _, m := range td.Members {
		if m.Login == login {
			return m
		}
	}
	t.Fatalf("%s not in trend members", login)
	return MemberTrend{}
}
