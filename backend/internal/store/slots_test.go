package store

import (
	"path/filepath"
	"testing"
)

func TestReplaceAndQuerySlots(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "slots.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	initial := []SlotRecord{
		{Source: "source", Location: "Club", Court: "Court 1", Date: "2099-01-02", Time: "19:00", DurationMinutes: 60, Price: 30, Currency: "EUR"},
		{Source: "source", Location: "Club", Court: "Court 2", Date: "2099-01-02", Time: "19:00", DurationMinutes: 60, Price: 24, Currency: "EUR"},
		{Source: "source", Location: "Other", Court: "Court 3", Date: "2099-01-03", Time: "20:00", DurationMinutes: 90, Price: 40, Currency: "EUR"},
	}
	if err := ReplaceSlots(storage.ORM, initial); err != nil {
		t.Fatalf("replace slots: %v", err)
	}
	groups, err := ListSlotGroups(storage.ORM, SlotFilter{
		MinDate: "2099-01-01", MaxDate: "2099-01-05", TimeStart: "18:00", TimeEnd: "22:00",
		MinDuration: 60, NowTime: "00:00",
	})
	if err != nil {
		t.Fatalf("list slot groups: %v", err)
	}
	if len(groups) != 2 {
		t.Fatalf("slot group count = %d, want 2", len(groups))
	}
	if groups[0].MinPrice != 24 || (groups[0].Courts != "Court 1|Court 2" && groups[0].Courts != "Court 2|Court 1") {
		t.Errorf("aggregated group = %#v", groups[0])
	}
	locations, err := ListLocations(storage.ORM)
	if err != nil {
		t.Fatalf("list locations: %v", err)
	}
	if len(locations) != 2 || locations[0] != "Club" || locations[1] != "Other" {
		t.Errorf("locations = %#v", locations)
	}

	replacement := []SlotRecord{{Source: "new", Location: "New Club", Court: "A", Date: "2099-02-01", Time: "18:00", DurationMinutes: 60, Currency: "EUR"}}
	if err := ReplaceSlots(storage.ORM, replacement); err != nil {
		t.Fatalf("replace slots again: %v", err)
	}
	if got := scalarInt(t, storage.sql, `SELECT COUNT(*) FROM slots`); got != 1 {
		t.Errorf("slot count after replacement = %d, want 1", got)
	}
}
