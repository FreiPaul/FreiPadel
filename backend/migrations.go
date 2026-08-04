package main

import (
	"database/sql"
	"errors"
	"fmt"
	"strconv"
)

const schemaVersionKey = "schema_version"

type schemaMigration struct {
	Version int
	Name    string
	Up      func(*sql.Tx) error
}

var schemaMigrations = []schemaMigration{
	{
		Version: 1,
		Name:    "add user settings preferences",
		Up: func(tx *sql.Tx) error {
			if err := addColumnIfMissing(tx, "user_settings", "locations",
				`ALTER TABLE user_settings ADD COLUMN locations TEXT NOT NULL DEFAULT '[]'`); err != nil {
				return err
			}
			return addColumnIfMissing(tx, "user_settings", "notifications",
				`ALTER TABLE user_settings ADD COLUMN notifications TEXT NOT NULL DEFAULT '{}'`)
		},
	},
	{
		Version: 2,
		Name:    "add invite lifecycle fields",
		Up: func(tx *sql.Tx) error {
			columns := []struct {
				name string
				sql  string
			}{
				{"kind", `ALTER TABLE invites ADD COLUMN kind TEXT NOT NULL DEFAULT 'single'`},
				{"disabled", `ALTER TABLE invites ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`},
				{"uses", `ALTER TABLE invites ADD COLUMN uses INTEGER NOT NULL DEFAULT 0`},
			}
			for _, column := range columns {
				if err := addColumnIfMissing(tx, "invites", column.name, column.sql); err != nil {
					return err
				}
			}
			// Backfill invites redeemed before the usage counter existed.
			_, err := tx.Exec(`UPDATE invites SET uses = 1 WHERE used_by IS NOT NULL AND uses = 0`)
			return err
		},
	},
	{
		Version: 3,
		Name:    "add invite email",
		Up: func(tx *sql.Tx) error {
			return addColumnIfMissing(tx, "invites", "email",
				`ALTER TABLE invites ADD COLUMN email TEXT`)
		},
	},
}

var latestSchemaVersion = schemaMigrations[len(schemaMigrations)-1].Version

func addColumnIfMissing(tx *sql.Tx, table, column, statement string) error {
	var exists int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM pragma_table_info(?) WHERE name = ?`, table, column).
		Scan(&exists); err != nil {
		return fmt.Errorf("inspect %s.%s: %w", table, column, err)
	}
	if exists != 0 {
		return nil
	}
	if _, err := tx.Exec(statement); err != nil {
		return fmt.Errorf("add %s.%s: %w", table, column, err)
	}
	return nil
}

func applySchemaMigrations(db *sql.DB, migrations []schemaMigration) error {
	latest, err := validateSchemaMigrations(migrations)
	if err != nil {
		return err
	}
	current, err := readSchemaVersion(db)
	if err != nil {
		return err
	}
	if current > latest {
		return fmt.Errorf("database schema version %d is newer than supported version %d", current, latest)
	}

	for _, migration := range migrations {
		if migration.Version <= current {
			continue
		}
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("begin schema migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if err := migration.Up(tx); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("apply schema migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
			ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
			schemaVersionKey, strconv.Itoa(migration.Version)); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("record schema migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		if err := tx.Commit(); err != nil {
			return fmt.Errorf("commit schema migration %d (%s): %w", migration.Version, migration.Name, err)
		}
		current = migration.Version
	}
	return nil
}

func validateSchemaMigrations(migrations []schemaMigration) (int, error) {
	for i, migration := range migrations {
		want := i + 1
		if migration.Version != want {
			return 0, fmt.Errorf("schema migration %q has version %d, want %d", migration.Name, migration.Version, want)
		}
		if migration.Name == "" || migration.Up == nil {
			return 0, fmt.Errorf("schema migration %d is incomplete", migration.Version)
		}
	}
	if len(migrations) == 0 {
		return 0, nil
	}
	return migrations[len(migrations)-1].Version, nil
}

func readSchemaVersion(db *sql.DB) (int, error) {
	var value string
	err := db.QueryRow(`SELECT value FROM meta WHERE key = ?`, schemaVersionKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, fmt.Errorf("read schema version: %w", err)
	}
	version, err := strconv.Atoi(value)
	if err != nil || version < 0 {
		return 0, fmt.Errorf("invalid schema version %q", value)
	}
	return version, nil
}
