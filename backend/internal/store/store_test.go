package store

import (
	"database/sql"
	"errors"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"freipadel/internal/sessiontoken"
	"gorm.io/gorm"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	database, err := Open(filepath.Join(t.TempDir(), "freipadel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	db := database.SQL
	t.Cleanup(func() {
		if err := database.Close(); err != nil {
			t.Errorf("close database: %v", err)
		}
	})
	return db
}

func scalarInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("query scalar %q: %v", query, err)
	}
	return got
}

func TestOpenDBCreatesCurrentSchemaAndPragmas(t *testing.T) {
	db := openTestDB(t)

	for _, table := range []string{
		"users", "sessions", "invites", "user_settings", "slots",
		"meta", "polls", "poll_slots", "votes", "sync_log",
	} {
		if got := scalarInt(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); got != 1 {
			t.Errorf("table %q count = %d, want 1", table, got)
		}
	}
	for _, index := range []string{"idx_slots_date", "idx_poll_slots_poll"} {
		if got := scalarInt(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?`, index); got != 1 {
			t.Errorf("index %q count = %d, want 1", index, got)
		}
	}

	if got := scalarInt(t, db, `PRAGMA foreign_keys`); got != 1 {
		t.Errorf("foreign_keys = %d, want 1", got)
	}
	if got := scalarInt(t, db, `PRAGMA busy_timeout`); got != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", got)
	}
	var journalMode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}
	if got := db.Stats().MaxOpenConnections; got != 1 {
		t.Errorf("MaxOpenConnections = %d, want 1", got)
	}
	if got := getMeta(db, schemaVersionKey); got != strconv.Itoa(latestSchemaVersion) {
		t.Errorf("schema version = %q, want %d", got, latestSchemaVersion)
	}
}

func TestFirstMigrationCreatesInitialSchema(t *testing.T) {
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "migration.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if _, err := db.Exec(`CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL)`); err != nil {
		t.Fatalf("bootstrap migration metadata: %v", err)
	}

	if err := applySchemaMigrations(db, schemaMigrations[:1]); err != nil {
		t.Fatalf("apply first migration: %v", err)
	}
	for _, table := range []string{
		"users", "sessions", "invites", "user_settings", "slots",
		"meta", "polls", "poll_slots", "votes", "sync_log",
	} {
		if got := scalarInt(t, db,
			`SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = ?`, table); got != 1 {
			t.Errorf("table %q count after first migration = %d, want 1", table, got)
		}
	}
	if got := getMeta(db, schemaVersionKey); got != "1" {
		t.Errorf("schema version after first migration = %q, want 1", got)
	}
	if got := scalarInt(t, db,
		`SELECT COUNT(*) FROM pragma_table_info('user_settings') WHERE name = 'locations'`); got != 1 {
		t.Errorf("locations column count after first migration = %d, want 1", got)
	}
	if got := scalarInt(t, db,
		`SELECT COUNT(*) FROM pragma_table_info('invites') WHERE name = 'kind'`); got != 0 {
		t.Errorf("invite kind column count before second migration = %d, want 0", got)
	}
}

func TestOpenDBGORMAndSQLShareConnectionPool(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "freipadel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	sqlDB, err := database.ORM.DB()
	if err != nil {
		t.Fatalf("get GORM connection pool: %v", err)
	}
	if sqlDB != database.SQL {
		t.Fatal("GORM and compatibility SQL handles use different connection pools")
	}

	user := userModel{Email: "gorm@example.com", Name: "GORM", PasswordHash: "hash"}
	if err := database.ORM.Create(&user).Error; err != nil {
		t.Fatalf("create user through GORM: %v", err)
	}
	if user.ID == 0 {
		t.Error("GORM did not populate the generated user ID")
	}
	if got := scalarInt(t, database.SQL, `SELECT COUNT(*) FROM users WHERE id = ? AND email = ?`,
		user.ID, user.Email); got != 1 {
		t.Errorf("user count through SQL handle = %d, want 1", got)
	}
}

func TestGORMMetadataAndSyncWrites(t *testing.T) {
	database, err := Open(filepath.Join(t.TempDir(), "freipadel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = database.Close() })

	if got := database.GetMeta("missing"); got != "" {
		t.Errorf("missing metadata = %q, want empty", got)
	}
	if err := database.SetMeta("test_key", "first"); err != nil {
		t.Fatalf("insert metadata: %v", err)
	}
	if err := database.SetMeta("test_key", "second"); err != nil {
		t.Fatalf("update metadata: %v", err)
	}
	if got := database.GetMeta("test_key"); got != "second" {
		t.Errorf("updated metadata = %q, want second", got)
	}

	wantErr := errors.New("roll back")
	err = database.ORM.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&userModel{Email: "rollback@example.com", Name: "Rollback", PasswordHash: "hash"}).Error; err != nil {
			return err
		}
		if err := AppendSync(tx, "user", "rollback", "upsert", []byte(`{"id":1}`), 7); err != nil {
			return err
		}
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("rollback transaction error = %v, want %v", err, wantErr)
	}
	if got := scalarInt(t, database.SQL, `SELECT COUNT(*) FROM users WHERE email = 'rollback@example.com'`); got != 0 {
		t.Errorf("rolled-back user count = %d, want 0", got)
	}
	if got := scalarInt(t, database.SQL, `SELECT COUNT(*) FROM sync_log WHERE entity_id = 'rollback'`); got != 0 {
		t.Errorf("rolled-back sync count = %d, want 0", got)
	}

	err = database.ORM.Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&userModel{Email: "commit@example.com", Name: "Commit", PasswordHash: "hash"}).Error; err != nil {
			return err
		}
		return AppendSync(tx, "user", "commit", "upsert", []byte(`{"id":2}`), 0)
	})
	if err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	var payload sql.NullString
	var visible sql.NullInt64
	if err := database.SQL.QueryRow(`SELECT payload, visible_to FROM sync_log WHERE entity_id = 'commit'`).
		Scan(&payload, &visible); err != nil {
		t.Fatalf("read committed sync event: %v", err)
	}
	if !payload.Valid || payload.String != `{"id":2}` || visible.Valid {
		t.Errorf("committed sync event = payload %#v, visibility %#v", payload, visible)
	}
}

func TestOpenDBMigratesLegacyDatabaseOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	legacy, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	legacySchema := `
		CREATE TABLE users (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			email TEXT NOT NULL UNIQUE COLLATE NOCASE,
			name TEXT NOT NULL,
			password_hash TEXT NOT NULL,
			is_admin INTEGER NOT NULL DEFAULT 0,
			created_at TEXT NOT NULL DEFAULT (datetime('now'))
		);
		CREATE TABLE sessions (
			token TEXT PRIMARY KEY,
			user_id INTEGER NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			expires_at TEXT NOT NULL
		);
		CREATE TABLE invites (
			token TEXT PRIMARY KEY,
			created_by INTEGER NOT NULL REFERENCES users(id),
			created_at TEXT NOT NULL DEFAULT (datetime('now')),
			used_by INTEGER REFERENCES users(id),
			used_at TEXT
		);
		CREATE TABLE user_settings (
			user_id INTEGER PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
			weekdays TEXT NOT NULL DEFAULT '[0,1,2,3,4]',
			time_start TEXT NOT NULL DEFAULT '19:00',
			time_end TEXT NOT NULL DEFAULT '21:00',
			days_ahead INTEGER NOT NULL DEFAULT 10,
			min_duration INTEGER NOT NULL DEFAULT 60
		);
		INSERT INTO users (id, email, name, password_hash) VALUES (1, 'legacy@example.com', 'Legacy', 'hash');
		INSERT INTO sessions (token, user_id, expires_at) VALUES ('raw-session-token', 1, '2099-01-01 00:00:00');
		INSERT INTO user_settings (user_id) VALUES (1);
		INSERT INTO invites (token, created_by, used_by, used_at) VALUES ('used-invite', 1, 1, datetime('now'));
	`
	if _, err := legacy.Exec(legacySchema); err != nil {
		legacy.Close()
		t.Fatalf("create legacy database: %v", err)
	}
	if err := legacy.Close(); err != nil {
		t.Fatalf("close legacy database: %v", err)
	}

	database, err := Open(path)
	if err != nil {
		t.Fatalf("migrate legacy database: %v", err)
	}
	db := database.SQL

	var locations, notifications string
	if err := db.QueryRow(`SELECT locations, notifications FROM user_settings WHERE user_id = 1`).
		Scan(&locations, &notifications); err != nil {
		db.Close()
		t.Fatalf("read migrated settings: %v", err)
	}
	if locations != "[]" || notifications != "{}" {
		t.Errorf("settings defaults = (%q, %q), want ([], {})", locations, notifications)
	}

	var kind string
	var disabled, uses int
	var email sql.NullString
	if err := db.QueryRow(`SELECT kind, disabled, uses, email FROM invites WHERE token = 'used-invite'`).
		Scan(&kind, &disabled, &uses, &email); err != nil {
		db.Close()
		t.Fatalf("read migrated invite: %v", err)
	}
	if kind != "single" || disabled != 0 || uses != 1 || email.Valid {
		t.Errorf("invite migration = kind %q, disabled %d, uses %d, email %#v", kind, disabled, uses, email)
	}

	if got := scalarInt(t, db, `SELECT COUNT(*) FROM sessions WHERE token = ?`, "raw-session-token"); got != 0 {
		t.Errorf("plaintext session count = %d, want 0", got)
	}
	hashed := sessiontoken.Hash("raw-session-token")
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM sessions WHERE token = ?`, hashed); got != 1 {
		t.Errorf("hashed session count = %d, want 1", got)
	}
	if got := getMeta(db, "sessions_hashed"); got != "1" {
		t.Errorf("sessions_hashed = %q, want 1", got)
	}
	if got := getMeta(db, schemaVersionKey); got != strconv.Itoa(latestSchemaVersion) {
		t.Errorf("legacy schema version = %q, want %d", got, latestSchemaVersion)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close migrated database: %v", err)
	}

	// Reopening must neither double-hash sessions nor duplicate existing data.
	database, err = Open(path)
	if err != nil {
		t.Fatalf("reopen migrated database: %v", err)
	}
	db = database.SQL
	t.Cleanup(func() { _ = database.Close() })
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM sessions WHERE token = ?`, hashed); got != 1 {
		t.Errorf("hashed session count after reopen = %d, want 1", got)
	}
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM users`); got != 1 {
		t.Errorf("user count after reopen = %d, want 1", got)
	}
}

func TestSchemaMigrationRollsBackChangesAndVersionOnFailure(t *testing.T) {
	db := openTestDB(t)
	wantErr := errors.New("migration failed")
	migrations := append([]schemaMigration(nil), schemaMigrations...)
	migrations = append(migrations, schemaMigration{
		Version: latestSchemaVersion + 1,
		Name:    "failing test migration",
		Up: func(tx *sql.Tx) error {
			if err := addColumnIfMissing(tx, "meta", "migration_probe",
				`ALTER TABLE meta ADD COLUMN migration_probe TEXT`); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('rolled_back', '1')`); err != nil {
				return err
			}
			return wantErr
		},
	})

	err := applySchemaMigrations(db, migrations)
	if !errors.Is(err, wantErr) {
		t.Fatalf("apply failing migration error = %v, want %v", err, wantErr)
	}
	if got := getMeta(db, "rolled_back"); got != "" {
		t.Errorf("rolled-back migration value = %q, want empty", got)
	}
	if got := scalarInt(t, db,
		`SELECT COUNT(*) FROM pragma_table_info('meta') WHERE name = 'migration_probe'`); got != 0 {
		t.Errorf("rolled-back column count = %d, want 0", got)
	}
	if got := getMeta(db, schemaVersionKey); got != strconv.Itoa(latestSchemaVersion) {
		t.Errorf("schema version after rollback = %q, want %d", got, latestSchemaVersion)
	}
}

func TestOpenDBRejectsNewerSchemaVersion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "newer.db")
	database, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	newerVersion := latestSchemaVersion + 1
	if err := setMeta(database.SQL, schemaVersionKey, strconv.Itoa(newerVersion)); err != nil {
		database.Close()
		t.Fatalf("set newer schema version: %v", err)
	}
	if err := database.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	_, err = Open(path)
	if err == nil {
		t.Fatal("opening a newer schema version succeeded")
	}
	if !strings.Contains(err.Error(), "newer than supported") {
		t.Fatalf("newer schema error = %v", err)
	}
}

func TestSchemaEnforcesIdentityAndForeignKeys(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO users (email, name, password_hash) VALUES (?, ?, ?)`,
		"Player@example.com", "Player", "hash")
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("read user id: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO users (email, name, password_hash) VALUES (?, ?, ?)`,
		"player@EXAMPLE.com", "Duplicate", "hash"); err == nil {
		t.Fatal("case-insensitive duplicate email insert succeeded")
	}

	if _, err := db.Exec(`INSERT INTO sessions (token, user_id, expires_at) VALUES (?, ?, ?)`,
		"session", userID, "2099-01-01 00:00:00"); err != nil {
		t.Fatalf("insert session: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO user_settings (user_id) VALUES (?)`, userID); err != nil {
		t.Fatalf("insert settings: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM users WHERE id = ?`, userID); err != nil {
		t.Fatalf("delete user: %v", err)
	}
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM sessions WHERE user_id = ?`, userID); got != 0 {
		t.Errorf("session count after user deletion = %d, want 0", got)
	}
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM user_settings WHERE user_id = ?`, userID); got != 0 {
		t.Errorf("settings count after user deletion = %d, want 0", got)
	}
}

func TestVoteCompositeKeyAndPollCascade(t *testing.T) {
	db := openTestDB(t)

	res, err := db.Exec(`INSERT INTO users (email, name, password_hash) VALUES ('voter@example.com', 'Voter', 'hash')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO polls (creator_id, title) VALUES (?, 'When?')`, userID)
	if err != nil {
		t.Fatalf("insert poll: %v", err)
	}
	pollID, _ := res.LastInsertId()
	res, err = db.Exec(`INSERT INTO poll_slots (poll_id, date, time, duration_minutes, location)
		VALUES (?, '2099-01-01', '19:00', 60, 'Club')`, pollID)
	if err != nil {
		t.Fatalf("insert poll slot: %v", err)
	}
	slotID, _ := res.LastInsertId()
	if _, err := db.Exec(`INSERT INTO votes (poll_slot_id, user_id, vote) VALUES (?, ?, 1)`, slotID, userID); err != nil {
		t.Fatalf("insert vote: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO votes (poll_slot_id, user_id, vote) VALUES (?, ?, 0)`, slotID, userID); err == nil {
		t.Fatal("duplicate vote insert succeeded")
	}

	if _, err := db.Exec(`DELETE FROM polls WHERE id = ?`, pollID); err != nil {
		t.Fatalf("delete poll: %v", err)
	}
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM poll_slots WHERE poll_id = ?`, pollID); got != 0 {
		t.Errorf("poll slot count after poll deletion = %d, want 0", got)
	}
	if got := scalarInt(t, db, `SELECT COUNT(*) FROM votes WHERE poll_slot_id = ?`, slotID); got != 0 {
		t.Errorf("vote count after poll deletion = %d, want 0", got)
	}
}
