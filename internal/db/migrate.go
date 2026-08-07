package db

import (
	"database/sql"
	"embed"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// migrate applies embedded migrations newer than PRAGMA user_version, in
// filename order. Files are named NNNN_description.sql; NNNN is the version.
func migrate(sqldb *sql.DB) error {
	var current int
	if err := sqldb.QueryRow("PRAGMA user_version").Scan(&current); err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)

	for _, name := range names {
		version, err := strconv.Atoi(strings.SplitN(name, "_", 2)[0])
		if err != nil {
			return fmt.Errorf("migration %s: name must start with a number", name)
		}
		if version <= current {
			continue
		}
		ddl, err := migrationFS.ReadFile("migrations/" + name)
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
