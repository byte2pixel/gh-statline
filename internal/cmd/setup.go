package cmd

import (
	"database/sql"
	"fmt"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/syncer"
)

// appEnv is everything a command needs after startup: parsed config, the
// selected team mirrored into the DB, and sync targets for its repos.
type appEnv struct {
	Cfg     config.Config
	Team    config.Team
	TeamID  int64
	DB      *sql.DB
	Store   *db.Store
	Targets []syncer.Target
}

func (a *appEnv) Close() {
	if a.DB != nil {
		a.DB.Close()
	}
}

// bootstrap loads config, opens the cache DB, and mirrors the selected team
// (teamName == "" means the config's default team).
func bootstrap(teamName string) (*appEnv, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, err
	}
	team, ok := cfg.TeamByName(teamName)
	if !ok {
		return nil, fmt.Errorf("team %q not found in config", teamName)
	}

	dbPath, err := config.DBPath()
	if err != nil {
		return nil, err
	}
	sqldb, err := db.Open(dbPath)
	if err != nil {
		return nil, err
	}

	store := db.NewStore(sqldb)
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		sqldb.Close()
		return nil, err
	}

	targets := make([]syncer.Target, 0, len(team.Repos))
	for _, r := range team.Repos {
		targets = append(targets, syncer.Target{Owner: r.Owner, Name: r.Name, RepoID: repoIDs[r.String()]})
	}

	return &appEnv{Cfg: cfg, Team: team, TeamID: teamID, DB: sqldb, Store: store, Targets: targets}, nil
}
