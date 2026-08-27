package store

import (
	"path/filepath"
	"testing"
)

func openClubTestStore(t *testing.T, name string) *Store {
	t.Helper()
	storage, err := Open(filepath.Join(t.TempDir(), name))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })
	return storage
}

func mustUser(t *testing.T, storage *Store, email, name string) UserRecord {
	t.Helper()
	user, err := CreateUser(storage.ORM, email, name, "hash", false)
	if err != nil {
		t.Fatalf("create user %s: %v", email, err)
	}
	return user
}

func TestClubMembershipRoundTrip(t *testing.T) {
	storage := openClubTestStore(t, "clubs.db")
	owner := mustUser(t, storage, "owner@example.com", "Owner")
	guest := mustUser(t, storage, "guest@example.com", "Guest")

	club, err := CreateClub(storage.ORM, "Köln Crew", owner.ID)
	if err != nil {
		t.Fatalf("create club: %v", err)
	}
	if club.Locations != "[]" {
		t.Errorf("new club locations = %q, want []", club.Locations)
	}

	// Adding twice must not fail: redeeming an invite you already used is a
	// normal thing to do.
	for range 2 {
		if err := AddClubMember(storage.ORM, club.ID, guest.ID); err != nil {
			t.Fatalf("add club member: %v", err)
		}
	}
	if count, err := CountClubMembers(storage.ORM, club.ID); err != nil || count != 1 {
		t.Fatalf("club members = %d, error = %v; want 1", count, err)
	}
	if member, err := IsClubMember(storage.ORM, club.ID, guest.ID); err != nil || !member {
		t.Fatalf("IsClubMember = %v, error = %v; want true", member, err)
	}

	clubs, err := ListClubsForUser(storage.ORM, guest.ID)
	if err != nil || len(clubs) != 1 || clubs[0].ID != club.ID {
		t.Fatalf("clubs for guest = %#v, error = %v", clubs, err)
	}
	if owned, err := ListOwnedClubIDs(storage.ORM, owner.ID); err != nil || len(owned) != 1 {
		t.Fatalf("owned clubs = %#v, error = %v; want 1", owned, err)
	}
	if owned, err := ListOwnedClubIDs(storage.ORM, guest.ID); err != nil || len(owned) != 0 {
		t.Fatalf("guest owns %#v, error = %v; want none", owned, err)
	}
}

// Losing your active club must leave you somewhere sensible: another club you
// are still in, or none at all — never a dangling id.
func TestRemoveClubMemberMovesTheActiveClub(t *testing.T) {
	storage := openClubTestStore(t, "active.db")
	user := mustUser(t, storage, "player@example.com", "Player")

	first, err := CreateClub(storage.ORM, "First", user.ID)
	if err != nil {
		t.Fatalf("create first club: %v", err)
	}
	second, err := CreateClub(storage.ORM, "Second", user.ID)
	if err != nil {
		t.Fatalf("create second club: %v", err)
	}
	for _, club := range []ClubRecord{first, second} {
		if err := AddClubMember(storage.ORM, club.ID, user.ID); err != nil {
			t.Fatalf("add club member: %v", err)
		}
	}
	if err := SetActiveClub(storage.ORM, user.ID, &first.ID); err != nil {
		t.Fatalf("set active club: %v", err)
	}

	if err := RemoveClubMember(storage.ORM, first.ID, user.ID); err != nil {
		t.Fatalf("remove club member: %v", err)
	}
	moved, err := FindUserByID(storage.ORM, user.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if moved.ActiveClubID == nil || *moved.ActiveClubID != second.ID {
		t.Fatalf("active club = %v, want %d", moved.ActiveClubID, second.ID)
	}

	if err := RemoveClubMember(storage.ORM, second.ID, user.ID); err != nil {
		t.Fatalf("remove last club member: %v", err)
	}
	stranded, err := FindUserByID(storage.ORM, user.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if stranded.ActiveClubID != nil {
		t.Fatalf("active club = %v, want none", *stranded.ActiveClubID)
	}
}

// You see the people you share a club with, and nobody else.
func TestListVisibleMembersStopsAtTheClubBoundary(t *testing.T) {
	storage := openClubTestStore(t, "visible.db")
	alice := mustUser(t, storage, "alice@example.com", "Alice")
	bob := mustUser(t, storage, "bob@example.com", "Bob")
	stranger := mustUser(t, storage, "stranger@example.com", "Stranger")

	shared, err := CreateClub(storage.ORM, "Shared", alice.ID)
	if err != nil {
		t.Fatalf("create shared club: %v", err)
	}
	other, err := CreateClub(storage.ORM, "Other", stranger.ID)
	if err != nil {
		t.Fatalf("create other club: %v", err)
	}
	for _, id := range []int64{alice.ID, bob.ID} {
		if err := AddClubMember(storage.ORM, shared.ID, id); err != nil {
			t.Fatalf("add shared member: %v", err)
		}
	}
	if err := AddClubMember(storage.ORM, other.ID, stranger.ID); err != nil {
		t.Fatalf("add other member: %v", err)
	}

	members, err := ListVisibleMembers(storage.ORM, alice.ID)
	if err != nil {
		t.Fatalf("list visible members: %v", err)
	}
	names := make([]string, len(members))
	for i, member := range members {
		names[i] = member.Name
		if member.ID == stranger.ID {
			t.Errorf("alice can see %q from another club", member.Name)
		}
	}
	if len(members) != 2 {
		t.Fatalf("visible members = %v, want Alice and Bob", names)
	}

	// Somebody in no club at all still sees themselves, and only themselves.
	loner := mustUser(t, storage, "loner@example.com", "Loner")
	alone, err := ListVisibleMembers(storage.ORM, loner.ID)
	if err != nil || len(alone) != 1 || alone[0].ID != loner.ID {
		t.Fatalf("visible members for a clubless user = %#v, error = %v", alone, err)
	}
}

// Deleting a club takes its polls and invites with it, and never strands a
// user pointing at an id that is gone.
func TestDeleteClubRemovesWhatBelongedToIt(t *testing.T) {
	storage := openClubTestStore(t, "delete.db")
	owner := mustUser(t, storage, "owner@example.com", "Owner")
	club, err := CreateClub(storage.ORM, "Doomed", owner.ID)
	if err != nil {
		t.Fatalf("create club: %v", err)
	}
	if err := AddClubMember(storage.ORM, club.ID, owner.ID); err != nil {
		t.Fatalf("add club member: %v", err)
	}
	if err := SetActiveClub(storage.ORM, owner.ID, &club.ID); err != nil {
		t.Fatalf("set active club: %v", err)
	}
	if err := CreateInvite(storage.ORM, "doomed-invite", owner.ID, club.ID, "single", nil); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if _, err := CreatePoll(storage.ORM, club.ID, owner.ID, "Doomed?", []PollSlotRecord{
		{Date: "2099-01-01", Time: "19:00", DurationMinutes: 60, Location: "Club", Court: "1"},
	}); err != nil {
		t.Fatalf("create poll: %v", err)
	}

	if err := DeleteClub(storage.ORM, club.ID); err != nil {
		t.Fatalf("delete club: %v", err)
	}
	if clubs, err := ListClubs(storage.ORM); err != nil || len(clubs) != 0 {
		t.Fatalf("clubs after deletion = %#v, error = %v", clubs, err)
	}
	if polls, err := ListPolls(storage.ORM, []int64{club.ID}); err != nil || len(polls) != 0 {
		t.Fatalf("polls after deletion = %#v, error = %v", polls, err)
	}
	if invites, err := ListInvites(storage.ORM, nil); err != nil || len(invites) != 0 {
		t.Fatalf("invites after deletion = %#v, error = %v", invites, err)
	}
	orphan, err := FindUserByID(storage.ORM, owner.ID)
	if err != nil {
		t.Fatalf("find user: %v", err)
	}
	if orphan.ActiveClubID != nil {
		t.Fatalf("active club after deletion = %v, want none", *orphan.ActiveClubID)
	}
}

// Polls and votes are club-scoped at the query layer, not just in the handlers.
func TestPollQueriesStayInsideTheirClubs(t *testing.T) {
	storage := openClubTestStore(t, "scope.db")
	user := mustUser(t, storage, "player@example.com", "Player")
	mine, err := CreateClub(storage.ORM, "Mine", user.ID)
	if err != nil {
		t.Fatalf("create club: %v", err)
	}
	theirs, err := CreateClub(storage.ORM, "Theirs", user.ID)
	if err != nil {
		t.Fatalf("create club: %v", err)
	}

	for _, club := range []ClubRecord{mine, theirs} {
		pollID, err := CreatePoll(storage.ORM, club.ID, user.ID, club.Name, []PollSlotRecord{
			{Date: "2099-01-01", Time: "19:00", DurationMinutes: 60, Location: "Club", Court: "1"},
		})
		if err != nil {
			t.Fatalf("create poll in %s: %v", club.Name, err)
		}
		slots, err := ListPollSlots(storage.ORM, &pollID, nil)
		if err != nil || len(slots) != 1 {
			t.Fatalf("poll slots = %#v, error = %v", slots, err)
		}
		if err := UpsertVote(storage.ORM, slots[0].ID, user.ID, true); err != nil {
			t.Fatalf("vote in %s: %v", club.Name, err)
		}
	}

	polls, err := ListPolls(storage.ORM, []int64{mine.ID})
	if err != nil || len(polls) != 1 || polls[0].Title != "Mine" {
		t.Fatalf("polls for one club = %#v, error = %v", polls, err)
	}
	if polls[0].ClubID != mine.ID {
		t.Errorf("poll club = %d, want %d", polls[0].ClubID, mine.ID)
	}
	votes, err := ListVotes(storage.ORM, []int64{mine.ID})
	if err != nil || len(votes) != 1 {
		t.Fatalf("votes for one club = %#v, error = %v", votes, err)
	}
	slots, err := ListPollSlots(storage.ORM, nil, []int64{mine.ID})
	if err != nil || len(slots) != 1 {
		t.Fatalf("slots for one club = %#v, error = %v", slots, err)
	}

	// A user in no club sees nothing at all rather than everything.
	for _, check := range []func() (int, error){
		func() (int, error) { p, err := ListPolls(storage.ORM, nil); return len(p), err },
		func() (int, error) { v, err := ListVotes(storage.ORM, nil); return len(v), err },
		func() (int, error) { s, err := ListPollSlots(storage.ORM, nil, nil); return len(s), err },
	} {
		got, err := check()
		if err != nil || got != 0 {
			t.Fatalf("clubless query returned %d rows, error = %v; want 0", got, err)
		}
	}
}
