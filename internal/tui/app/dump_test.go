package app

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/exp/teatest/v2"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
	"github.com/byte2pixel/gh-statline/internal/syncer"
)

// TestDumpView is a development harness, not a CI test: with STATLINE_DUMP=1
// (plus STATLINE_CONFIG/STATLINE_DB pointing at real data) it boots the app
// against that data and prints the rendered leaderboard as plain text. Run:
//
//	STATLINE_DUMP=1 go test ./internal/tui/app -run TestDumpView -v
func TestDumpView(t *testing.T) {
	if os.Getenv("STATLINE_DUMP") == "" {
		t.Skip("set STATLINE_DUMP=1 to dump the rendered view")
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	team, ok := cfg.TeamByName("")
	if !ok {
		t.Fatal("no default team")
	}
	dbPath, err := config.DBPath()
	if err != nil {
		t.Fatal(err)
	}
	sqldb, err := db.Open(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	store := db.NewStore(sqldb)
	teamID, repoIDs, err := store.MirrorTeam(team)
	if err != nil {
		t.Fatal(err)
	}
	var targets []syncer.Target
	for _, r := range team.Repos {
		targets = append(targets, syncer.Target{Owner: r.Owner, Name: r.Name, RepoID: repoIDs[r.String()]})
	}

	deps := Deps{
		DB: sqldb, Store: store, Cfg: cfg, Team: team, TeamID: teamID,
		Targets: targets,
		Doer:    emptyPageDoer{}, // no network: render whatever is cached
	}

	tm := teatest.NewTestModel(t, New(deps), teatest.WithInitialTermSize(110, 24))
	teatest.WaitFor(t, tm.Output(), func(b []byte) bool {
		return bytes.Contains(b, []byte("Statline"))
	}, teatest.WithDuration(10*time.Second))
	time.Sleep(500 * time.Millisecond) // let data + sync-complete messages land
	tm.Send(tea.KeyPressMsg{Code: 'q', Text: "q"})

	final, ok := tm.FinalModel(t, teatest.WithFinalTimeout(5*time.Second)).(Model)
	if !ok {
		t.Fatal("unexpected final model type")
	}
	fmt.Println("────────────────────────── rendered view ──────────────────────────")
	fmt.Println(stripANSI(final.View().Content))
	fmt.Println("────────────────────────────────────────────────────────────────────")
}

var ansiRE = regexp.MustCompile(`\x1b\[[0-9;?]*[a-zA-Z]|\x1b\][^\x07\x1b]*(\x07|\x1b\\)`)

func stripANSI(s string) string { return ansiRE.ReplaceAllString(s, "") }
