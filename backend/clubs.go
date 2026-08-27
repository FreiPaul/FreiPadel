package main

// Clubs are the groups users organise in. A user can belong to several and
// switches between them; everything club-scoped — polls, votes, venues,
// invites, the member directory — follows whichever club is active.

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"freipadel/internal/store"
	"gorm.io/gorm"
)

// defaultClubName is the club the first account founds, and the one migration 6
// gathers an existing deployment into.
const defaultClubName = "All"

type Club struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	OwnerID   int64    `json:"owner_id"`
	IsOwner   bool     `json:"is_owner"`
	Locations []string `json:"locations"` // empty = every venue
}

// AdminClub is the richer shape the club administration page renders.
type AdminClub struct {
	Club
	OwnerName   string `json:"owner_name"`
	MemberCount int64  `json:"member_count"`
}

func clubFromRecord(record store.ClubRecord, userID int64) Club {
	return Club{
		ID: record.ID, Name: record.Name, OwnerID: record.OwnerID,
		IsOwner: record.OwnerID == userID, Locations: parseLocations(record.Locations),
	}
}

// parseLocations reads a club's venue JSON. A malformed or absent value means
// "every venue", the same convention user_settings.locations uses.
func parseLocations(stored string) []string {
	var locations []string
	if err := json.Unmarshal([]byte(stored), &locations); err != nil || locations == nil {
		return []string{}
	}
	return normalizeLocationNames(locations)
}

// joinClub adds a membership and points the user at it. Used by registration,
// invite redemption and the admin member editor alike.
func (a *App) joinClub(tx *gorm.DB, userID, clubID int64) error {
	if err := a.addMember(tx, clubID, userID); err != nil {
		return err
	}
	return store.SetActiveClub(tx, userID, &clubID)
}

// addMember is the only way a membership is created, so that no path can add
// someone without announcing them. The member store on each client is built
// once at bootstrap and kept current by these deltas; without one, a club's
// open tabs never learn about anybody who arrives after they connected.
func (a *App) addMember(tx *gorm.DB, clubID, userID int64) error {
	if err := store.AddClubMember(tx, clubID, userID); err != nil {
		return err
	}
	user, err := store.FindUserByID(tx, userID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(syncMember{ID: user.ID, Name: user.Name, IsAdmin: user.IsAdmin})
	return store.AppendSync(tx, "user", strconv.FormatInt(user.ID, 10), "upsert", payload, 0, clubID)
}

// syncMembership tells one user's open connections that their clubs changed.
// The client re-bootstraps on it, which also rebuilds the server-side
// subscriber and so its idea of which clubs may be read.
func (a *App) syncMembership(tx *gorm.DB, userID int64) error {
	clubs, err := store.ListClubsForUser(tx, userID)
	if err != nil {
		return err
	}
	user, err := store.FindUserByID(tx, userID)
	if err != nil {
		return err
	}
	payload, _ := json.Marshal(map[string]any{
		"clubs":          clubList(clubs, userID),
		"active_club_id": user.ActiveClubID,
	})
	return store.AppendSync(tx, "me", strconv.FormatInt(userID, 10), "upsert", payload, userID, 0)
}

func clubList(records []store.ClubRecord, userID int64) []Club {
	clubs := make([]Club, len(records))
	for i, record := range records {
		clubs[i] = clubFromRecord(record, userID)
	}
	return clubs
}

// syncClub republishes a club to its members.
func (a *App) syncClub(tx *gorm.DB, clubID int64) error {
	record, err := store.FindClub(tx, clubID)
	if err != nil {
		return err
	}
	// is_owner is per reader, so it is resolved client-side from owner_id.
	payload, _ := json.Marshal(clubFromRecord(record, 0))
	return store.AppendSync(tx, "club", strconv.FormatInt(clubID, 10), "upsert", payload, 0, clubID)
}

// GET /api/clubs — the caller's own clubs and which one is active.
func (a *App) handleListClubs(w http.ResponseWriter, r *http.Request, u *User) {
	records, err := store.ListClubsForUser(a.orm.WithContext(r.Context()), u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"clubs":          clubList(records, u.ID),
		"active_club_id": u.ActiveClubID,
	})
}

// POST /api/clubs/{id}/activate — switch which club the app is showing.
func (a *App) handleActivateClub(w http.ResponseWriter, r *http.Request, u *User, clubID int64) {
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.SetActiveClub(tx, u.ID, &clubID); err != nil {
			return err
		}
		return a.syncMembership(tx, u.ID)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]any{"active_club_id": clubID})
}

// GET /api/admin/clubs — every club, for the administration page. Club
// administration is deliberately REST rather than sync: it changes rarely and
// nobody needs to watch it live.
func (a *App) handleAdminListClubs(w http.ResponseWriter, r *http.Request, u *User) {
	db := a.orm.WithContext(r.Context())
	records, err := store.ListClubs(db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	// The pool holds a single connection, so each result set is drained before
	// the next query runs.
	owners, err := store.ListMembers(db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	ownerNames := make(map[int64]string, len(owners))
	for _, owner := range owners {
		ownerNames[owner.ID] = owner.Name
	}

	clubs := make([]AdminClub, len(records))
	for i, record := range records {
		count, err := store.CountClubMembers(db, record.ID)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		clubs[i] = AdminClub{
			Club:        clubFromRecord(record, u.ID),
			OwnerName:   ownerNames[record.OwnerID],
			MemberCount: count,
		}
	}
	writeJSON(w, http.StatusOK, clubs)
}

// POST /api/admin/clubs — create a club. Only global admins may.
func (a *App) handleCreateClub(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Name    string `json:"name"`
		OwnerID int64  `json:"owner_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		httpError(w, http.StatusBadRequest, "club name is required")
		return
	}
	ownerID := req.OwnerID
	if ownerID == 0 {
		ownerID = u.ID
	}

	var club store.ClubRecord
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := store.FindUserByID(tx, ownerID); err != nil {
			return errClubOwnerUnknown
		}
		var err error
		club, err = store.CreateClub(tx, name, ownerID)
		if err != nil {
			return err
		}
		if err := a.addMember(tx, club.ID, ownerID); err != nil {
			return err
		}
		if err := a.syncClub(tx, club.ID); err != nil {
			return err
		}
		return a.syncMembership(tx, ownerID)
	})
	if errors.Is(err, errClubOwnerUnknown) {
		httpError(w, http.StatusBadRequest, "unknown owner")
		return
	}
	if err != nil {
		httpError(w, http.StatusConflict, "a club with this name already exists")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusCreated, clubFromRecord(club, u.ID))
}

// PATCH /api/clubs/{id} — rename, hand over, or set the club's venues. Venues
// need no endpoint of their own: they are a field on the club row.
func (a *App) handleUpdateClub(w http.ResponseWriter, r *http.Request, u *User, clubID int64) {
	var req struct {
		Name      *string   `json:"name"`
		OwnerID   *int64    `json:"owner_id"`
		Locations *[]string `json:"locations"`
	}
	if !readJSON(w, r, &req) {
		return
	}

	var updated store.ClubRecord
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		club, err := store.FindClub(tx, clubID)
		if err != nil {
			return err
		}
		name, ownerID, locations := club.Name, club.OwnerID, club.Locations
		if req.Name != nil {
			name = strings.TrimSpace(*req.Name)
			if name == "" {
				return errClubNameRequired
			}
		}
		if req.OwnerID != nil {
			if _, err := store.FindUserByID(tx, *req.OwnerID); err != nil {
				return errClubOwnerUnknown
			}
			ownerID = *req.OwnerID
			// The owner has to be able to see what they own.
			if err := a.addMember(tx, clubID, ownerID); err != nil {
				return err
			}
		}
		if req.Locations != nil {
			encoded, err := json.Marshal(normalizeLocationNames(*req.Locations))
			if err != nil {
				return err
			}
			locations = string(encoded)
		}
		if err := store.UpdateClub(tx, clubID, name, ownerID, locations); err != nil {
			return err
		}
		updated, err = store.FindClub(tx, clubID)
		if err != nil {
			return err
		}
		if err := a.syncClub(tx, clubID); err != nil {
			return err
		}
		if req.OwnerID != nil {
			return a.syncMembership(tx, ownerID)
		}
		return nil
	})
	switch {
	case errors.Is(err, errClubNameRequired):
		httpError(w, http.StatusBadRequest, "club name is required")
		return
	case errors.Is(err, errClubOwnerUnknown):
		httpError(w, http.StatusBadRequest, "unknown owner")
		return
	case err != nil:
		httpError(w, http.StatusConflict, "could not update the club")
		return
	}
	// A venue change alters which slots the club can see.
	a.publishSlotKeys()
	a.hub.notify()
	writeJSON(w, http.StatusOK, clubFromRecord(updated, u.ID))
}

// DELETE /api/clubs/{id} — only once it is empty, and it takes its polls and
// invites with it.
func (a *App) handleDeleteClub(w http.ResponseWriter, r *http.Request, u *User) {
	clubID, ok := a.clubFromPath(w, r)
	if !ok {
		return
	}
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		count, err := store.CountClubMembers(tx, clubID)
		if err != nil {
			return err
		}
		if count != 0 {
			return errClubNotEmpty
		}
		if err := store.AppendSync(tx, "club", strconv.FormatInt(clubID, 10), "delete", nil, 0, clubID); err != nil {
			return err
		}
		return store.DeleteClub(tx, clubID)
	})
	if errors.Is(err, errClubNotEmpty) {
		httpError(w, http.StatusConflict, "remove every member before deleting this club")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/clubs/{id}/members
func (a *App) handleListClubMembers(w http.ResponseWriter, r *http.Request, u *User, clubID int64) {
	records, err := store.ListClubMembers(a.orm.WithContext(r.Context()), clubID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	members := make([]syncMember, len(records))
	for i, record := range records {
		members[i] = syncMember{ID: record.ID, Name: record.Name, IsAdmin: record.IsAdmin}
	}
	writeJSON(w, http.StatusOK, members)
}

// POST /api/clubs/{id}/members — body {"user_id": n}
func (a *App) handleAddClubMember(w http.ResponseWriter, r *http.Request, u *User, clubID int64) {
	var req struct {
		UserID int64 `json:"user_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if _, err := store.FindUserByID(tx, req.UserID); err != nil {
			return errClubMemberUnknown
		}
		if err := a.addMember(tx, clubID, req.UserID); err != nil {
			return err
		}
		return a.syncMembership(tx, req.UserID)
	})
	if errors.Is(err, errClubMemberUnknown) {
		httpError(w, http.StatusBadRequest, "unknown user")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/clubs/{id}/members/{userID} — a user left with no club keeps
// their account; the app shows them an empty state until they are invited back.
func (a *App) handleRemoveClubMember(w http.ResponseWriter, r *http.Request, u *User, clubID int64) {
	userID, err := strconv.ParseInt(r.PathValue("userID"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid user id")
		return
	}
	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		club, err := store.FindClub(tx, clubID)
		if err != nil {
			return err
		}
		if club.OwnerID == userID {
			return errClubOwnerLocked
		}
		if err := store.RemoveClubMember(tx, clubID, userID); err != nil {
			return err
		}
		return a.syncMembership(tx, userID)
	})
	if errors.Is(err, errClubOwnerLocked) {
		httpError(w, http.StatusConflict, "hand the club over before removing its owner")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

var (
	errClubNotEmpty      = errors.New("club still has members")
	errClubOwnerUnknown  = errors.New("unknown club owner")
	errClubOwnerLocked   = errors.New("club owner cannot be removed")
	errClubNameRequired  = errors.New("club name is required")
	errClubMemberUnknown = errors.New("unknown club member")
)
