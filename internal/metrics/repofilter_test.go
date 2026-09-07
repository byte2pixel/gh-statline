package metrics

import (
	"fmt"
	"testing"
	"time"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// Filter.RepoIDs narrows every query to a subset of the team repos. The base
// fixture has one repo, so the non-empty branch of repoCond had no coverage
// at all: an appended condition whose args land out of order compiles fine
// and returns wrong numbers, which is the failure mode AGENTS.md warns about
// for this whole file.

// twoRepoFixture gives alice a merged PR in each of two repos, each with one
// review and one comment from bob, so a filter that works halves every
// number and a filter that silently does nothing changes none.
func twoRepoFixture(t *testing.T) (*db.Store, int64, map[string]int64) {
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
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}, {Owner: "acme", Name: "web"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }

	pr := func(id string, repoID int64, number int) db.PullRequest {
		return db.PullRequest{
			ID: id, RepoID: repoID, Number: number, Author: "alice", Title: id,
			State: "MERGED", CreatedAt: at(10 * day), MergedAt: i64(at(9 * day)),
			UpdatedAt: at(9 * day), Additions: 100, Deletions: 20, ChangedFiles: 3,
			Reviews: []db.Review{{
				ID: id + "-R", Author: "bob", State: "APPROVED",
				SubmittedAt: at(10*day) + day/2, CommentCount: 1,
			}},
			Comments: []db.IssueComment{{ID: id + "-C", Author: "bob", CreatedAt: at(9 * day)}},
		}
	}
	if err := store.SavePullRequests([]db.PullRequest{
		pr("PR_API", repoIDs["acme/api"], 1),
		pr("PR_WEB", repoIDs["acme/web"], 2),
	}); err != nil {
		t.Fatal(err)
	}
	return store, teamID, repoIDs
}

func repoFilter(teamID int64, repoIDs ...int64) Filter {
	return Filter{
		TeamID:  teamID,
		RepoIDs: repoIDs,
		Bots:    config.NewBotMatcher(config.Default().ExcludeBots),
	}
}

// Every per-member number is repo-scoped: the counts, the counterparty
// comment counts, and the medians that feed the team table.
func TestTeamStatsHonoursRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	w := LastDays(30, fixedNow)

	rowsFor := func(t *testing.T, f Filter) map[string]Row {
		t.Helper()
		rows, err := TeamStats(store.DB, f, w)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]Row{}
		for _, r := range rows {
			out[r.Login] = r
		}
		return out
	}

	both := rowsFor(t, repoFilter(teamID))
	if got := both["alice"].PRsOpened; got != 2 {
		t.Errorf("unfiltered PRsOpened = %d, want 2", got)
	}
	if got := both["alice"].PRsMerged; got != 2 {
		t.Errorf("unfiltered PRsMerged = %d, want 2", got)
	}
	if got := both["bob"].ReviewsGiven; got != 2 {
		t.Errorf("unfiltered ReviewsGiven = %d, want 2", got)
	}
	// Two per PR: the conversation comment plus the review thread comment,
	// which both count. The point here is that the filter halves them.
	if got := both["alice"].CommentsRecv; got != 4 {
		t.Errorf("unfiltered CommentsRecv = %d, want 4", got)
	}
	if got := both["bob"].CommentsGiven; got != 4 {
		t.Errorf("unfiltered CommentsGiven = %d, want 4", got)
	}

	api := rowsFor(t, repoFilter(teamID, repoIDs["acme/api"]))
	if got := api["alice"].PRsOpened; got != 1 {
		t.Errorf("filtered PRsOpened = %d, want 1", got)
	}
	if got := api["alice"].PRsMerged; got != 1 {
		t.Errorf("filtered PRsMerged = %d, want 1", got)
	}
	if got := api["bob"].ReviewsGiven; got != 1 {
		t.Errorf("filtered ReviewsGiven = %d, want 1", got)
	}
	if got := api["alice"].CommentsRecv; got != 2 {
		t.Errorf("filtered CommentsRecv = %d, want 2", got)
	}
	if got := api["bob"].CommentsGiven; got != 2 {
		t.Errorf("filtered CommentsGiven = %d, want 2", got)
	}
	// Both PRs are identical in shape, so the medians must survive the cut
	// rather than collapse to the no-data sentinels.
	if got := api["alice"].CycleTimeP50; got != both["alice"].CycleTimeP50 || got == 0 {
		t.Errorf("filtered CycleTimeP50 = %v, want the unfiltered %v", got, both["alice"].CycleTimeP50)
	}
	if got := api["alice"].TTFRP50; got != both["alice"].TTFRP50 || got == 0 {
		t.Errorf("filtered TTFRP50 = %v, want the unfiltered %v", got, both["alice"].TTFRP50)
	}
	if got := api["alice"].SizeP50; got != both["alice"].SizeP50 || got < 0 {
		t.Errorf("filtered SizeP50 = %d, want the unfiltered %d", got, both["alice"].SizeP50)
	}
}

// The chart and trend entry points assemble their own SQL and append the
// repo condition separately, so each one needs its own check.
func TestChartsHonourRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	w := LastDays(30, fixedNow)
	all := repoFilter(teamID)
	one := repoFilter(teamID, repoIDs["acme/api"])

	sum := func(t *testing.T, f Filter) (opened, merged int) {
		t.Helper()
		buckets, err := Throughput(store.DB, f, w)
		if err != nil {
			t.Fatal(err)
		}
		for _, b := range buckets {
			opened += b.Opened
			merged += b.Merged
		}
		return opened, merged
	}
	if o, m := sum(t, all); o != 2 || m != 2 {
		t.Errorf("unfiltered throughput = (%d opened, %d merged), want (2, 2)", o, m)
	}
	if o, m := sum(t, one); o != 1 || m != 1 {
		t.Errorf("filtered throughput = (%d opened, %d merged), want (1, 1)", o, m)
	}

	reviews := func(t *testing.T, f Filter) int {
		t.Helper()
		mx, err := ReviewMatrix(store.DB, f, w)
		if err != nil {
			t.Fatal(err)
		}
		total := 0
		for _, row := range mx.Counts {
			for _, n := range row {
				total += n
			}
		}
		return total
	}
	if got := reviews(t, all); got != 2 {
		t.Errorf("unfiltered matrix total = %d, want 2", got)
	}
	if got := reviews(t, one); got != 1 {
		t.Errorf("filtered matrix total = %d, want 1", got)
	}

	// Both PRs merged in the same week, so filtering halves the sample count
	// without moving the median.
	trend := func(t *testing.T, f Filter) (points int, merged int) {
		t.Helper()
		ps, err := CycleTrend(store.DB, f, w)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range ps {
			merged += p.Merged
		}
		return len(ps), merged
	}
	if _, m := trend(t, all); m != 2 {
		t.Errorf("unfiltered cycle trend merged = %d, want 2", m)
	}
	if _, m := trend(t, one); m != 1 {
		t.Errorf("filtered cycle trend merged = %d, want 1", m)
	}

	breakdown := func(t *testing.T, f Filter) []RepoBreakdown {
		t.Helper()
		rb, err := PersonRepos(store.DB, f, w, "alice")
		if err != nil {
			t.Fatal(err)
		}
		return rb
	}
	if got := breakdown(t, all); len(got) != 2 {
		t.Errorf("unfiltered person breakdown covers %d repos, want 2", len(got))
	}
	if got := breakdown(t, one); len(got) != 1 || got[0].Repo != "acme/api" {
		t.Errorf("filtered person breakdown = %+v, want acme/api alone", got)
	}
}

// nil and empty mean the same thing — all of the team repos — and neither
// may be mistaken for "no repos at all".
func TestEmptyRepoIDsMeansEveryTeamRepo(t *testing.T) {
	store, teamID, _ := twoRepoFixture(t)
	w := LastDays(30, fixedNow)

	cases := map[string]Filter{
		"nil":   repoFilter(teamID),
		"empty": repoFilter(teamID, []int64{}...),
	}
	// Spreading a slice into a variadic parameter passes that slice through
	// instead of allocating a new one, so these really are the two shapes and
	// not the same one twice. repoCond keys on len alone, but a future reader
	// should not have to take that on faith.
	if cases["nil"].RepoIDs != nil {
		t.Fatalf("the nil case is not nil: %#v", cases["nil"].RepoIDs)
	}
	if cases["empty"].RepoIDs == nil {
		t.Fatalf("the empty case collapsed to nil, so only one shape is covered")
	}

	for name, f := range cases {
		t.Run(name, func(t *testing.T) {
			rows, err := TeamStats(store.DB, f, w)
			if err != nil {
				t.Fatal(err)
			}
			for _, r := range rows {
				if r.Login == "alice" && r.PRsOpened != 2 {
					t.Errorf("RepoIDs %#v gave alice %d PRs, want both", f.RepoIDs, r.PRsOpened)
				}
			}
		})
	}
}

// withSyncState records a completed walk for every team repo reaching back
// days days. CoverageFloor gates the trend length and the tile deltas on
// it, so without a row here those two never see the filter at all.
func withSyncState(t *testing.T, store *db.Store, repoIDs map[string]int64, days int64) {
	t.Helper()
	now := fixedNow.Unix()
	floor := now - days*day
	for _, id := range repoIDs {
		st := db.SyncState{RepoID: id, WatermarkUpdated: &now, BackfillUntil: &floor, LastSyncedAt: &now}
		if err := store.SetSyncState(st); err != nil {
			t.Fatal(err)
		}
	}
}

// withOpenPRs gives alice one open, non-draft PR per repo, two days old.
// They are opened in-window, so they would shift every count the tests
// above pin; only the aging test, which has no window, asks for them.
func withOpenPRs(t *testing.T, store *db.Store, repoIDs map[string]int64) {
	t.Helper()
	now := fixedNow.Unix()
	var prs []db.PullRequest
	for i, repo := range []string{"acme/api", "acme/web"} {
		prs = append(prs, db.PullRequest{
			ID: "OPEN_" + repo, RepoID: repoIDs[repo], Number: 10 + i, Author: "alice", Title: "wip",
			State: "OPEN", CreatedAt: now - 2*day, UpdatedAt: now - day,
			Additions: 5, Deletions: 1, ChangedFiles: 1,
		})
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}
}

// withSlowWebPRs gives alice two more merged PRs in web, each a 3-day cycle
// with bob's first review two days in. The base PRs are identical across
// repos, so the medians alone cannot tell a working filter from a no-op;
// these make web's medians differ from api's.
func withSlowWebPRs(t *testing.T, store *db.Store, repoIDs map[string]int64) {
	t.Helper()
	now := fixedNow.Unix()
	at := func(d int64) int64 { return now - d }
	i64 := func(v int64) *int64 { return &v }
	var prs []db.PullRequest
	for i := 0; i < 2; i++ {
		id := fmt.Sprintf("SLOW_%d", i)
		prs = append(prs, db.PullRequest{
			ID: id, RepoID: repoIDs["acme/web"], Number: 20 + i, Author: "alice", Title: id,
			State: "MERGED", CreatedAt: at(8 * day), MergedAt: i64(at(5 * day)),
			UpdatedAt: at(5 * day), Additions: 100, Deletions: 20, ChangedFiles: 3,
			Reviews: []db.Review{{
				ID: id + "-R", Author: "bob", State: "APPROVED", SubmittedAt: at(6 * day),
			}},
		})
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}
}

func sum(ns []int) int {
	t := 0
	for _, n := range ns {
		t += n
	}
	return t
}

// The two distributions and the punch card each assemble their own query.
// Both PRs are 120 lines (M) and waited 12h for bob (<1d); the punch card
// counts the two opens and bob's two reviews.
func TestDistributionsHonourRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	w := LastDays(30, fixedNow)
	cases := []struct {
		name string
		f    Filter
		want int
	}{
		{"unfiltered", repoFilter(teamID), 2},
		{"api only", repoFilter(teamID, repoIDs["acme/api"]), 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sizes, err := SizeDistribution(store.DB, c.f, w)
			if err != nil {
				t.Fatal(err)
			}
			if sizes.Total() != c.want || sizes.Counts[2] != c.want {
				t.Errorf("sizes = %v, want %d in M and nothing else", sizes.Counts, c.want)
			}
			ttfr, err := TTFRDistribution(store.DB, c.f, w)
			if err != nil {
				t.Fatal(err)
			}
			if ttfr.Total() != c.want || ttfr.Counts[2] != c.want {
				t.Errorf("ttfr = %v, want %d in <1d and nothing else", ttfr.Counts, c.want)
			}
			punch, err := PunchCard(store.DB, c.f, w)
			if err != nil {
				t.Fatal(err)
			}
			if punch.Total != 2*c.want {
				t.Errorf("punch total = %d, want %d", punch.Total, 2*c.want)
			}
		})
	}
}

// Aging has no window, so it gets its own open PRs.
func TestOpenAgingHonoursRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	withOpenPRs(t, store, repoIDs)

	all, err := OpenAging(store.DB, repoFilter(teamID), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if all.Total != 2 || len(all.Stalest) != 2 || all.Buckets.Counts[1] != 2 {
		t.Errorf("unfiltered aging = %+v, want two PRs in <3d", all)
	}
	web, err := OpenAging(store.DB, repoFilter(teamID, repoIDs["acme/web"]), fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if web.Total != 1 || len(web.Stalest) != 1 || web.Stalest[0].Repo != "acme/web" {
		t.Errorf("filtered aging = %+v, want acme/web alone", web)
	}
}

// The tile medians are team-level, so no per-member map catches a filter
// that fell off. With web slowed down, each scope has its own answer:
// lower-middle of {1d,1d,3d,3d} is 1d, of {1d,3d,3d} is 3d, of {1d} is 1d.
func TestTeamMediansHonourRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	withSlowWebPRs(t, store, repoIDs)
	w := LastDays(30, fixedNow)

	cases := []struct {
		name        string
		f           Filter
		cycle, ttfr time.Duration
	}{
		{"unfiltered", repoFilter(teamID), 24 * time.Hour, 12 * time.Hour},
		{"web only", repoFilter(teamID, repoIDs["acme/web"]), 72 * time.Hour, 48 * time.Hour},
		{"api only", repoFilter(teamID, repoIDs["acme/api"]), 24 * time.Hour, 12 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cycle, ttfr, err := TeamMedians(store.DB, c.f, w)
			if err != nil {
				t.Fatal(err)
			}
			if cycle != c.cycle || ttfr != c.ttfr {
				t.Errorf("medians = (%v, %v), want (%v, %v)", cycle, ttfr, c.cycle, c.ttfr)
			}
		})
	}
}

// The sparkline behind the person view runs two more queries of its own.
func TestPersonActivityHonoursRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	w := LastDays(30, fixedNow)
	total := func(t *testing.T, f Filter, login string) float64 {
		t.Helper()
		days, err := PersonActivity(store.DB, f, w, login)
		if err != nil {
			t.Fatal(err)
		}
		s := 0.0
		for _, d := range days {
			s += d
		}
		return s
	}
	all := repoFilter(teamID)
	one := repoFilter(teamID, repoIDs["acme/api"])
	// alice's opens and bob's reviews, one per repo each.
	for login, want := range map[string]float64{"alice": 2, "bob": 2} {
		if got := total(t, all, login); got != want {
			t.Errorf("unfiltered %s activity = %v, want %v", login, got, want)
		}
		if got := total(t, one, login); got != want/2 {
			t.Errorf("filtered %s activity = %v, want %v", login, got, want/2)
		}
	}
}

// The weekly series appends the condition to four queries and its TTFR
// helper to a fifth. Coverage reaches back 30 days, so four whole weeks
// are plotted; the base PRs land in week 2 by both creation and merge, the
// slow web PRs are created in week 2 and merged in week 3.
func TestTrendSeriesHonoursRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	withSlowWebPRs(t, store, repoIDs)
	withSyncState(t, store, repoIDs, 30)

	cases := []struct {
		name                      string
		f                         Filter
		opened, reviews, comments int
		cycleWeek3, ttfrWeek2     time.Duration
		aliceOpened, bobReviews   int
	}{
		{"unfiltered", repoFilter(teamID), 4, 4, 4, 72 * time.Hour, 12 * time.Hour, 4, 4},
		{"api only", repoFilter(teamID, repoIDs["acme/api"]), 1, 1, 2, 0, 12 * time.Hour, 1, 1},
		{"web only", repoFilter(teamID, repoIDs["acme/web"]), 3, 3, 2, 72 * time.Hour, 48 * time.Hour, 3, 3},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := TrendSeries(store.DB, c.f, TrendWeeks, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			if len(d.Weeks) != 4 {
				t.Fatalf("weeks = %d, want 4 (30 days of coverage)", len(d.Weeks))
			}
			if got := sum(d.Team.Opened); got != c.opened {
				t.Errorf("team opened = %d, want %d", got, c.opened)
			}
			if got := sum(d.Team.Merged); got != c.opened {
				t.Errorf("team merged = %d, want %d (every PR is merged)", got, c.opened)
			}
			if got := sum(d.Team.Reviews); got != c.reviews {
				t.Errorf("team reviews = %d, want %d", got, c.reviews)
			}
			if got := sum(d.Team.Comments); got != c.comments {
				t.Errorf("team comments = %d, want %d", got, c.comments)
			}
			if d.Team.Cycle[3] != c.cycleWeek3 {
				t.Errorf("week-3 cycle = %v, want %v", d.Team.Cycle[3], c.cycleWeek3)
			}
			if d.Team.TTFR[2] != c.ttfrWeek2 {
				t.Errorf("week-2 ttfr = %v, want %v", d.Team.TTFR[2], c.ttfrWeek2)
			}
			members := map[string]MemberTrend{}
			for _, m := range d.Members {
				members[m.Login] = m
			}
			if got := sum(members["alice"].Opened); got != c.aliceOpened {
				t.Errorf("alice opened = %d, want %d", got, c.aliceOpened)
			}
			if got := sum(members["bob"].Reviews); got != c.bobReviews {
				t.Errorf("bob reviews = %d, want %d", got, c.bobReviews)
			}
		})
	}
}

// LoadDashboard is what the app actually calls, and it runs TeamStats and
// TeamMedians a second time over the previous window for the tile deltas.
// A merged PR in web 50 days back is the only thing in that window, so the
// prior counts read 1 unfiltered, 1 for web, and 0 for api.
func TestLoadDashboardHonoursRepoIDs(t *testing.T) {
	store, teamID, repoIDs := twoRepoFixture(t)
	withSyncState(t, store, repoIDs, 70)
	now := fixedNow.Unix()
	merged := now - 49*day
	if err := store.SavePullRequests([]db.PullRequest{{
		ID: "PREV_WEB", RepoID: repoIDs["acme/web"], Number: 30, Author: "alice", Title: "old",
		State: "MERGED", CreatedAt: now - 50*day, MergedAt: &merged, UpdatedAt: merged,
		Additions: 10, Deletions: 2, ChangedFiles: 1,
		Reviews: []db.Review{{
			ID: "PREV_WEB-R", Author: "bob", State: "APPROVED", SubmittedAt: now - 50*day + day/2,
		}},
	}}); err != nil {
		t.Fatal(err)
	}
	w := LastDays(30, fixedNow)

	cases := []struct {
		name       string
		f          Filter
		opened     int // alice, current window
		prevOpened int
		prevCycle  time.Duration
	}{
		{"unfiltered", repoFilter(teamID), 2, 1, 24 * time.Hour},
		{"api only", repoFilter(teamID, repoIDs["acme/api"]), 1, 0, 0},
		{"web only", repoFilter(teamID, repoIDs["acme/web"]), 1, 1, 24 * time.Hour},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d, err := LoadDashboard(store.DB, c.f, w, fixedNow)
			if err != nil {
				t.Fatal(err)
			}
			var alice Row
			for _, r := range d.Rows {
				if r.Login == "alice" {
					alice = r
				}
			}
			if alice.PRsOpened != c.opened {
				t.Errorf("alice opened = %d, want %d", alice.PRsOpened, c.opened)
			}
			if !d.Tiles.HasPrev {
				t.Fatal("70 days of coverage should enable the previous-window tiles")
			}
			if d.Tiles.PrevOpened != c.prevOpened || d.Tiles.PrevMerged != c.prevOpened ||
				d.Tiles.PrevReviews != c.prevOpened {
				t.Errorf("prev tiles = %+v, want %d opened/merged/reviewed", d.Tiles, c.prevOpened)
			}
			if d.Tiles.PrevCycle != c.prevCycle {
				t.Errorf("prev cycle = %v, want %v", d.Tiles.PrevCycle, c.prevCycle)
			}
			// Every current-window dataset follows the same scope as the rows.
			matrix := 0
			for _, row := range d.Matrix.Counts {
				matrix += sum(row)
			}
			opened := 0
			for _, b := range d.Buckets {
				opened += b.Opened
			}
			if d.Sizes.Total() != c.opened || d.TTFR.Total() != c.opened ||
				d.Punch.Total != 2*c.opened || matrix != c.opened || opened != c.opened {
				t.Errorf("datasets disagree with the rows: sizes %d ttfr %d punch %d matrix %d throughput %d, want %d each (punch double)",
					d.Sizes.Total(), d.TTFR.Total(), d.Punch.Total, matrix, opened, c.opened)
			}
			if d.Tiles.Cycle != 24*time.Hour || d.Tiles.TTFR != 12*time.Hour {
				t.Errorf("current medians = (%v, %v), want (24h, 12h)", d.Tiles.Cycle, d.Tiles.TTFR)
			}
		})
	}
}
