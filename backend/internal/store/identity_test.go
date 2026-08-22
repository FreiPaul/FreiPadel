package store

import (
	"path/filepath"
	"testing"
	"time"
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

func TestPendingEmailChangePersistence(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "email-change.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	first, err := CreateUser(storage.ORM, "first@example.com", "First", "hash", false)
	if err != nil {
		t.Fatalf("create first user: %v", err)
	}
	second, err := CreateUser(storage.ORM, "second@example.com", "Second", "hash", false)
	if err != nil {
		t.Fatalf("create second user: %v", err)
	}

	if err := UpsertPendingEmailChange(storage.ORM, first.ID, "new@example.com", "first-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("create pending email change: %v", err)
	}
	pending, err := FindPendingEmailChangeByTokenHash(storage.ORM, "first-token")
	if err != nil {
		t.Fatalf("find pending email change: %v", err)
	}
	if pending.UserID != first.ID || pending.NewEmail != "new@example.com" {
		t.Fatalf("pending email change = %#v", pending)
	}
	if exists, err := PendingEmailChangeEmailExists(storage.ORM, "NEW@example.com", second.ID); err != nil || !exists {
		t.Fatalf("case-insensitive pending reservation = %v, error = %v", exists, err)
	}
	if exists, err := PendingEmailChangeEmailExists(storage.ORM, "new@example.com", first.ID); err != nil || exists {
		t.Fatalf("own pending reservation = %v, error = %v", exists, err)
	}
	if err := UpsertPendingEmailChange(storage.ORM, second.ID, "NEW@example.com", "duplicate-token", time.Now().Add(time.Hour)); err == nil {
		t.Fatal("case-insensitive duplicate pending email was accepted")
	}

	if err := UpsertPendingEmailChange(storage.ORM, first.ID, "replacement@example.com", "second-token", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("replace pending email change: %v", err)
	}
	if _, err := FindPendingEmailChangeByTokenHash(storage.ORM, "first-token"); !IsNotFound(err) {
		t.Fatalf("replaced token lookup error = %v, want not found", err)
	}
	if pending, err = FindPendingEmailChangeByUserID(storage.ORM, first.ID); err != nil || pending.TokenHash != "second-token" {
		t.Fatalf("replacement pending email change = %#v, error = %v", pending, err)
	}

	if err := UpsertPendingEmailChange(storage.ORM, second.ID, "expired@example.com", "expired-token", time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("create expired email change: %v", err)
	}
	if err := DeleteExpiredPendingEmailChanges(storage.ORM); err != nil {
		t.Fatalf("delete expired email changes: %v", err)
	}
	if _, err := FindPendingEmailChangeByTokenHash(storage.ORM, "expired-token"); !IsNotFound(err) {
		t.Fatalf("expired token lookup error = %v, want not found", err)
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

// TestUserOrInviteEmailExistsMatchesOnlyTheGivenAddress guards against GORM's
// struct-condition rule, which silently drops zero-valued fields: a struct
// condition built from an empty email emits no WHERE clause and counts every row,
// which made every single- and group-invite creation fail with 409.
func TestUserOrInviteEmailExistsMatchesOnlyTheGivenAddress(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "exists.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	admin, err := CreateUser(storage.ORM, "admin@example.com", "Admin", "hash", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	pending := "pending@example.com"
	if err := CreateInvite(storage.ORM, "pending-token", admin.ID, "email", &pending); err != nil {
		t.Fatalf("create invite: %v", err)
	}

	tests := []struct {
		name  string
		email string
		want  bool
	}{
		{"empty address matches nothing", "", false},
		{"unknown address", "stranger@example.com", false},
		{"existing account", "admin@example.com", true},
		{"outstanding invite", pending, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := UserOrInviteEmailExists(storage.ORM, tt.email)
			if err != nil {
				t.Fatalf("UserOrInviteEmailExists(%q): %v", tt.email, err)
			}
			if got != tt.want {
				t.Fatalf("UserOrInviteEmailExists(%q) = %v, want %v", tt.email, got, tt.want)
			}
		})
	}
}

// TestCreateInviteWithoutEmailStoresNull verifies single and group invites leave
// the column NULL rather than "", so they are reported as having no address.
func TestCreateInviteWithoutEmailStoresNull(t *testing.T) {
	storage, err := Open(filepath.Join(t.TempDir(), "nullemail.db"))
	if err != nil {
		t.Fatalf("open database: %v", err)
	}
	t.Cleanup(func() { _ = storage.Close() })

	admin, err := CreateUser(storage.ORM, "admin@example.com", "Admin", "hash", true)
	if err != nil {
		t.Fatalf("create admin: %v", err)
	}
	for _, kind := range []string{"single", "group"} {
		t.Run(kind, func(t *testing.T) {
			token := kind + "-token"
			if err := CreateInvite(storage.ORM, token, admin.ID, kind, nil); err != nil {
				t.Fatalf("create %s invite: %v", kind, err)
			}
			invite, err := FindInvite(storage.ORM, token)
			if err != nil {
				t.Fatalf("find %s invite: %v", kind, err)
			}
			if invite.Email != nil {
				t.Fatalf("%s invite email = %q, want NULL", kind, *invite.Email)
			}
			// An address-less invite must never collide with a real address.
			if exists, err := UserOrInviteEmailExists(storage.ORM, ""); err != nil || exists {
				t.Fatalf("empty-address lookup after %s invite = %v, error = %v", kind, exists, err)
			}
		})
	}
}
