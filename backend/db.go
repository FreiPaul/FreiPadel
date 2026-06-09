package main

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

const schema = `
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
	used_by    INTEGER REFERENCES users(id), -- single invites: who redeemed it
	used_at    TEXT,
	kind       TEXT NOT NULL DEFAULT 'single', -- 'single' (one-time) | 'group' (reusable)
	disabled   INTEGER NOT NULL DEFAULT 0,
	uses       INTEGER NOT NULL DEFAULT 0
);

CREATE TABLE IF NOT EXISTS user_settings (
	user_id      INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
	weekdays     TEXT NOT NULL DEFAULT '[0,1,2,3,4]',
	time_start   TEXT NOT NULL DEFAULT '19:00',
	time_end     TEXT NOT NULL DEFAULT '21:00',
	days_ahead   INTEGER NOT NULL DEFAULT 10,
	min_duration INTEGER NOT NULL DEFAULT 60,
	locations    TEXT NOT NULL DEFAULT '[]' -- JSON array of location names; empty = all
);

CREATE TABLE IF NOT EXISTS slots (
	id               INTEGER PRIMARY KEY AUTOINCREMENT,
	source           TEXT NOT NULL,
	location         TEXT NOT NULL,
	court            TEXT NOT NULL,
	date             TEXT NOT NULL, -- YYYY-MM-DD (local, Europe/Berlin)
	time             TEXT NOT NULL, -- HH:MM (local)
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
	status          TEXT NOT NULL DEFAULT 'active', -- active | closed
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
	vote         INTEGER NOT NULL, -- 1 = yes, 0 = no
	updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
	PRIMARY KEY (poll_slot_id, user_id)
);
`

func openDB(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// modernc/sqlite works best with a single writer connection.
	db.SetMaxOpenConns(1)
	if _, err := db.Exec(schema); err != nil {
		return nil, fmt.Errorf("apply schema: %w", err)
	}
	// Migrations for databases created before these columns existed;
	// the duplicate-column errors on fresh databases are expected.
	_, _ = db.Exec(`ALTER TABLE user_settings ADD COLUMN locations TEXT NOT NULL DEFAULT '[]'`)
	_, _ = db.Exec(`ALTER TABLE invites ADD COLUMN kind TEXT NOT NULL DEFAULT 'single'`)
	_, _ = db.Exec(`ALTER TABLE invites ADD COLUMN disabled INTEGER NOT NULL DEFAULT 0`)
	_, _ = db.Exec(`ALTER TABLE invites ADD COLUMN uses INTEGER NOT NULL DEFAULT 0`)
	// Backfill uses for invites redeemed before the counter existed.
	_, _ = db.Exec(`UPDATE invites SET uses = 1 WHERE used_by IS NOT NULL AND uses = 0`)

	if err := migrateHashSessions(db); err != nil {
		return nil, fmt.Errorf("hash existing sessions: %w", err)
	}
	return db, nil
}

// migrateHashSessions rewrites plaintext session tokens to their hashes once, so
// the database no longer holds directly usable tokens. Existing sessions keep
// working: the browser still sends the raw token, which we now hash before
// lookup. Guarded by a meta flag because a hashed token is indistinguishable
// from a raw one by length/format, so re-running would double-hash and log
// everyone out.
func migrateHashSessions(db *sql.DB) error {
	if getMeta(db, "sessions_hashed") == "1" {
		return nil
	}
	rows, err := db.Query(`SELECT token FROM sessions`)
	if err != nil {
		return err
	}
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, t := range tokens {
		if _, err := db.Exec(`UPDATE sessions SET token = ? WHERE token = ?`, hashToken(t), t); err != nil {
			return err
		}
	}
	return setMeta(db, "sessions_hashed", "1")
}

func getMeta(db *sql.DB, key string) string {
	var v string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	return v
}

func setMeta(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}
