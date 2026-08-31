package db

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/byte2pixel/gh-statline/internal/config"
)

func assertOwnerOnly(t *testing.T, path string) {
	t.Helper()
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if perm := fi.Mode().Perm(); perm&0o077 != 0 {
		t.Errorf("%s mode = %#o, want no group or other access", filepath.Base(path), perm)
	}
}

// The cache holds pull request titles from private repositories, so neither
// it nor its WAL sidecars may be left at the process umask (gh issue #33).
func TestOpenRestrictsCachePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on Windows")
	}
	dir := filepath.Join(t.TempDir(), "cache")
	path := filepath.Join(dir, "statline.db")

	sqldb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	// A write forces the sidecars into existence.
	if _, _, err := NewStore(sqldb).MirrorTeam(config.Team{
		Name:    "testers",
		Members: []config.Member{{Login: "alice"}},
		Repos:   []config.Repo{{Owner: "acme", Name: "api"}},
	}); err != nil {
		t.Fatal(err)
	}

	assertOwnerOnly(t, dir)
	assertOwnerOnly(t, path)
	for _, sidecar := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(sidecar); os.IsNotExist(err) {
			continue
		}
		assertOwnerOnly(t, sidecar)
	}
}

// Caches written before the mode was enforced must be tightened on open,
// not left readable for the rest of their life.
func TestOpenTightensExistingCache(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix permission bits are not meaningful on Windows")
	}
	path := filepath.Join(t.TempDir(), "statline.db")
	if err := os.WriteFile(path, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// Chmod rather than a WriteFile mode: umask must not decide the premise.
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatal(err)
	}

	sqldb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer sqldb.Close()

	assertOwnerOnly(t, path)
}
