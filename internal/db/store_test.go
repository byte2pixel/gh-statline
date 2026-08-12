package db

import (
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	sqldb, err := Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { sqldb.Close() })
	return NewStore(sqldb)
}

func count(t *testing.T, s *Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := s.DB.QueryRow(query, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestDeleteTeamCascades(t *testing.T) {
	s := testStore(t)
	_, _, err := s.MirrorTeam(config.Team{
		Name:    "testers",
		Members: []config.Member{{Login: "alice"}, {Login: "bob"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	})
	if err != nil {
		t.Fatal(err)
	}

	if err := s.DeleteTeam("testers"); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"teams", "team_members", "team_repos"} {
		if n := count(t, s, "SELECT COUNT(*) FROM "+table); n != 0 {
			t.Errorf("%s: %d rows left after delete", table, n)
		}
	}
	// The repos row is shared cache and must survive.
	if n := count(t, s, "SELECT COUNT(*) FROM repos"); n != 1 {
		t.Errorf("repos: got %d rows, want 1", n)
	}
}

func TestDeleteTeamLeavesOtherTeams(t *testing.T) {
	s := testStore(t)
	shared := []config.Repo{{Owner: "acme", Name: "api"}}
	for _, name := range []string{"one", "two"} {
		_, _, err := s.MirrorTeam(config.Team{
			Name:    name,
			Members: []config.Member{{Login: "alice"}},
			Repos:   shared,
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	if err := s.DeleteTeam("one"); err != nil {
		t.Fatal(err)
	}
	var id int64
	if err := s.DB.QueryRow(`SELECT id FROM teams WHERE name = 'two'`).Scan(&id); err != nil {
		t.Fatal("surviving team missing:", err)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM team_members WHERE team_id = ?", id); n != 1 {
		t.Errorf("surviving team_members: got %d, want 1", n)
	}
	if n := count(t, s, "SELECT COUNT(*) FROM team_repos WHERE team_id = ?", id); n != 1 {
		t.Errorf("surviving team_repos: got %d, want 1", n)
	}
}
