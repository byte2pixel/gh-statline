// Package db owns the local SQLite cache: schema migrations and all reads
// and writes. The database is a rebuildable cache of GitHub data — deleting
// it costs a re-sync, nothing more.
package db

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite" // pure-Go driver, registers as "sqlite"
)

// Open opens (creating if needed) the cache at path and migrates it.
// Use ":memory:" for tests.
//
// The pool is capped at one connection: per-connection pragmas then apply
// everywhere, plain Windows paths work as DSNs without URI escaping, and a
// single writer removes SQLITE_BUSY from the picture. Statement volume here
// is far below the point where that matters. Callers must fully drain or
// close sql.Rows before issuing another query.
func Open(path string) (*sql.DB, error) {
	if path != ":memory:" {
		if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
			return nil, err
		}
		if err := restrictToOwner(path); err != nil {
			return nil, fmt.Errorf("securing %s: %w", path, err)
		}
	}
	sqldb, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	sqldb.SetMaxOpenConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode = WAL",
		"PRAGMA busy_timeout = 5000",
		"PRAGMA foreign_keys = ON",
		"PRAGMA synchronous = NORMAL",
	} {
		if _, err := sqldb.Exec(pragma); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("%s: %w", pragma, err)
		}
	}
	if err := migrate(sqldb); err != nil {
		sqldb.Close()
		return nil, fmt.Errorf("migrating %s: %w", path, err)
	}
	if path != ":memory:" {
		// The sidecars only exist once WAL mode is on, and their mode comes
		// from the driver rather than from us.
		if err := ownerOnly(path); err != nil {
			sqldb.Close()
			return nil, fmt.Errorf("securing %s: %w", path, err)
		}
	}
	return sqldb, nil
}

// restrictToOwner creates the cache before SQLite can, so it never inherits a
// world-readable umask: the database holds pull request titles from private
// repositories. The chmod also tightens caches left by earlier versions.
func restrictToOwner(path string) error {
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE, 0o600)
	if err != nil {
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return ownerOnly(path)
}

// ownerOnly restricts the database and its WAL sidecars, skipping whichever
// SQLite has not created yet.
func ownerOnly(path string) error {
	for _, p := range []string{path, path + "-wal", path + "-shm"} {
		if err := os.Chmod(p, 0o600); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}
