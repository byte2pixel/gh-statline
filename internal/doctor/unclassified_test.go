package doctor

import (
	"strings"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// The summary names what the numbers still carry: reviews that may be
// thread replies but have not been classified yet (#105).
func TestSummaryNamesUnclassifiedReviews(t *testing.T) {
	states := []db.RepoSyncState{
		state("acme", "api", db.SyncState{LastSyncedAt: ago(1), WatermarkUpdated: ago(1)}),
	}
	rep := Build(team("platform"), states, 0, false, fixedNow)
	if strings.Contains(rep.Summary(), "awaiting classification") {
		t.Errorf("summary mentions classification with nothing pending: %q", rep.Summary())
	}
	rep.Unclassified = 1
	if !strings.Contains(rep.Summary(), "1 review awaiting classification") {
		t.Errorf("summary = %q, want the singular count", rep.Summary())
	}
	rep.Unclassified = 240
	if !strings.Contains(rep.Summary(), "240 reviews awaiting classification") {
		t.Errorf("summary = %q, want the count", rep.Summary())
	}
}

// Load counts the candidates in the team's own repos and nothing else.
func TestLoadCountsUnclassifiedReviews(t *testing.T) {
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	store := db.NewStore(sqldb)

	cfg := config.Team{
		Name: "platform", Org: "acme",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	}
	teamID, repoIDs, err := store.MirrorTeam(cfg)
	if err != nil {
		t.Fatal(err)
	}
	otherID, err := store.UpsertRepo("acme", "elsewhere")
	if err != nil {
		t.Fatal(err)
	}
	if err := store.SavePullRequests([]db.PullRequest{
		{
			ID: "PR_A", RepoID: repoIDs["acme/api"], Number: 1, Author: "alice", Title: "a",
			State: "OPEN", CreatedAt: 1000, UpdatedAt: 2000,
			Reviews: []db.Review{
				{ID: "RV_cand", Author: "bob", State: "COMMENTED", SubmittedAt: 1500, CommentCount: 1},
				{ID: "RV_appr", Author: "bob", State: "APPROVED", SubmittedAt: 1600, CommentCount: 1},
			},
		},
		{
			ID: "PR_X", RepoID: otherID, Number: 1, Author: "alice", Title: "x",
			State: "OPEN", CreatedAt: 1000, UpdatedAt: 2000,
			Reviews: []db.Review{
				{ID: "RV_other", Author: "bob", State: "COMMENTED", SubmittedAt: 1500, CommentCount: 1},
			},
		},
	}); err != nil {
		t.Fatal(err)
	}

	rep, err := Load(store, cfg, teamID, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unclassified != 1 {
		t.Errorf("Unclassified = %d, want 1: one candidate in the team's repo", rep.Unclassified)
	}
	if err := store.SaveReviewFacts([]db.ReviewFacts{{ReviewID: "RV_cand", Known: true, FirstReply: true}}); err != nil {
		t.Fatal(err)
	}
	rep, err = Load(store, cfg, teamID, fixedNow)
	if err != nil {
		t.Fatal(err)
	}
	if rep.Unclassified != 0 {
		t.Errorf("Unclassified after facts = %d, want 0", rep.Unclassified)
	}
}
