package store

import (
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

const sqliteTimeLayout = "2006-01-02 15:04:05"

// assertRealTimestamp fails unless value is a UTC timestamp SQLite can order.
// The two rejected forms are the ones this codebase has actually produced: the
// literal tag text, and the empty string left behind by dropping the tag.
func assertRealTimestamp(t *testing.T, label, value string) {
	t.Helper()
	switch value {
	case "CURRENT_TIMESTAMP":
		t.Fatalf("%s = %q: GORM wrote the default tag as a literal", label, value)
	case "":
		t.Fatalf("%s is empty: the column default did not apply", label)
	}
	if _, err := time.Parse(sqliteTimeLayout, value); err != nil {
		t.Fatalf("%s = %q, want %q layout: %v", label, value, sqliteTimeLayout, err)
	}
}

// TestWritesRealTimestamps covers every ORM path that persists a timestamp.
func TestWritesRealTimestamps(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "timestamps.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	user, err := CreateUser(storage.ORM, "admin@example.com", "Admin", "hash", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if err := CreateInvite(storage.ORM, "token", user.ID, "single", nil); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	pollID, err := CreatePoll(storage.ORM, user.ID, "Tuesday", []PollSlotRecord{{
		Date: "2026-06-09", Time: "19:00", DurationMinutes: 90, Location: "Club", Court: "Court 1",
	}})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	if err := AppendSync(storage.ORM, "poll", strconv.FormatInt(pollID, 10), "upsert", []byte(`{}`), 0); err != nil {
		t.Fatalf("append sync: %v", err)
	}
	slots, err := ListPollSlots(storage.ORM, &pollID)
	if err != nil || len(slots) != 1 {
		t.Fatalf("list poll slots = %d slots, error = %v", len(slots), err)
	}
	if err := UpsertVote(storage.ORM, slots[0].ID, user.ID, true); err != nil {
		t.Fatalf("upsert vote: %v", err)
	}

	for _, probe := range []struct{ label, query string }{
		{"users.created_at", `SELECT created_at FROM users`},
		{"invites.created_at", `SELECT created_at FROM invites`},
		{"polls.created_at", `SELECT created_at FROM polls`},
		{"sync_log.created_at", `SELECT created_at FROM sync_log`},
		{"votes.updated_at", `SELECT updated_at FROM votes`},
	} {
		var value string
		if err := storage.sql.QueryRow(probe.query).Scan(&value); err != nil {
			t.Fatalf("%s: %v", probe.label, err)
		}
		assertRealTimestamp(t, probe.label, value)
	}
}

// TestSyncLogCompactionFindsExpiredRows guards the consequence that made this
// bug expensive: with a literal timestamp no row was ever older than the cutoff,
// so the retention sweep silently did nothing and the log grew without bound.
func TestSyncLogCompactionFindsExpiredRows(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "compaction.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	if err := AppendSync(storage.ORM, "poll", "1", "upsert", []byte(`{}`), 0); err != nil {
		t.Fatalf("append sync: %v", err)
	}
	if _, err := storage.sql.Exec(
		`UPDATE sync_log SET created_at = datetime('now', '-30 days')`); err != nil {
		t.Fatalf("backdate: %v", err)
	}
	if err := AppendSync(storage.ORM, "poll", "2", "upsert", []byte(`{}`), 0); err != nil {
		t.Fatalf("append recent sync: %v", err)
	}

	expired, err := MaxExpiredSyncID(storage.ORM)
	if err != nil {
		t.Fatalf("max expired sync id: %v", err)
	}
	if expired != 1 {
		t.Fatalf("MaxExpiredSyncID = %d, want 1 (the backdated row only)", expired)
	}
}

// TestMigrationRepairsLiteralTimestamps verifies databases written by the broken
// build are repaired on upgrade rather than staying permanently unsortable.
func TestMigrationRepairsLiteralTimestamps(t *testing.T) {
	path := filepath.Join(t.TempDir(), "repair.db")
	storage, err := Open(path)
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	if _, err := storage.sql.Exec(
		`INSERT INTO users (email, name, password_hash, is_admin, created_at)
		 VALUES ('legacy@example.com', 'Legacy', 'hash', 0, 'CURRENT_TIMESTAMP')`); err != nil {
		t.Fatalf("insert corrupt row: %v", err)
	}
	if _, err := storage.sql.Exec(
		`INSERT INTO sync_log (entity, entity_id, action, created_at)
		 VALUES ('poll', '1', 'upsert', 'CURRENT_TIMESTAMP')`); err != nil {
		t.Fatalf("insert corrupt sync row: %v", err)
	}
	// Rewind past the repair so reopening replays it, as an upgrade would.
	if err := setMeta(storage.sql, schemaVersionKey, "3"); err != nil {
		t.Fatalf("rewind schema version: %v", err)
	}
	if err := storage.Close(); err != nil {
		t.Fatalf("close database: %v", err)
	}

	reopened, err := Open(path)
	if err != nil {
		t.Fatalf("reopen database: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	if got := getMeta(reopened.sql, schemaVersionKey); got != strconv.Itoa(latestSchemaVersion) {
		t.Fatalf("schema version = %q, want %d", got, latestSchemaVersion)
	}
	for _, probe := range []struct{ label, query string }{
		{"users.created_at", `SELECT created_at FROM users WHERE email = 'legacy@example.com'`},
		{"sync_log.created_at", `SELECT created_at FROM sync_log`},
	} {
		var value string
		if err := reopened.sql.QueryRow(probe.query).Scan(&value); err != nil {
			t.Fatalf("%s: %v", probe.label, err)
		}
		assertRealTimestamp(t, probe.label, value)
	}
}
