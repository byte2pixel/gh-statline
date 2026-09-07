package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies the migrations in fsys that are newer than PRAGMA
// user_version, in filename order. Files live under migrations/ and are named
// NNNN_description.sql, where NNNN is the version. Open passes the embedded
// set; the parameter exists so tests can supply their own.
func migrate(sqldb *sql.DB, fsys fs.FS) error {
	var current int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}

	entries, err := fs.ReadDir(fsys, "migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	// Parse every version before applying anything: the highest is the schema
	// this build knows, and a misnamed file should fail before a migration
	// has been half-applied rather than after.
	versions := make([]int, len(names))
	newest := 0
	for i, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", name)
		}
		versions[i] = version
		if version > newest {
			newest = version
		}
	}

	// A cache written by a newer build has columns this one has never heard
	// of, and nothing here would notice: every migration is already applied,
	// so the loop below does nothing and the open "succeeds". The failure
	// lands later as scattered "no such column" errors, far from the cause.
	// Reachable by rolling an extension upgrade back, or by syncing a cache
	// directory between machines running different versions.
	if current > newest {
		return fmt.Errorf("cache is at schema version %d but this build only knows %d: "+
			"it was written by a newer version of statline — delete the cache or upgrade", current, newest)
	}

	for i, name := range names {
		version := versions[i]
		if version <= current {
			continue
		}
		ddl, err := fs.ReadFile(fsys, "migrations/"+name)
		if err != nil {
			return err
		}
		tx, err := sqldb.Begin()
		if err != nil {
			return err
		}
		if _, err := tx.Exec(string(ddl)); err != nil {
			tx.Rollback()
			return fmt.Errorf("applying %s: %w", name, err)
		}
		// PRAGMA user_version cannot be parameterized; version is a parsed int.
		if _, err := tx.Exec(fmt.Sprintf("PRAGMA user_version = %d", version)); err != nil {
			tx.Rollback()
			return fmt.Errorf("bumping user_version for %s: %w", name, err)
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		current = version
	}
	return nil
}
