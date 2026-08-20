package store

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
		Name:    "create initial schema and add user settings preferences",
		Up: func(tx *sql.Tx) error {
			// This is the original schema baseline. CREATE IF NOT EXISTS also
			// makes it safe for databases created before schema versions were
			// recorded; the column additions below then bring version 1 current.
			if _, err := tx.Exec(`
				CREATE TABLE IF NOT EXISTS users (
					id            INTEGER PRIMARY KEY AUTOINCREMENT,
					email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
					name          TEXT NOT NULL,
					password_hash TEXT NOT NULL,
					is_admin      INTEGER NOT NULL DEFAULT 0,
					created_at    TEXT NOT NULL DEFAULT (datetime('now'))
				);

				CREATE TABLE IF NOT EXISTS sessions (
					token      TEXT PRIMARY KEY,
					user_id    INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					expires_at TEXT NOT NULL
				);

				CREATE TABLE IF NOT EXISTS invites (
					token      TEXT PRIMARY KEY,
					created_by INTEGER NOT NULL REFERENCES users(id),
					created_at TEXT NOT NULL DEFAULT (datetime('now')),
					used_by    INTEGER REFERENCES users(id),
					used_at    TEXT
				);

				CREATE TABLE IF NOT EXISTS user_settings (
					user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
					weekdays     TEXT NOT NULL DEFAULT '[0,1,2,3,4]',
					time_start   TEXT NOT NULL DEFAULT '19:00',
					time_end     TEXT NOT NULL DEFAULT '21:00',
					days_ahead   INTEGER NOT NULL DEFAULT 10,
					min_duration INTEGER NOT NULL DEFAULT 60
				);

				CREATE TABLE IF NOT EXISTS slots (
					id               INTEGER PRIMARY KEY AUTOINCREMENT,
					source           TEXT NOT NULL,
					location         TEXT NOT NULL,
					court            TEXT NOT NULL,
					date             TEXT NOT NULL,
					time             TEXT NOT NULL,
					duration_minutes INTEGER NOT NULL,
					price            REAL NOT NULL DEFAULT 0,
					currency         TEXT NOT NULL DEFAULT 'EUR'
				);
				CREATE INDEX IF NOT EXISTS idx_slots_date ON slots(date);

				CREATE TABLE IF NOT EXISTS meta (
					key   TEXT PRIMARY KEY,
					value TEXT NOT NULL
				);

				CREATE TABLE IF NOT EXISTS polls (
					id              INTEGER PRIMARY KEY AUTOINCREMENT,
					creator_id      INTEGER NOT NULL REFERENCES users(id),
					title           TEXT NOT NULL,
					status          TEXT NOT NULL DEFAULT 'active',
					winning_slot_id INTEGER REFERENCES poll_slots(id),
					created_at      TEXT NOT NULL DEFAULT (datetime('now')),
					closed_at       TEXT
				);

				CREATE TABLE IF NOT EXISTS poll_slots (
					id               INTEGER PRIMARY KEY AUTOINCREMENT,
					poll_id          INTEGER NOT NULL REFERENCES polls(id) ON DELETE CASCADE,
					date             TEXT NOT NULL,
					time             TEXT NOT NULL,
					duration_minutes INTEGER NOT NULL,
					location         TEXT NOT NULL,
					court            TEXT NOT NULL DEFAULT '',
					price            REAL NOT NULL DEFAULT 0,
					currency         TEXT NOT NULL DEFAULT 'EUR'
				);
				CREATE INDEX IF NOT EXISTS idx_poll_slots_poll ON poll_slots(poll_id);

				CREATE TABLE IF NOT EXISTS votes (
					poll_slot_id INTEGER NOT NULL REFERENCES poll_slots(id) ON DELETE CASCADE,
					user_id      INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
					vote         INTEGER NOT NULL,
					updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
					PRIMARY KEY (poll_slot_id, user_id)
				);

				CREATE TABLE IF NOT EXISTS sync_log (
					id         INTEGER PRIMARY KEY AUTOINCREMENT,
					entity     TEXT NOT NULL,
					entity_id  TEXT NOT NULL,
					action     TEXT NOT NULL,
					payload    TEXT,
					visible_to INTEGER,
					created_at TEXT NOT NULL DEFAULT (datetime('now'))
				);
			`); err != nil {
				return fmt.Errorf("create initial schema: %w", err)
			}
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
	{
		Version: 4,
		Name:    "repair literal CURRENT_TIMESTAMP values",
		Up: func(tx *sql.Tx) error {
			// These columns are typed string, so GORM inlined the old
			// default:CURRENT_TIMESTAMP tag as literal text instead of deferring to
			// SQLite's column default. The original times are unrecoverable; "now"
			// restores ordering and lets stranded sync_log rows expire.
			repairs := []struct{ table, column string }{
				{"users", "created_at"},
				{"invites", "created_at"},
				{"polls", "created_at"},
				{"sync_log", "created_at"},
				{"votes", "updated_at"},
			}
			for _, repair := range repairs {
				statement := fmt.Sprintf(
					`UPDATE %s SET %s = datetime('now') WHERE %s = 'CURRENT_TIMESTAMP'`,
					repair.table, repair.column, repair.column,
				)
				if _, err := tx.Exec(statement); err != nil {
					return fmt.Errorf("repair %s.%s: %w", repair.table, repair.column, err)
				}
			}
			return nil
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
