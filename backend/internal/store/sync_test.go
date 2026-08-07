package store

import (
	"path/filepath"
	"testing"
)

func TestSyncLogVisibilityAndReplay(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "sync.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for _, event := range []struct {
		id        string
		visibleTo int64
	}{
		{"public", 0}, {"user-7", 7}, {"admin", -1}, {"user-8", 8},
	} {
		if err := AppendSync(storage.ORM, "test", event.id, "upsert", []byte(`{"ok":true}`), event.visibleTo); err != nil {
			t.Fatalf("append %s event: %v", event.id, err)
		}
	}
	maxID, err := MaxSyncID(storage.ORM)
	if err != nil || maxID != 4 {
		t.Fatalf("max sync id = %d, error = %v; want 4", maxID, err)
	}

	all, err := ReadSyncLog(storage.ORM, 0, 0, false, false)
	if err != nil || len(all) != 4 {
		t.Fatalf("dispatcher events = %#v, error = %v", all, err)
	}
	user, err := ReadSyncLog(storage.ORM, 0, 7, false, true)
	if err != nil || len(user) != 2 || user[0].EntityID != "public" || user[1].EntityID != "user-7" {
		t.Fatalf("user events = %#v, error = %v", user, err)
	}
	admin, err := ReadSyncLog(storage.ORM, 0, 99, true, true)
	if err != nil || len(admin) != 2 || admin[0].EntityID != "public" || admin[1].EntityID != "admin" {
		t.Fatalf("admin events = %#v, error = %v", admin, err)
	}
	resumed, err := ReadSyncLog(storage.ORM, 2, 0, false, false)
	if err != nil || len(resumed) != 2 || resumed[0].ID != 3 {
		t.Fatalf("resumed events = %#v, error = %v", resumed, err)
	}
	if err := DeleteSyncThrough(storage.ORM, 2); err != nil {
		t.Fatalf("delete sync prefix: %v", err)
	}
	if got := scalarInt(t, storage.SQL, `SELECT COUNT(*) FROM sync_log`); got != 2 {
		t.Errorf("sync rows after deletion = %d, want 2", got)
	}
}
