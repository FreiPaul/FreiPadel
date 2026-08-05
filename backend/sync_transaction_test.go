package main

import (
	"database/sql"
	"path/filepath"
	"testing"

	"freipadel/internal/store"
)

func syncTestScalarInt(t *testing.T, db *sql.DB, query string, args ...any) int {
	t.Helper()
	var got int
	if err := db.QueryRow(query, args...).Scan(&got); err != nil {
		t.Fatalf("query scalar %q: %v", query, err)
	}
	return got
}

func TestDomainAndSyncWritesShareTransactionBoundary(t *testing.T) {
	storage, err := store.Open(filepath.Join(t.TempDir(), "freipadel.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	db := storage.SQL

	res, err := db.Exec(`INSERT INTO users (email, name, password_hash) VALUES ('creator@example.com', 'Creator', 'hash')`)
	if err != nil {
		t.Fatalf("insert user: %v", err)
	}
	userID, _ := res.LastInsertId()

	tx, err := db.Begin()
	if err != nil {
		t.Fatalf("begin rollback transaction: %v", err)
	}
	res, err = tx.Exec(`INSERT INTO polls (creator_id, title) VALUES (?, 'Rolled back')`, userID)
	if err != nil {
		t.Fatalf("insert rolled-back poll: %v", err)
	}
	pollID, _ := res.LastInsertId()
	if err := appendSync(tx, "poll", "rollback", "upsert", map[string]int64{"id": pollID}, 0); err != nil {
		t.Fatalf("append rolled-back sync event: %v", err)
	}
	if err := tx.Rollback(); err != nil {
		t.Fatalf("rollback transaction: %v", err)
	}
	if got := syncTestScalarInt(t, db, `SELECT COUNT(*) FROM polls WHERE id = ?`, pollID); got != 0 {
		t.Errorf("rolled-back poll count = %d, want 0", got)
	}
	if got := syncTestScalarInt(t, db, `SELECT COUNT(*) FROM sync_log WHERE entity_id = 'rollback'`); got != 0 {
		t.Errorf("rolled-back sync count = %d, want 0", got)
	}

	tx, err = db.Begin()
	if err != nil {
		t.Fatalf("begin commit transaction: %v", err)
	}
	res, err = tx.Exec(`INSERT INTO polls (creator_id, title) VALUES (?, 'Committed')`, userID)
	if err != nil {
		t.Fatalf("insert committed poll: %v", err)
	}
	pollID, _ = res.LastInsertId()
	if err := appendSync(tx, "poll", "commit", "upsert", map[string]int64{"id": pollID}, 0); err != nil {
		t.Fatalf("append committed sync event: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatalf("commit transaction: %v", err)
	}
	if got := syncTestScalarInt(t, db, `SELECT COUNT(*) FROM polls WHERE id = ?`, pollID); got != 1 {
		t.Errorf("committed poll count = %d, want 1", got)
	}
	if got := syncTestScalarInt(t, db, `SELECT COUNT(*) FROM sync_log WHERE entity_id = 'commit'`); got != 1 {
		t.Errorf("committed sync count = %d, want 1", got)
	}
}
