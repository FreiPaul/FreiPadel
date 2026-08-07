package store

import (
	"path/filepath"
	"testing"
)

func TestPollAndVotePersistence(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "polls.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	user, err := CreateUser(storage.ORM, "creator@example.com", "Creator", "hash", false)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	pollID, err := CreatePoll(storage.ORM, user.ID, "When?", []PollSlotRecord{
		{Date: "2099-01-01", Time: "19:00", DurationMinutes: 60, Location: "Club", Court: "1, 2", Price: 20, Currency: "EUR"},
		{Date: "2099-01-02", Time: "20:00", DurationMinutes: 90, Location: "Club", Court: "3", Price: 30, Currency: "EUR"},
	})
	if err != nil {
		t.Fatalf("create poll: %v", err)
	}
	poll, err := FindPoll(storage.ORM, pollID)
	if err != nil {
		t.Fatalf("find poll: %v", err)
	}
	if poll.Title != "When?" || poll.CreatorName != "Creator" || poll.Status != "active" {
		t.Errorf("created poll = %#v", poll)
	}
	slots, err := ListPollSlots(storage.ORM, &pollID)
	if err != nil || len(slots) != 2 {
		t.Fatalf("poll slots = %#v, error = %v", slots, err)
	}
	belongs, err := PollSlotBelongsTo(storage.ORM, slots[0].ID, pollID)
	if err != nil || !belongs {
		t.Fatalf("slot belongs = %v, error = %v", belongs, err)
	}

	if err := UpsertVote(storage.ORM, slots[0].ID, user.ID, false); err != nil {
		t.Fatalf("insert false vote: %v", err)
	}
	if err := UpsertVote(storage.ORM, slots[0].ID, user.ID, true); err != nil {
		t.Fatalf("update vote: %v", err)
	}
	votes, err := ListVotes(storage.ORM)
	if err != nil || len(votes) != 1 || !votes[0].Vote || votes[0].Name != "Creator" {
		t.Fatalf("votes = %#v, error = %v", votes, err)
	}

	winner := slots[0].ID
	if err := ClosePoll(storage.ORM, pollID, &winner); err != nil {
		t.Fatalf("close poll: %v", err)
	}
	poll, err = FindPoll(storage.ORM, pollID)
	if err != nil || poll.Status != "closed" || poll.WinningSlotID == nil || *poll.WinningSlotID != winner {
		t.Fatalf("closed poll = %#v, error = %v", poll, err)
	}
	if err := DeletePoll(storage.ORM, pollID); err != nil {
		t.Fatalf("delete poll: %v", err)
	}
	if got := scalarInt(t, storage.sql, `SELECT COUNT(*) FROM poll_slots WHERE poll_id = ?`, pollID); got != 0 {
		t.Errorf("poll slots after deletion = %d, want 0", got)
	}
}
