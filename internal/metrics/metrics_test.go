package metrics

import (
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

const day = int64(86400)

// fixedNow pins every time-relative fixture and window. Tuesday noon UTC
// keeps the whole-day offsets below clear of day and week boundaries, so
// bucket membership never depends on when the suite runs.
var fixedNow = time.Date(2026, 3, 10, 12, 0, 0, 0, time.UTC)

// fixture builds a deterministic scenario around "now":
//
//	PR1 alice, opened now-10d, merged now-9d (cycle 1d), size 120
//	    reviews: dependabot[bot] COMMENTED early (must not count as TTFR),
//	             bob APPROVED at open+12h with 2 thread comments,
//	             bob DISMISSED at open+1d (state rewritten by a push)
//	    comments: bob 1, alice 1 (self — never counts)
//	PR2 alice, opened now-5d, open, size 10
//	    reviews: renovate-gtw (User, matches glob) early — must not count,
//	             bob CHANGES_REQUESTED at open+1d with 1 thread comment
//	PR3 bob, opened now-3d, merged now-1d (cycle 2d), size 60
//	    reviews: alice COMMENTED at open+1d, 0 thread comments
//	    comments: alice 1
//	PR4 alice, opened now-40d, merged now-35d — outside the 30d window
//	PR5 carol (hidden member), opened now-6d, merged now-5d, size 300
//	    reviews: outsider (not a member, not a bot) at open+1d — a TTFR
//	             sample only if a hidden author leaks into the charts
//	PR6 autoreview-svc (member flagged is_bot, matching no glob), opened
//	    now-8d, merged now-7d, size 900
//	carol also reviews bob's PR3 at now-1d, after alice's review, so it
//	changes no TTFR but would show up in the punch card if hidden
//	reviewers leaked.
//
// carol is a hidden member and autoreview-svc is a bot member: neither
// may appear in rows, and neither may contribute to any team-level chart.
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
		Members: []config.Member{
			{Login: "alice"}, {Login: "bob"},
			{Login: "carol", Hidden: true},
			{Login: "autoreview-svc"},
		},
		Repos: []config.Repo{{Owner: "acme", Name: "api"}},
	}
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	repoID := repoIDs["acme/api"]

	now := fixedNow.Unix()
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
				// GitHub rewrote this one's state after a later push. It is
				// still a review bob gave, and every view except the team
				// table used to agree on that.
				{ID: "R1c", Author: "bob", State: "DISMISSED",
					SubmittedAt: at(9 * day), CommentCount: 0},
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
				// Hidden member reviewing a visible member's PR: later than
				// alice's, so it cannot change PR3's time to first review.
				{ID: "R3b", Author: "carol", State: "APPROVED",
					SubmittedAt: at(1 * day), CommentCount: 0},
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
		{
			ID: "PR5", RepoID: repoID, Number: 5, Author: "carol", Title: "hidden work",
			State: "MERGED", CreatedAt: at(6 * day), MergedAt: i64(at(5 * day)),
			UpdatedAt: at(5 * day), Additions: 200, Deletions: 100, ChangedFiles: 4,
			Reviews: []db.Review{
				{ID: "R5a", Author: "outsider", State: "APPROVED",
					SubmittedAt: at(5 * day), CommentCount: 0},
			},
		},
		{
			ID: "PR6", RepoID: repoID, Number: 6, Author: "autoreview-svc",
			AuthorIsBot: true, Title: "bot work",
			State: "MERGED", CreatedAt: at(8 * day), MergedAt: i64(at(7 * day)),
			UpdatedAt: at(7 * day), Additions: 600, Deletions: 300, ChangedFiles: 9,
		},
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}
	return store, teamID, repoID
}

// Nothing enforces that a review author has a users row, and reading is_bot
// must not drop the review when one is missing: losing the earliest review
// silently reports the next reviewer's latency as time-to-first-review.
func TestTTFRSurvivesMissingUserRow(t *testing.T) {
	store, teamID, _ := fixture(t)
	// Both of alice's TTFR samples come from bob's reviews; the earlier
	// reviewer on each PR is a bot or glob-matched and never counts.
	if _, err := store.DB.Exec(`DELETE FROM users WHERE login = 'bob'`); err != nil {
		t.Fatal(err)
	}
	rows, err := TeamStats(store.DB, Filter{
		TeamID: teamID,
		Bots:   config.NewBotMatcher(config.Default().ExcludeBots),
	}, LastDays(30, fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Login != "alice" {
		t.Fatalf("unexpected order: %+v", rows)
	}
	if got := rows[0].TTFRP50; got != 12*time.Hour {
		t.Errorf("alice.TTFRP50 = %v, want 12h (bob's reviews were dropped)", got)
	}
}

func TestBucketIdxRejectsTimestampsBeforeStart(t *testing.T) {
	const start, size = int64(1_700_000_000), day
	cases := []struct {
		name string
		ts   int64
		want int
	}{
		// Anything less than one bucket early truncated toward zero, so it
		// used to report bucket 0 and count as first-bucket activity. A
		// whole bucket or more early already produced a negative index.
		{"just before start", start - 1, -1},
		{"most of a bucket early", start - size + 1, -1},
		{"buckets before start", start - 10*size, -1},
		{"start", start, 0},
		{"last second of first bucket", start + size - 1, 0},
		{"second bucket", start + size, 1},
	}
	for _, c := range cases {
		if got := bucketIdx(c.ts, start, size); got != c.want {
			t.Errorf("%s: bucketIdx = %d, want %d", c.name, got, c.want)
		}
	}
	if got := bucketIdx(start, start, 0); got != -1 {
		t.Errorf("zero-size bucket = %d, want -1", got)
	}
}

// The label has to describe the window that was computed. Formatting the
// caller's own time named the wrong day whenever their date was not the UTC
// one, while Start and End were always UTC.
func TestRangeLabelMatchesWindow(t *testing.T) {
	west := time.FixedZone("UTC-5", -5*3600)
	from := time.Date(2026, 3, 10, 23, 30, 0, 0, west) // 2026-03-11 UTC
	to := time.Date(2026, 3, 12, 23, 30, 0, 0, west)   // 2026-03-13 UTC

	w := Range(from, to)
	if want := "2026-03-11 → 2026-03-13"; w.Label != want {
		t.Errorf("Label = %q, want %q", w.Label, want)
	}
	if got := time.Unix(w.Start, 0).UTC().Format("2006-01-02"); got != "2026-03-11" {
		t.Errorf("window starts %s, want 2026-03-11", got)
	}
	if got := time.Unix(w.End-1, 0).UTC().Format("2006-01-02"); got != "2026-03-13" {
		t.Errorf("window ends %s, want 2026-03-13 (whole day included)", got)
	}
}

func TestTeamStatsGoldenValues(t *testing.T) {
	store, teamID, _ := fixture(t)
	rows, err := TeamStats(store.DB, Filter{
		TeamID: teamID,
		Bots:   config.NewBotMatcher(config.Default().ExcludeBots),
	}, LastDays(30, fixedNow))
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
	// R1b APPROVED + R2b CHANGES_REQUESTED + R1c DISMISSED: the dismissed
	// review is work bob did, and it is what every other view already counted.
	check("bob.ReviewsGiven", bob.ReviewsGiven, 3)
	check("bob.Dismissed", bob.Dismissed, 1)
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
	rows, err := TeamStats(store.DB, f, LastDays(50, fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].PRsOpened != 3 || rows[0].PRsMerged != 2 {
		t.Errorf("50d alice opened/merged = %d/%d, want 3/2", rows[0].PRsOpened, rows[0].PRsMerged)
	}

	// A 2-day window sees only PR3's merge (created outside).
	rows, err = TeamStats(store.DB, f, LastDays(2, fixedNow))
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

func TestChartMetrics(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}
	w := LastDays(30, fixedNow)

	trend, err := CycleTrend(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	// PR1 merged 9d ago (cycle 1d), PR3 merged 1d ago (cycle 2d): 8 days
	// apart, always distinct week buckets.
	var pts []TrendPoint
	for _, p := range trend {
		if p.Merged > 0 {
			pts = append(pts, p)
		}
	}
	if len(pts) != 2 || pts[0].Median != 24*time.Hour || pts[1].Median != 48*time.Hour {
		t.Errorf("cycle trend = %+v", pts)
	}

	ttfr, err := TTFRDistribution(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	// Samples: 12h (PR1) → "<1d"; 24h exactly (PR2, PR3) → "<3d".
	if ttfr.Counts[2] != 1 || ttfr.Counts[3] != 2 || ttfr.Total() != 3 {
		t.Errorf("ttfr dist = %+v", ttfr)
	}

	sizes, err := SizeDistribution(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	// PR1=120 (M), PR2=10 (S: XS is <10 strictly), PR3=60 (M).
	if sizes.Counts[1] != 1 || sizes.Counts[2] != 2 || sizes.Total() != 3 {
		t.Errorf("size dist = %+v", sizes)
	}

	m, err := ReviewMatrix(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Logins) != 2 || m.Logins[0] != "alice" || m.Logins[1] != "bob" {
		t.Fatalf("matrix logins = %v", m.Logins)
	}
	if len(m.Authors) != 2 { // no member reviews on non-member PRs here
		t.Fatalf("matrix authors = %v", m.Authors)
	}
	if m.Counts[0][1] != 1 { // alice reviewed bob's PR3 once
		t.Errorf("alice→bob = %d, want 1", m.Counts[0][1])
	}
	if m.Counts[1][0] != 3 || m.Max != 3 { // bob reviewed alice's PR1 twice + PR2
		t.Errorf("bob→alice = %d max %d, want 3/3", m.Counts[1][0], m.Max)
	}

	punch, err := PunchCard(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	// 3 member PR opens in window + member reviews R1b,R1c,R2b,R3a = 7.
	if punch.Total != 7 {
		t.Errorf("punch total = %d, want 7", punch.Total)
	}

	aging, err := OpenAging(store.DB, f, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	// Only PR2 is open (5d old → "<1w").
	if aging.Total != 1 || aging.Buckets.Counts[2] != 1 {
		t.Errorf("aging = %+v", aging)
	}
	if len(aging.Stalest) != 1 || aging.Stalest[0].Number != 2 || aging.Stalest[0].AgeDays != 5 {
		t.Errorf("stalest = %+v", aging.Stalest)
	}

}

// TestReviewMatrixOthers exercises the "(others)" column the base fixture
// never produces: reviews members gave on non-member PRs. Human outsiders
// fold into one aggregate column that must not set the heat scale; reviews
// on bot-authored PRs (is_bot flag or config glob) vanish entirely.
func TestReviewMatrixOthers(t *testing.T) {
	store, teamID, repoID := fixture(t)
	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }

	prs := []db.PullRequest{
		// Two human outsiders: bob's per-author counts (3 and 2) aggregate to
		// 5 in his (others) cell — more than any member↔member cell.
		{
			ID: "PR7", RepoID: repoID, Number: 7, Author: "outsider", Title: "drive-by",
			State: "OPEN", CreatedAt: at(6 * day), UpdatedAt: at(2 * day),
			Reviews: []db.Review{
				{ID: "R7a", Author: "bob", State: "COMMENTED", SubmittedAt: at(5 * day)},
				{ID: "R7b", Author: "bob", State: "CHANGES_REQUESTED", SubmittedAt: at(4 * day)},
				{ID: "R7c", Author: "bob", State: "APPROVED", SubmittedAt: at(3 * day)},
				{ID: "R7d", Author: "alice", State: "APPROVED", SubmittedAt: at(3 * day)},
			},
		},
		{
			ID: "PR8", RepoID: repoID, Number: 8, Author: "visitor", Title: "drive-by 2",
			State: "OPEN", CreatedAt: at(6 * day), UpdatedAt: at(2 * day),
			Reviews: []db.Review{
				{ID: "R8a", Author: "bob", State: "APPROVED", SubmittedAt: at(2 * day)},
				{ID: "R8b", Author: "bob", State: "COMMENTED", SubmittedAt: at(1 * day)},
			},
		},
		// Bot-authored PRs: the is_bot flag and the config glob each have to
		// keep their reviews out of (others) on their own.
		{
			ID: "PR9", RepoID: repoID, Number: 9, Author: "dependabot[bot]",
			AuthorIsBot: true, Title: "bump", State: "OPEN",
			CreatedAt: at(6 * day), UpdatedAt: at(2 * day),
			Reviews: []db.Review{
				{ID: "R9a", Author: "bob", State: "APPROVED", SubmittedAt: at(2 * day)},
			},
		},
		{
			ID: "PR10", RepoID: repoID, Number: 10, Author: "renovate-gtw",
			Title: "bump 2", State: "OPEN",
			CreatedAt: at(6 * day), UpdatedAt: at(2 * day),
			Reviews: []db.Review{
				{ID: "R10a", Author: "alice", State: "APPROVED", SubmittedAt: at(2 * day)},
			},
		},
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}

	m, err := ReviewMatrix(store.DB, Filter{
		TeamID: teamID,
		Bots:   config.NewBotMatcher(config.Default().ExcludeBots),
	}, LastDays(30, fixedNow))
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Authors) != 3 || m.Authors[2] != "(others)" {
		t.Fatalf("authors = %v, want [alice bob (others)]", m.Authors)
	}
	oth := len(m.Logins)       // trailing column
	if m.Counts[0][oth] != 1 { // alice: PR7 only; renovate-gtw glob-filtered
		t.Errorf("alice→(others) = %d, want 1", m.Counts[0][oth])
	}
	if m.Counts[1][oth] != 5 { // bob: 3 on PR7 + 2 on PR8; dependabot filtered
		t.Errorf("bob→(others) = %d, want 5", m.Counts[1][oth])
	}
	// The aggregate column exceeds every real cell; the heat scale must stay
	// pinned to the member↔member maximum (bob→alice = 3 from the fixture).
	if m.Max != 3 {
		t.Errorf("Max = %d, want 3 (member↔member cells only)", m.Max)
	}
}

// TestTeamMediansAndCoverage covers the tile-delta inputs: team-level p50s,
// the previous-window arithmetic, and the coverage gate that decides whether
// a window-over-window comparison is honest.
// Every team-level aggregate must drop hidden members and bot members. The
// per-member metrics get this for free by filtering their result rows
// against the visible member list; these have no such list, so each one
// needs the filter in SQL. The fixture gives carol (hidden) and
// autoreview-svc (flagged is_bot, matching no glob) real merged PRs and a
// review, so any leak moves one of these numbers.
func TestChartsExcludeHiddenAndBotMembers(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}
	w := LastDays(30, fixedNow)

	rows, err := TeamStats(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 {
		t.Errorf("rows = %d, want 2 (carol hidden, autoreview-svc is a bot): %+v", len(rows), rows)
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
	// alice PR1+PR2 opened, PR1 merged; bob PR3 opened and merged.
	if opened != 3 || merged != 2 {
		t.Errorf("throughput opened/merged = %d/%d, want 3/2", opened, merged)
	}

	trend, err := CycleTrend(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, p := range trend {
		total += p.Merged
	}
	if total != 2 { // PR1 and PR3 only
		t.Errorf("cycle trend merges = %d, want 2", total)
	}

	sizes, err := SizeDistribution(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if sizes.Total() != 3 { // PR1, PR2, PR3
		t.Errorf("size dist total = %d, want 3: %+v", sizes.Total(), sizes)
	}

	// carol's PR5 was reviewed by a non-member, non-bot outsider, so it
	// yields a sample unless the hidden author is filtered out.
	ttfr, err := TTFRDistribution(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if ttfr.Total() != 3 {
		t.Errorf("ttfr samples = %d, want 3: %+v", ttfr.Total(), ttfr)
	}

	// 3 opens by visible members + their 4 reviews. carol's review of PR3
	// and the outsider's review of PR5 are both excluded.
	punch, err := PunchCard(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if punch.Total != 7 {
		t.Errorf("punch total = %d, want 7", punch.Total)
	}

	m, err := ReviewMatrix(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Logins) != 2 || m.Logins[0] != "alice" || m.Logins[1] != "bob" {
		t.Errorf("matrix axis = %v, want [alice bob]", m.Logins)
	}
}

func TestTeamMediansAndCoverage(t *testing.T) {
	store, teamID, repoID := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(config.Default().ExcludeBots)}
	w := LastDays(30, fixedNow)

	cycle, ttfr, err := TeamMedians(store.DB, f, w)
	if err != nil {
		t.Fatal(err)
	}
	if cycle <= 0 || ttfr <= 0 {
		t.Errorf("team medians cycle=%v ttfr=%v, want both > 0", cycle, ttfr)
	}

	prev := PrevWindow(w)
	if prev.End != w.Start || prev.End-prev.Start != w.End-w.Start {
		t.Errorf("PrevWindow = %+v for %+v", prev, w)
	}

	if _, ok := CoverageFloor(store.DB, teamID); ok {
		t.Error("coverage should be unknown before any repo has synced")
	}
	floor := fixedNow.AddDate(0, 0, -120).Unix()
	if err := store.SetSyncState(db.SyncState{RepoID: repoID, BackfillUntil: &floor}); err != nil {
		t.Fatal(err)
	}
	got, ok := CoverageFloor(store.DB, teamID)
	if !ok || got != floor {
		t.Errorf("coverage floor = %d/%v, want %d/true", got, ok, floor)
	}
}

// TestThroughputAdaptiveBuckets: every window preset should produce ~30
// buckets so the columns fill the chart width like the daily 30d view does.
func TestThroughputAdaptiveBuckets(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}

	wantSize := map[int]time.Duration{
		7:  6 * time.Hour,
		14: 12 * time.Hour,
		30: 24 * time.Hour,
		90: 72 * time.Hour,
	}
	for days, want := range wantSize {
		if got := BucketSize(LastDays(days, fixedNow)); got != want {
			t.Errorf("%dd bucket size = %v, want %v", days, got, want)
		}
	}

	for days := range wantSize {
		w := LastDays(days, fixedNow)
		buckets, err := Throughput(store.DB, f, w)
		if err != nil {
			t.Fatal(err)
		}
		if len(buckets) < 24 || len(buckets) > 36 {
			t.Errorf("%dd: %d buckets, want 24–36", days, len(buckets))
		}
		step := BucketSize(w)
		for i := 1; i < len(buckets); i++ {
			if got := buckets[i].Day.Sub(buckets[i-1].Day); got != step {
				t.Fatalf("%dd: bucket %d step = %v, want %v", days, i, got, step)
			}
		}
	}
}

func TestPersonBreakdownAndThroughput(t *testing.T) {
	store, teamID, _ := fixture(t)
	f := Filter{TeamID: teamID, Bots: config.NewBotMatcher(nil)}
	w := LastDays(30, fixedNow)

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
