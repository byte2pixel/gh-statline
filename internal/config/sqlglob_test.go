package config_test

import (
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
	"github.com/byte2pixel/gh-statline/internal/db"
)

// SQLGlobs translates the exclude_bots globs into SQLite GLOB patterns for
// the bot_actors view. The view and BotMatcher must agree on every login,
// or a bot excluded from one number would appear in another. This pins the
// translation against the Go matcher over a corpus of real-shaped logins:
// GitHub logins are ASCII, so SQLite's ASCII lower() folds exactly what
// Go's (?i) does; brackets are literal on both sides.
func TestSQLGlobsMatchIsBot(t *testing.T) {
	sqldb, err := db.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	patternSets := [][]string{
		config.Default().ExcludeBots,
		{"*[bot]"},
		{"dependabot*", "renovate*"},
		{"exact-name"},
		{"c?-bot"},
		{"[weird]*"},
		{"*"},
		{"?"},
		{},
	}
	logins := []string{
		"dependabot[bot]", "Dependabot", "dependabot-preview[bot]", "dependabo", "botdependabot",
		"renovate[bot]", "renovate-gtw", "Renovate-Approve", "copilot", "copilot-swe-agent[bot]",
		"COPILOT-pull-request-reviewer[bot]", "github-actions[bot]", "ci-robot[bot]", "CI-Robot[BOT]",
		"cibot", "bot", "[bot]", "a[bot]b", "x[bot", "bot]", "[bot]x",
		"exact-name", "Exact-Name", "exact-name2", "exact-nam",
		"c1-bot", "cX-bot", "c-bot", "c12-bot", "C?-bot",
		"[weird]", "[weird]one", "weird", "w",
		"alice", "Bob", "o-brien", "x_y", "a", "", "mireille", "sam-contractor",
	}

	for _, globs := range patternSets {
		m := config.NewBotMatcher(globs)
		sqlGlobs := config.SQLGlobs(globs)
		if len(sqlGlobs) != len(globs) {
			t.Fatalf("SQLGlobs(%q) = %q: one pattern per glob", globs, sqlGlobs)
		}
		for _, login := range logins {
			got := false
			for _, p := range sqlGlobs {
				var hit int
				if err := sqldb.QueryRow(`SELECT lower(?) GLOB ?`, login, p).Scan(&hit); err != nil {
					t.Fatal(err)
				}
				if hit == 1 {
					got = true
					break
				}
			}
			if want := m.IsBot(login); got != want {
				t.Errorf("globs %q, login %q: SQL says %v, IsBot says %v (sql patterns %q)",
					globs, login, got, want, sqlGlobs)
			}
		}
	}
}
