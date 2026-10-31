package db

import (
	"reflect"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
)

// factsFixture is one team over two repos with every kind of review the
// classification pass must tell apart: in acme/api a COMMENTED review with
// one comment (a candidate), one with three comments and an approval with
// one comment (never candidates); in acme/web another candidate that a
// pass scoped to acme/api must leave alone.
func factsFixture(t *testing.T) (*Store, map[string]int64) {
	t.Helper()
	s := testStore(t)
	_, repoIDs, err := s.MirrorTeam(config.Team{
		Name: "t", Org: "acme",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}, {Owner: "acme", Name: "web"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SavePullRequests([]PullRequest{
		{
			ID: "PR_API", RepoID: repoIDs["acme/api"], Number: 1, Author: "alice", Title: "api",
			State: "OPEN", CreatedAt: 1000, UpdatedAt: 5000,
			Reviews: []Review{
				{ID: "RV_old", Author: "bob", State: "COMMENTED", SubmittedAt: 2000, CommentCount: 1},
				{ID: "RV_new", Author: "bob", State: "COMMENTED", SubmittedAt: 4000, CommentCount: 1},
				{ID: "RV_multi", Author: "bob", State: "COMMENTED", SubmittedAt: 3000, CommentCount: 3},
				{ID: "RV_appr", Author: "bob", State: "APPROVED", SubmittedAt: 3500, CommentCount: 1},
			},
		},
		{
			ID: "PR_WEB", RepoID: repoIDs["acme/web"], Number: 1, Author: "bob", Title: "web",
			State: "OPEN", CreatedAt: 1000, UpdatedAt: 5000,
			Reviews: []Review{
				{ID: "RV_web", Author: "alice", State: "COMMENTED", SubmittedAt: 4500, CommentCount: 1},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}
	return s, repoIDs
}

func replies(t *testing.T, s *Store) []string {
	t.Helper()
	rs, err := s.DB.Query(`SELECT id FROM thread_replies ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rs.Close()
	var out []string
	for rs.Next() {
		var id string
		if err := rs.Scan(&id); err != nil {
			t.Fatal(err)
		}
		out = append(out, id)
	}
	if err := rs.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

// Only a COMMENTED review holding exactly one comment is worth a lookup,
// only within the repos asked for, newest first, and each one is asked
// for until it has a facts row.
func TestUnclassifiedReviewsAreCandidatesInScopeNewestFirst(t *testing.T) {
	s, repoIDs := factsFixture(t)
	api := []int64{repoIDs["acme/api"]}

	got, err := s.ListUnclassifiedReviews(api, 10)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"RV_new", "RV_old"}; !reflect.DeepEqual(got, want) {
		t.Errorf("candidates = %v, want %v", got, want)
	}
	if n, _ := s.CountUnclassifiedReviews(api); n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
	if got, _ := s.ListUnclassifiedReviews(api, 1); !reflect.DeepEqual(got, []string{"RV_new"}) {
		t.Errorf("limit 1 = %v, want the newest", got)
	}
	both := []int64{repoIDs["acme/api"], repoIDs["acme/web"]}
	if n, _ := s.CountUnclassifiedReviews(both); n != 3 {
		t.Errorf("count over both repos = %d, want 3", n)
	}
	if n, _ := s.CountUnclassifiedReviews(nil); n != 0 {
		t.Errorf("count over no repos = %d, want 0", n)
	}
}

// The view is the one definition of a thread reply: no body, one comment,
// replying to a thread. A review the API no longer knows gets NULL facts,
// which keeps it a review and keeps it off the next list.
func TestReviewFactsDefineThreadReplies(t *testing.T) {
	s, repoIDs := factsFixture(t)
	api := []int64{repoIDs["acme/api"]}

	if err := s.SaveReviewFacts([]ReviewFacts{
		{ReviewID: "RV_new", Known: true, HasBody: false, FirstReply: true},
		{ReviewID: "RV_old", Known: false},
		// Facts the pass would never fetch, to show the view needs the
		// comment count too: a reply-shaped fact on a three-comment
		// review makes no reply.
		{ReviewID: "RV_multi", Known: true, HasBody: false, FirstReply: true},
	}); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, s); !reflect.DeepEqual(got, []string{"RV_new"}) {
		t.Errorf("thread_replies = %v, want [RV_new]", got)
	}
	if n, _ := s.CountUnclassifiedReviews(api); n != 0 {
		t.Errorf("count after facts = %d, want 0: an unknown review must not be asked for again", n)
	}
	if n := count(t, s, `SELECT COUNT(*) FROM review_facts WHERE review_id = 'RV_old' AND has_body IS NULL AND first_reply IS NULL`); n != 1 {
		t.Error("an unknown review should carry NULL facts")
	}

	// A body, or a comment that opens a thread, is a review.
	if err := s.SaveReviewFacts([]ReviewFacts{
		{ReviewID: "RV_new", Known: true, HasBody: true, FirstReply: true},
	}); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, s); len(got) != 0 {
		t.Errorf("a review with a body is in thread_replies: %v", got)
	}
	if err := s.SaveReviewFacts([]ReviewFacts{
		{ReviewID: "RV_new", Known: true, HasBody: false, FirstReply: false},
	}); err != nil {
		t.Fatal(err)
	}
	if got := replies(t, s); len(got) != 0 {
		t.Errorf("a single comment that opens a thread is in thread_replies: %v", got)
	}
}

// SavePullRequests replaces a PR's reviews on every re-sync. The facts
// live beside the reviews precisely so that they survive it; otherwise
// every touched PR would be re-fetched on every sync.
func TestReviewFactsSurviveResync(t *testing.T) {
	s, repoIDs := factsFixture(t)
	api := []int64{repoIDs["acme/api"]}
	if err := s.SaveReviewFacts([]ReviewFacts{
		{ReviewID: "RV_new", Known: true, FirstReply: true},
		{ReviewID: "RV_old", Known: true, HasBody: true},
	}); err != nil {
		t.Fatal(err)
	}

	// Re-save the PR as the next walk would, reviews and all.
	if err := s.SavePullRequests([]PullRequest{{
		ID: "PR_API", RepoID: repoIDs["acme/api"], Number: 1, Author: "alice", Title: "api",
		State: "MERGED", CreatedAt: 1000, UpdatedAt: 6000,
		Reviews: []Review{
			{ID: "RV_old", Author: "bob", State: "COMMENTED", SubmittedAt: 2000, CommentCount: 1},
			{ID: "RV_new", Author: "bob", State: "COMMENTED", SubmittedAt: 4000, CommentCount: 1},
		},
	}}); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.CountUnclassifiedReviews(api); n != 0 {
		t.Errorf("count after re-sync = %d, want 0", n)
	}
	if got := replies(t, s); !reflect.DeepEqual(got, []string{"RV_new"}) {
		t.Errorf("thread_replies after re-sync = %v, want [RV_new]", got)
	}
}
