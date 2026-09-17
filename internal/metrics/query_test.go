package metrics

import (
	"database/sql"
	"reflect"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// Every metric query binds the same named set: the argument order that
// weak point 4 warned about no longer exists. The repo filter travels as
// one JSON array, NULL when the filter is off, so a query has exactly one
// :repos to consult however many ids the picker chose.
func TestNamedArgsEncodeTheRepoFilterAsJSON(t *testing.T) {
	w := Window{Start: 10, End: 20}
	got := namedArgs(Filter{TeamID: 7, RepoIDs: []int64{3, 1}}, w)
	want := []any{
		sql.Named("team", int64(7)), sql.Named("start", int64(10)),
		sql.Named("end", int64(20)), sql.Named("repos", "[3,1]"),
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("namedArgs = %#v, want %#v", got, want)
	}
	// nil and empty both mean every team repo, and both bind NULL: an empty
	// JSON array would match nothing.
	for _, ids := range [][]int64{nil, {}} {
		args := namedArgs(Filter{TeamID: 7, RepoIDs: ids}, w)
		if repos := args[3].(sql.NamedArg); repos.Name != "repos" || repos.Value != nil {
			t.Errorf("RepoIDs %#v bound %#v, want NULL", ids, repos)
		}
	}
}

// The fragments run against the real schema: repoScope through json_each,
// and the two view predicates over a roster with a hidden member, a typed
// bot, and a glob-matched member.
func TestFragmentsScopeThroughTheViews(t *testing.T) {
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)
	teamID, repoIDs, err := store.MirrorTeam(config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{
			{Login: "alice"}, {Login: "carol", Hidden: true},
			{Login: "autoreview-svc"}, {Login: "renovate-gtw"},
		},
		Repos: []config.Repo{{Owner: "acme", Name: "api"}, {Owner: "acme", Name: "web"}, {Owner: "acme", Name: "infra"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := store.MirrorBotGlobs(config.Default().ExcludeBots); err != nil {
		t.Fatal(err)
	}
	var prs []db.PullRequest
	for i, repo := range []string{"acme/api", "acme/web", "acme/infra"} {
		prs = append(prs, db.PullRequest{
			ID: repo, RepoID: repoIDs[repo], Number: i + 1, Author: "alice", Title: repo,
			State: "OPEN", CreatedAt: 5, UpdatedAt: 5,
			Reviews: []db.Review{
				{ID: repo + "-svc", Author: "autoreview-svc", AuthorIsBot: true, State: "COMMENTED", SubmittedAt: 6},
				{ID: repo + "-ren", Author: "renovate-gtw", State: "COMMENTED", SubmittedAt: 7},
				{ID: repo + "-carol", Author: "carol", State: "APPROVED", SubmittedAt: 8},
				{ID: repo + "-bob", Author: "bob", State: "APPROVED", SubmittedAt: 9},
			},
		})
	}
	if err := store.SavePullRequests(prs); err != nil {
		t.Fatal(err)
	}

	count := func(t *testing.T, q string, f Filter) int {
		t.Helper()
		var n int
		if err := sqldb.QueryRow(q, namedArgs(f, Window{})...).Scan(&n); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
		return n
	}
	prsQ := `SELECT COUNT(*) FROM pull_requests p` + teamRepos() + ` WHERE 1 = 1` + repoScope()
	for _, c := range []struct {
		ids  []int64
		want int
	}{
		{nil, 3},
		{[]int64{repoIDs["acme/api"]}, 1},
		{[]int64{repoIDs["acme/api"], repoIDs["acme/infra"]}, 2},
		{[]int64{999}, 0},
	} {
		if got := count(t, prsQ, Filter{TeamID: teamID, RepoIDs: c.ids}); got != c.want {
			t.Errorf("repoScope with %v = %d PRs, want %d", c.ids, got, c.want)
		}
	}

	// Reviewers on the roster and visible: alice never reviews her own PRs,
	// carol is hidden, autoreview-svc is typed Bot, renovate-gtw matches a
	// glob, bob is not a member. Nothing survives.
	reviewersQ := `SELECT COUNT(*) FROM reviews r JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() +
		` WHERE 1 = 1` + visibleMember("r.author_login")
	if got := count(t, reviewersQ, Filter{TeamID: teamID}); got != 0 {
		t.Errorf("visibleMember kept %d reviews, want 0", got)
	}
	// Not a bot: carol and bob remain, on each of the three PRs.
	humansQ := `SELECT COUNT(*) FROM reviews r JOIN pull_requests p ON p.id = r.pr_id` + teamRepos() +
		` WHERE 1 = 1` + notBot("r.author_login")
	if got := count(t, humansQ, Filter{TeamID: teamID}); got != 6 {
		t.Errorf("notBot kept %d reviews, want 6", got)
	}
	// Authors: alice is the only visible author; every PR is hers.
	authorsQ := `SELECT COUNT(*) FROM pull_requests p` + teamRepos() + ` WHERE 1 = 1` + visibleMember("p.author_login")
	if got := count(t, authorsQ, Filter{TeamID: teamID}); got != 3 {
		t.Errorf("visibleMember on authors kept %d PRs, want 3", got)
	}
}
