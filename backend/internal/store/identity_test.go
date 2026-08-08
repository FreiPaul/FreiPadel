package store

import (
	"path/filepath"
	"testing"
)

func TestIdentityAndSettingsPersistence(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "identity.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	user, err := CreateUser(storage.ORM, "player@example.com", "Player", "password-hash", true)
	if err != nil {
		t.Fatalf("create user: %v", err)
	}
	if user.ID == 0 || !user.IsAdmin {
		t.Fatalf("created user = %#v", user)
	}
	if err := CreateDefaultSettings(storage.ORM, user.ID); err != nil {
		t.Fatalf("create default settings: %v", err)
	}
	settings, err := FindSettings(storage.ORM, user.ID)
	if err != nil {
		t.Fatalf("find default settings: %v", err)
	}
	if settings.TimeStart != "19:00" || settings.Locations != "[]" {
		t.Errorf("default settings = %#v", settings)
	}
	settings.TimeStart = "18:30"
	settings.Locations = `["Club"]`
	if err := UpsertSettings(storage.ORM, settings); err != nil {
		t.Fatalf("upsert settings: %v", err)
	}
	updated, err := FindSettings(storage.ORM, user.ID)
	if err != nil {
		t.Fatalf("find updated settings: %v", err)
	}
	if updated.TimeStart != "18:30" || updated.Locations != `["Club"]` {
		t.Errorf("updated settings = %#v", updated)
	}

	if err := CreateSession(storage.ORM, "token-hash", user.ID, "2099-01-01 00:00:00"); err != nil {
		t.Fatalf("create session: %v", err)
	}
	fromSession, err := FindUserBySession(storage.ORM, "token-hash")
	if err != nil {
		t.Fatalf("find user by session: %v", err)
	}
	if fromSession.ID != user.ID || fromSession.Email != user.Email || !fromSession.IsAdmin {
		t.Errorf("session user = %#v, want %#v", fromSession, user)
	}
	if err := DeleteSession(storage.ORM, "token-hash"); err != nil {
		t.Fatalf("delete session: %v", err)
	}
	if _, err := FindUserBySession(storage.ORM, "token-hash"); !IsNotFound(err) {
		t.Fatalf("deleted session lookup error = %v, want not found", err)
	}
}

func TestInvitePersistence(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "invites.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	creator, err := CreateUser(storage.ORM, "admin@example.com", "Admin", "hash", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	email := "friend@example.com"
	if err := CreateInvite(storage.ORM, "invite-token", creator.ID, "email", &email); err != nil {
		t.Fatalf("create invite: %v", err)
	}
	if exists, err := UserOrInviteEmailExists(storage.ORM, email); err != nil || !exists {
		t.Fatalf("email exists = %v, error = %v", exists, err)
	}
	invite, err := FindInvite(storage.ORM, "invite-token")
	if err != nil {
		t.Fatalf("find invite: %v", err)
	}
	if invite.Email == nil || *invite.Email != email || invite.Uses != 0 || invite.Disabled {
		t.Errorf("created invite = %#v", invite)
	}

	friend, err := CreateUser(storage.ORM, email, "Friend", "hash", false)
	if err != nil {
		t.Fatalf("create friend: %v", err)
	}
	if err := RedeemInvite(storage.ORM, invite.Token, friend.ID); err != nil {
		t.Fatalf("redeem invite: %v", err)
	}
	invite, err = FindInvite(storage.ORM, invite.Token)
	if err != nil {
		t.Fatalf("find redeemed invite: %v", err)
	}
	if invite.Uses != 1 || invite.UsedByID == nil || *invite.UsedByID != friend.ID || invite.UsedByName == nil {
		t.Errorf("redeemed invite = %#v", invite)
	}
	invites, err := ListInvites(storage.ORM)
	if err != nil {
		t.Fatalf("list invites: %v", err)
	}
	if len(invites) != 1 || invites[0].UsedByName == nil || *invites[0].UsedByName != friend.Name {
		t.Errorf("listed invites = %#v", invites)
	}
	if affected, err := DeleteInvite(storage.ORM, invite.Token); err != nil || affected != 0 {
		t.Errorf("delete used invite affected = %d, error = %v; want 0", affected, err)
	}
}
