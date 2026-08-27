package store

import (
	"path/filepath"
	"slices"
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
		if err := AppendSync(storage.ORM, "test", event.id, "upsert", []byte(`{"ok":true}`), event.visibleTo, 0); err != nil {
			t.Fatalf("append %s event: %v", event.id, err)
		}
	}
	maxID, err := MaxSyncID(storage.ORM)
	if err != nil || maxID != 4 {
		t.Fatalf("max sync id = %d, error = %v; want 4", maxID, err)
	}

	all, err := ReadSyncLog(storage.ORM, 0, nil)
	if err != nil || len(all) != 4 {
		t.Fatalf("dispatcher events = %#v, error = %v", all, err)
	}
	user, err := ReadSyncLog(storage.ORM, 0, &SyncAudience{UserID: 7})
	if err != nil || len(user) != 2 || user[0].EntityID != "public" || user[1].EntityID != "user-7" {
		t.Fatalf("user events = %#v, error = %v", user, err)
	}
	admin, err := ReadSyncLog(storage.ORM, 0, &SyncAudience{UserID: 99, IsAdmin: true})
	if err != nil || len(admin) != 2 || admin[0].EntityID != "public" || admin[1].EntityID != "admin" {
		t.Fatalf("admin events = %#v, error = %v", admin, err)
	}
	resumed, err := ReadSyncLog(storage.ORM, 2, nil)
	if err != nil || len(resumed) != 2 || resumed[0].ID != 3 {
		t.Fatalf("resumed events = %#v, error = %v", resumed, err)
	}
	if err := DeleteSyncThrough(storage.ORM, 2); err != nil {
		t.Fatalf("delete sync prefix: %v", err)
	}
	if got := scalarInt(t, storage.sql, `SELECT COUNT(*) FROM sync_log`); got != 2 {
		t.Errorf("sync rows after deletion = %d, want 2", got)
	}
}

// The SQL in ReadSyncLog has to agree with the caller's own visibility check
// row for row: anything it drops is lost from an SSE replay for good, because
// the client's cursor moves past it either way.
func TestSyncLogClubVisibility(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "sync-clubs.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	for _, event := range []struct {
		id        string
		visibleTo int64
		clubID    int64
	}{
		{"global", 0, 0},
		{"club-1-poll", 0, 1},
		{"club-1-invite", VisibleToAdmins, 1},
		{"club-1-personal", 7, 1},
		{"club-2-invite", VisibleToAdmins, 2},
	} {
		if err := AppendSync(storage.ORM, "test", event.id, "upsert", nil, event.visibleTo, event.clubID); err != nil {
			t.Fatalf("append %s event: %v", event.id, err)
		}
	}

	for _, tc := range []struct {
		name     string
		audience SyncAudience
		want     []string
	}{
		{"member", SyncAudience{UserID: 7, MemberOf: []int64{1}},
			[]string{"global", "club-1-poll", "club-1-personal"}},
		{"admin in no club", SyncAudience{UserID: 99, IsAdmin: true},
			[]string{"global", "club-1-invite", "club-2-invite"}},
		{"owner who is not a member", SyncAudience{UserID: 8, Owns: []int64{1}},
			[]string{"global", "club-1-invite"}},
		{"addressee outside the club", SyncAudience{UserID: 7},
			[]string{"global", "club-1-personal"}},
		{"stranger", SyncAudience{UserID: 5},
			[]string{"global"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			records, err := ReadSyncLog(storage.ORM, 0, &tc.audience)
			if err != nil {
				t.Fatalf("read log: %v", err)
			}
			got := make([]string, len(records))
			for i, record := range records {
				got[i] = record.EntityID
			}
			if !slices.Equal(got, tc.want) {
				t.Errorf("events = %v, want %v", got, tc.want)
			}
		})
	}
}
