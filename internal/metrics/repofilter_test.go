package metrics

import (
	"testing"

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
