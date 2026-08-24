package config

import (
	"errors"
	"os"
	"path/filepath"
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
