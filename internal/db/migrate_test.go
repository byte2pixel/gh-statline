package db

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"testing/fstest"
)

// bareDB opens an in-memory database with no migrations applied, so each
// test decides what schema version it starts from.
func bareDB(t *testing.T) *sql.DB {
	t.Helper()
	sqldb, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqldb.SetMaxOpenConns(1)
	t.Cleanup(func() { sqldb.Close() })
	return sqldb
}

func userVersion(t *testing.T, sqldb *sql.DB) int {
	t.Helper()
	var v int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&v); err != nil {
		t.Fatal(err)
	}
	return v
}

func setUserVersion(t *testing.T, sqldb *sql.DB, v int) {
	t.Helper()
	if _, err := sqldb.Exec(fmt.Sprintf("PRAGMA user_version = %d", v)); err != nil {
		t.Fatal(err)
	}
}

func tableExists(t *testing.T, sqldb *sql.DB, name string) bool {
	t.Helper()
	var n int
	err := sqldb.QueryRow(
		`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func migrations(files map[string]string) fstest.MapFS {
	fsys := fstest.MapFS{}
	for name, body := range files {
		fsys["migrations/"+name] = &fstest.MapFile{Data: []byte(body)}
	}
	return fsys
}

// Each migration is applied in filename order and user_version ends at the
// newest one — 0003 indexes a column 0002 added to a table 0001 created, so
// any other order fails outright.
func TestMigrateAppliesInOrder(t *testing.T) {
	sqldb := bareDB(t)
	fsys := migrations(map[string]string{
		"0001_init.sql":  `CREATE TABLE t (id INTEGER PRIMARY KEY);`,
		"0002_col.sql":   `ALTER TABLE t ADD COLUMN name TEXT;`,
		"0003_index.sql": `CREATE INDEX t_name ON t (name);`,
	})

	if err := migrate(sqldb, fsys); err != nil {
		t.Fatal(err)
	}
	if got := userVersion(t, sqldb); got != 3 {
		t.Errorf("user_version = %d, want 3", got)
	}
	if _, err := sqldb.Exec(`INSERT INTO t (id, name) VALUES (1, 'a')`); err != nil {
		t.Errorf("schema is not the fully migrated one: %v", err)
	}
}

// Re-running the same set is a no-op: every version is already applied, so
// nothing re-executes and the stamp does not move.
func TestMigrateIsIdempotent(t *testing.T) {
	sqldb := bareDB(t)
	fsys := migrations(map[string]string{"0001_init.sql": `CREATE TABLE t (id INTEGER);`})

	if err := migrate(sqldb, fsys); err != nil {
		t.Fatal(err)
	}
	// A second CREATE TABLE would fail, so success here proves the skip.
	if err := migrate(sqldb, fsys); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	if got := userVersion(t, sqldb); got != 1 {
		t.Errorf("user_version = %d, want 1", got)
	}
}

// A version at or below the stamp is skipped without being read, so a cache
// migrated by an older build never replays history it already has.
func TestMigrateSkipsAppliedVersions(t *testing.T) {
	sqldb := bareDB(t)
	setUserVersion(t, sqldb, 1)
	fsys := migrations(map[string]string{
		"0001_init.sql": `THIS IS NOT SQL AND MUST NEVER RUN;`,
		"0002_next.sql": `CREATE TABLE t (id INTEGER);`,
	})

	if err := migrate(sqldb, fsys); err != nil {
		t.Fatalf("migrate replayed an applied version: %v", err)
	}
	if got := userVersion(t, sqldb); got != 2 {
		t.Errorf("user_version = %d, want 2", got)
	}
}

// One migration per transaction: a failure rolls back everything that file
// did, leaves the stamp on the last good version, and keeps earlier
// migrations committed so a fixed re-run can resume from there.
func TestMigrateRollsBackTheFailingMigration(t *testing.T) {
	sqldb := bareDB(t)
	fsys := migrations(map[string]string{
		"0001_init.sql": `CREATE TABLE good (id INTEGER);`,
		"0002_bad.sql":  `CREATE TABLE half (id INTEGER); THIS IS NOT SQL;`,
	})

	err := migrate(sqldb, fsys)
	if err == nil {
		t.Fatal("migrate accepted a broken migration")
	}
	if !strings.Contains(err.Error(), "0002_bad.sql") {
		t.Errorf("err = %v, want it to name the failing file", err)
	}
	if got := userVersion(t, sqldb); got != 1 {
		t.Errorf("user_version = %d, want 1 — the stamp must not move past a failure", got)
	}
	if !tableExists(t, sqldb, "good") {
		t.Error("0001 was rolled back too; each migration commits on its own")
	}
	if tableExists(t, sqldb, "half") {
		t.Error("the failing migration left a table behind; it must roll back whole")
	}
}

// A cache written by a newer build has every migration this one knows, so
// the loop does nothing and the open used to "succeed" — then fail later
// with scattered "no such column". Refuse it up front, and say what to do.
func TestMigrateRefusesANewerSchema(t *testing.T) {
	sqldb := bareDB(t)
	setUserVersion(t, sqldb, 7)
	fsys := migrations(map[string]string{
		"0001_init.sql": `CREATE TABLE t (id INTEGER);`,
		"0002_col.sql":  `ALTER TABLE t ADD COLUMN name TEXT;`,
	})

	err := migrate(sqldb, fsys)
	if err == nil {
		t.Fatal("a cache from a newer build was accepted")
	}
	for _, want := range []string{"7", "2", "newer version of statline", "delete"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %v, want it to mention %q", err, want)
		}
	}
	if tableExists(t, sqldb, "t") {
		t.Error("migrations ran despite the downgrade guard")
	}
	if got := userVersion(t, sqldb); got != 7 {
		t.Errorf("user_version = %d, want it left at 7", got)
	}
}

// The version is parsed from every filename before any of them runs, so a
// misnamed file is caught with the database still untouched rather than
// halfway through the set.
func TestMigrateRejectsAMisnamedFileBeforeApplyingAny(t *testing.T) {
	sqldb := bareDB(t)
	fsys := migrations(map[string]string{
		"0001_init.sql": `CREATE TABLE t (id INTEGER);`,
		"later_add.sql": `CREATE TABLE u (id INTEGER);`,
	})

	err := migrate(sqldb, fsys)
	if err == nil {
		t.Fatal("a migration with no version prefix was accepted")
	}
	if !strings.Contains(err.Error(), "later_add.sql") {
		t.Errorf("err = %v, want it to name the offending file", err)
	}
	if tableExists(t, sqldb, "t") {
		t.Error("0001 was applied before the name check; nothing may run")
	}
}

// Open reports the newer-cache refusal with the path, since deleting that
// file is the fix the message tells the user to apply.
func TestOpenReportsANewerCacheWithItsPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "statline.db")
	sqldb, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	setUserVersion(t, sqldb, 999)
	if err := sqldb.Close(); err != nil {
		t.Fatal(err)
	}

	if _, err := Open(path); err == nil {
		t.Fatal("reopening a cache from a newer build succeeded")
	} else {
		if !strings.Contains(err.Error(), path) {
			t.Errorf("err = %v, want the cache path in it", err)
		}
		if !strings.Contains(err.Error(), "newer version of statline") {
			t.Errorf("err = %v, want the newer-build explanation", err)
		}
	}
}

// The embedded set is what ships. An unparseable prefix breaks every open,
// and a duplicate version silently skips the second file forever, since the
// stamp the first one leaves already covers it.
func TestEmbeddedMigrationsHaveUniqueVersions(t *testing.T) {
	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) == 0 {
		t.Fatal("no embedded migrations")
	}
	seen := map[int]string{}
	for _, e := range entries {
		name := e.Name()
		v, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			t.Errorf("%s: name must start with a number", name)
			continue
		}
		if prev, dup := seen[v]; dup {
			t.Errorf("%s and %s share version %d; the second would never apply", prev, name, v)
		}
		seen[v] = name
	}
}
