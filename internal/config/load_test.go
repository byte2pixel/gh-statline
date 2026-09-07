package config

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadSaveRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)

	if _, err := Load(); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Load with no file = %v, want ErrNotFound", err)
	}

	want := Default()
	want.DefaultTeam = "platform"
	want.Teams = []Team{{
		Name: "platform", Org: "acme", GHTeamSlug: "platform-eng",
		Members: []Member{{Login: "alice"}, {Login: "bob", Hidden: true}},
		Repos:   []Repo{{Owner: "acme", Name: "api"}},
		NoSync:  true,
	}}
	if err := Save(want); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.DefaultTeam != "platform" || len(got.Teams) != 1 {
		t.Fatalf("round trip lost data: %+v", got)
	}
	tm := got.Teams[0]
	if tm.GHTeamSlug != "platform-eng" || !tm.NoSync ||
		len(tm.Members) != 2 || !tm.Members[1].Hidden ||
		tm.Repos[0].String() != "acme/api" {
		t.Errorf("team fields lost: %+v", tm)
	}

	// An unset ui.theme must stay out of the file. Every in-app change
	// rewrites the whole config, and writing `theme: ""` back into
	// everyone's file would be noise.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "theme:") {
		t.Errorf("unset ui.theme was written to disk:\n%s", raw)
	}

	// No leftover temp file from the atomic write.
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file left behind: %v", err)
	}
}

func TestLoadRejectsInvalidYAML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)
	if err := os.WriteFile(path, []byte("teams: [\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("invalid YAML accepted")
	}
}

func TestLoadRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)
	// Parses fine but fails Validate: default_team without teams.
	if err := os.WriteFile(path, []byte("default_team: ghostteam\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(); err == nil {
		t.Error("structurally invalid config accepted")
	}
}

// A hand-added ui.theme has to survive the round trip. Every in-app change
// (team switch, window, sort) rewrites the whole file, and dropping the key
// would un-pin the palette with nothing to show for it (#54).
func TestSaveKeepsConfiguredTheme(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)

	cfg := Default()
	cfg.DefaultTeam = "platform"
	cfg.Teams = []Team{{Name: "platform", Org: "acme"}}
	cfg.UI.Theme = "light"
	if err := Save(cfg); err != nil {
		t.Fatal(err)
	}

	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if got.UI.Theme != "light" {
		t.Fatalf("ui.theme = %q after a round trip, want light", got.UI.Theme)
	}
	if dark, forced := got.UI.ThemeMode(); dark || !forced {
		t.Errorf("ThemeMode = (dark %v, forced %v), want (false, true)", dark, forced)
	}
}

// validCfg is the smallest config that passes Validate.
func validCfg() Config {
	c := Default()
	c.DefaultTeam = "platform"
	c.Teams = []Team{{Name: "platform", Org: "acme", Members: []Member{{Login: "alice"}}}}
	return c
}

// Load validates, so a Save that skips the check trades a reported failure
// for a hard startup failure on the next run. Nothing may reach disk.
func TestSaveRejectsInvalidConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)

	cases := []struct {
		name string
		cfg  func(Config) Config
	}{
		{"no teams", func(c Config) Config { c.Teams = nil; return c }},
		{"empty team name", func(c Config) Config { c.Teams[0].Name = ""; return c }},
		{"empty member login", func(c Config) Config {
			c.Teams[0].Members = []Member{{Login: ""}}
			return c
		}},
		{"repo missing a name", func(c Config) Config {
			c.Teams[0].Repos = []Repo{{Owner: "acme"}}
			return c
		}},
		// The in-app case from the issue: a team is removed and default_team
		// is left pointing at it.
		{"dangling default_team", func(c Config) Config { c.DefaultTeam = "ghostteam"; return c }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := Save(tc.cfg(validCfg())); err == nil {
				t.Fatal("Save wrote an invalid config")
			}
			if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("a file was created anyway: %v", err)
			}
			if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
				t.Errorf("temp file left behind: %v", err)
			}
		})
	}
}

// A refused Save must not damage the config already on disk: the session
// keeps running on its in-memory value, and the next start should still find
// the last good file.
func TestSaveRefusalLeavesTheExistingFileIntact(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)

	if err := Save(validCfg()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	broken := validCfg()
	broken.Teams = nil
	if err := Save(broken); err == nil {
		t.Fatal("Save wrote an invalid config over a good one")
	}

	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Errorf("the good config was modified:\nbefore:\n%s\nafter:\n%s", before, after)
	}
	if _, err := Load(); err != nil {
		t.Errorf("the file on disk no longer loads: %v", err)
	}
}

// Every in-app change rewrites the whole file. Writing a shorter config over
// a longer one must replace it, not overwrite its first n bytes and leave
// the old tail parsing as extra teams.
func TestSaveReplacesRatherThanOverwritesInPlace(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yml")
	t.Setenv("STATLINE_CONFIG", path)

	big := validCfg()
	for _, name := range []string{"infra", "mobile", "data-platform-and-analytics"} {
		big.Teams = append(big.Teams, Team{Name: name, Org: "acme",
			Members: []Member{{Login: "bob"}, {Login: "carol"}},
			Repos:   []Repo{{Owner: "acme", Name: name}}})
	}
	if err := Save(big); err != nil {
		t.Fatal(err)
	}
	if err := Save(validCfg()); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, gone := range []string{"infra", "mobile", "data-platform-and-analytics"} {
		if strings.Contains(string(raw), gone) {
			t.Errorf("%q survived the rewrite:\n%s", gone, raw)
		}
	}
	got, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Teams) != 1 {
		t.Errorf("got %d teams after shrinking, want 1", len(got.Teams))
	}
	if _, err := os.Stat(path + ".tmp"); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("temp file left behind: %v", err)
	}
}
