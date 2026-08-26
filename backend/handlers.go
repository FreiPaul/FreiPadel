package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"maps"
	"net/http"
	"regexp"
	"strconv"
	"time"

	"freipadel/internal/store"
	"freipadel/scraper"

	"gorm.io/gorm"
)

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type Settings struct {
	Weekdays      []int           `json:"weekdays"` // 0=Monday … 6=Sunday (matches the Python logic)
	TimeStart     string          `json:"time_start"`
	TimeEnd       string          `json:"time_end"`
	DaysAhead     int             `json:"days_ahead"`
	MinDuration   int             `json:"min_duration"`
	Locations     []string        `json:"locations"`     // empty = all locations
	Notifications map[string]bool `json:"notifications"` // notification key -> enabled
}

// notificationDefaults is the source of truth for which notification keys exist
// and their default value. Add a key here to introduce a new notification type;
// no schema change or backfill is needed — mergeNotifications fills it in for
// every user on read.
var notificationDefaults = map[string]bool{
	"slot_booked":  false,
	"poll_created": false,
}

// mergeNotifications overlays a user's stored preferences on top of the
// defaults, dropping any unknown/stale keys. This is what makes the setting
// extendible: the stored JSON is opaque, but the known keys live in code.
func mergeNotifications(stored map[string]bool) map[string]bool {
	out := make(map[string]bool, len(notificationDefaults))
	maps.Copy(out, notificationDefaults)
	for k, v := range stored {
		if _, known := notificationDefaults[k]; known {
			out[k] = v
		}
	}
	return out
}

func (a *App) loadSettings(userID int64) (Settings, error) {
	var s Settings
	record, err := store.FindSettings(a.orm, userID)
	if store.IsNotFound(err) {
		// Older account without a settings row — use defaults.
		return Settings{Weekdays: []int{0, 1, 2, 3, 4}, TimeStart: "19:00", TimeEnd: "21:00",
			DaysAhead: 10, MinDuration: 60, Locations: []string{}, Notifications: mergeNotifications(nil)}, nil
	}
	if err != nil {
		return s, err
	}
	weekdaysJSON, locationsJSON, notificationsJSON := record.Weekdays, record.Locations, record.Notifications
	s.TimeStart, s.TimeEnd = record.TimeStart, record.TimeEnd
	s.DaysAhead, s.MinDuration = record.DaysAhead, record.MinDuration
	if err := json.Unmarshal([]byte(weekdaysJSON), &s.Weekdays); err != nil {
		s.Weekdays = []int{0, 1, 2, 3, 4}
	}
	if err := json.Unmarshal([]byte(locationsJSON), &s.Locations); err != nil || s.Locations == nil {
		s.Locations = []string{}
	}
	s.Locations = normalizeLocationNames(s.Locations)
	var stored map[string]bool
	_ = json.Unmarshal([]byte(notificationsJSON), &stored) // nil on error -> pure defaults
	s.Notifications = mergeNotifications(stored)
	return s, nil
}

// GET /api/settings
func (a *App) handleGetSettings(w http.ResponseWriter, r *http.Request, u *User) {
	s, err := a.loadSettings(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, s)
}

// PUT /api/settings
func (a *App) handlePutSettings(w http.ResponseWriter, r *http.Request, u *User) {
	var s Settings
	if !readJSON(w, r, &s) {
		return
	}
	if len(s.Weekdays) == 0 {
		httpError(w, http.StatusBadRequest, "select at least one weekday")
		return
	}
	seen := map[int]bool{}
	for _, d := range s.Weekdays {
		if d < 0 || d > 6 || seen[d] {
			httpError(w, http.StatusBadRequest, "invalid weekdays")
			return
		}
		seen[d] = true
	}
	if !timeRe.MatchString(s.TimeStart) || !timeRe.MatchString(s.TimeEnd) || s.TimeStart >= s.TimeEnd {
		httpError(w, http.StatusBadRequest, "invalid time window")
		return
	}
	if s.DaysAhead < 1 || s.DaysAhead > 21 {
		httpError(w, http.StatusBadRequest, "days ahead must be between 1 and 21")
		return
	}
	if s.MinDuration < 30 || s.MinDuration > 240 {
		httpError(w, http.StatusBadRequest, "minimum duration must be between 30 and 240 minutes")
		return
	}
	if len(s.Locations) > 50 {
		httpError(w, http.StatusBadRequest, "too many locations")
		return
	}
	if s.Locations == nil {
		s.Locations = []string{}
	}
	s.Locations = normalizeLocationNames(s.Locations)
	// Drop unknown keys and fill in defaults for any the client omitted, so the
	// stored map only ever contains valid keys.
	s.Notifications = mergeNotifications(s.Notifications)
	weekdaysJSON, _ := json.Marshal(s.Weekdays)
	locationsJSON, _ := json.Marshal(s.Locations)
	notificationsJSON, _ := json.Marshal(s.Notifications)
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.UpsertSettings(tx, store.SettingsRecord{
			UserID: u.ID, Weekdays: string(weekdaysJSON), TimeStart: s.TimeStart, TimeEnd: s.TimeEnd,
			DaysAhead: s.DaysAhead, MinDuration: s.MinDuration,
			Locations: string(locationsJSON), Notifications: string(notificationsJSON),
		}); err != nil {
			return err
		}
		payload, _ := json.Marshal(s)
		return store.AppendSync(tx, "settings", strconv.FormatInt(u.ID, 10), "upsert", payload, u.ID, 0)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, s)
}

func normalizeLocationNames(locations []string) []string {
	out := make([]string, 0, len(locations))
	seen := make(map[string]bool, len(locations))
	for _, location := range locations {
		location = scraper.NormalizeLocationName(location)
		if location != "" && !seen[location] {
			seen[location] = true
			out = append(out, location)
		}
	}
	return out
}

// SlotGroup is one pollable option: all free courts at the same
// date/time/duration/location collapsed into a single row.
type SlotGroup struct {
	Date            string   `json:"date"`
	Weekday         int      `json:"weekday"` // 0=Monday … 6=Sunday
	Time            string   `json:"time"`
	DurationMinutes int      `json:"duration_minutes"`
	Location        string   `json:"location"`
	Source          string   `json:"source"`
	Courts          []string `json:"courts"`
	MinPrice        float64  `json:"min_price"`
	Currency        string   `json:"currency"`
}

// GET /api/slots — available slots filtered by the current user's settings.
func (a *App) handleGetSlots(w http.ResponseWriter, r *http.Request, u *User) {
	if u.ClubID() == 0 {
		httpError(w, http.StatusConflict, "you are not in a club yet")
		return
	}
	s, err := a.loadSettings(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	clubLocations, err := a.clubLocations(r, u.ClubID())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	now := time.Now().In(a.tz)
	minDate := now.Format("2006-01-02")
	maxDate := now.AddDate(0, 0, s.DaysAhead-1).Format("2006-01-02")
	nowTime := now.Format("15:04")

	records, err := store.ListSlotGroups(a.orm.WithContext(r.Context()), store.SlotFilter{
		MinDate: minDate, MaxDate: maxDate, TimeStart: s.TimeStart, TimeEnd: s.TimeEnd,
		MinDuration: s.MinDuration, NowTime: nowTime,
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	wanted := map[int]bool{}
	for _, d := range s.Weekdays {
		wanted[d] = true
	}
	// The club decides which venues exist here; the user's own filter narrows
	// that further. Either being empty means "no constraint from that side".
	wantedLoc := map[string]bool{}
	for _, l := range intersectLocations(clubLocations, s.Locations) {
		wantedLoc[l] = true
	}

	groups := []SlotGroup{}
	for _, record := range records {
		g := SlotGroup{
			Date: record.Date, Time: record.Time, DurationMinutes: record.DurationMinutes,
			Location: record.Location, Source: record.Source, Currency: record.Currency, MinPrice: record.MinPrice,
		}
		d, err := time.ParseInLocation("2006-01-02", g.Date, a.tz)
		if err != nil {
			continue
		}
		g.Weekday = (int(d.Weekday()) + 6) % 7 // Go: Sunday=0 → ours: Monday=0
		if !wanted[g.Weekday] {
			continue
		}
		if len(wantedLoc) > 0 && !wantedLoc[g.Location] {
			continue
		}
		g.Courts = splitCourts(record.Courts)
		groups = append(groups, g)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slots":           groups,
		"last_fetched_at": a.store.GetMeta("last_fetched_at"),
		"scraping":        a.isScraping(),
	})
}

func splitCourts(s string) []string {
	if s == "" {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '|' {
			c := s[start:i]
			if c != "" && !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
			start = i + 1
		}
	}
	return out
}

// GET /api/locations — the venues the active club can see, for the filter UI.
// ?all=1 returns every scraped venue, which is what the club editor picks from.
func (a *App) handleListLocations(w http.ResponseWriter, r *http.Request, u *User) {
	scraped, err := store.ListLocations(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if r.URL.Query().Get("all") == "1" {
		writeJSON(w, http.StatusOK, scraped)
		return
	}
	clubLocations, err := a.clubLocations(r, u.ClubID())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, intersectLocations(clubLocations, scraped))
}

// clubLocations reads a club's venue list. An empty result means the club is
// not restricted to any subset.
func (a *App) clubLocations(r *http.Request, clubID int64) ([]string, error) {
	if clubID == 0 {
		return []string{}, nil
	}
	club, err := store.FindClub(a.orm.WithContext(r.Context()), clubID)
	if err != nil {
		return nil, err
	}
	return parseLocations(club.Locations), nil
}

// intersectLocations narrows one venue list by another, treating an empty list
// as "everything" — the convention user_settings.locations already uses.
func intersectLocations(a, b []string) []string {
	if len(a) == 0 {
		return b
	}
	if len(b) == 0 {
		return a
	}
	allowed := make(map[string]bool, len(b))
	for _, location := range b {
		allowed[location] = true
	}
	out := make([]string, 0, len(a))
	for _, location := range a {
		if allowed[location] {
			out = append(out, location)
		}
	}
	return out
}

// POST /api/slots/refresh — trigger a scrape unless one just ran or is running.
func (a *App) handleRefreshSlots(w http.ResponseWriter, r *http.Request, u *User) {
	started := a.triggerScrape()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": started, "scraping": true})
}

// GET /api/users — the people who share a club with the caller (for showing
// who voted). ?all=1 gives an admin the whole directory, to staff clubs from.
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request, u *User) {
	db := a.orm.WithContext(r.Context())
	type member struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	members := []member{}

	if u.IsAdmin && r.URL.Query().Get("all") == "1" {
		records, err := store.ListMembers(db)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		for _, record := range records {
			members = append(members, member{ID: record.ID, Name: record.Name, IsAdmin: record.IsAdmin})
		}
		writeJSON(w, http.StatusOK, members)
		return
	}

	records, err := store.ListVisibleMembers(db, u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	for _, record := range records {
		members = append(members, member{ID: record.ID, Name: record.Name, IsAdmin: record.IsAdmin})
	}
	writeJSON(w, http.StatusOK, members)
}

// --- Invites (admin) ---

type Invite struct {
	Token     string  `json:"token"`
	Kind      string  `json:"kind"` // 'single' | 'group' | 'email'
	ClubID    int64   `json:"club_id"`
	ClubName  string  `json:"club_name"`
	Email     *string `json:"email"`
	CreatedAt string  `json:"created_at"`
	UsedBy    *string `json:"used_by"` // single invites: name of the user who redeemed it
	UsedAt    *string `json:"used_at"`
	Disabled  bool    `json:"disabled"`
	Uses      int     `json:"uses"`
}

func inviteFromRecord(record store.InviteRecord) Invite {
	return Invite{
		Token: record.Token, Kind: record.Kind,
		ClubID: record.ClubID, ClubName: record.ClubName, Email: record.Email,
		CreatedAt: record.CreatedAt, UsedBy: record.UsedByName, UsedAt: record.UsedAt,
		Disabled: record.Disabled, Uses: record.Uses,
	}
}

// POST /api/invites — body: {"kind": "single"|"group"|"email", "club_id": n}.
// Every invite belongs to exactly one club; it defaults to the caller's active
// one, and creating it requires managing that club.
func (a *App) handleCreateInvite(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Kind   string `json:"kind"`
		Email  string `json:"email"`
		Origin string `json:"origin"`
		ClubID int64  `json:"club_id"`
	}
	// Body is optional; an empty body means a single-use invite.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	if req.Kind == "" {
		req.Kind = "single"
	}
	if req.ClubID == 0 {
		req.ClubID = u.ClubID()
	}
	if req.ClubID == 0 {
		httpError(w, http.StatusBadRequest, "an invite needs a club")
		return
	}
	if _, err := store.FindClub(a.orm.WithContext(r.Context()), req.ClubID); err != nil {
		httpError(w, http.StatusNotFound, "club not found")
		return
	}
	if !a.managesClub(r, u, req.ClubID) {
		httpError(w, http.StatusForbidden, "not allowed to manage this club")
		return
	}
	req.Origin = a.linkOrigin(req.Origin)
	if req.Origin == "" {
		req.Origin = "https://freipadel.freipaul.com"
	}
	if req.Kind != "single" && req.Kind != "group" && req.Kind != "email" {
		httpError(w, http.StatusBadRequest, "kind must be 'single' or 'group' or 'email'")
		return
	}

	if req.Kind == "email" && !a.emailer.Configured() {
		httpError(w, http.StatusBadRequest, "the emailer is disabled in this deployment")
		return
	}

	if req.Kind == "email" && req.Email == "" {
		httpError(w, http.StatusBadRequest, "email invite must include an email address")
		return
	}
	if req.Kind == "email" {
		var err error
		req.Email, err = normalizeEmail(req.Email)
		if err != nil {
			httpError(w, http.StatusBadRequest, "invalid email address")
			return
		}
	}

	// An email invite is addressed to one person, so refuse to issue a second one
	// for an address that already has an account or an outstanding invite. Single
	// and group invites carry no address, so there is nothing to collide on.
	if req.Kind == "email" {
		exists, err := store.UserOrInviteEmailExists(a.orm.WithContext(r.Context()), req.Email)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		if exists {
			httpError(w, http.StatusConflict, "user/invite with email already exists")
			return
		}
	}

	// Only email invites carry an address; the rest store NULL
	var inviteEmail *string
	if req.Kind == "email" {
		inviteEmail = &req.Email
	}

	token := randomToken(16)
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.CreateInvite(tx, token, u.ID, req.ClubID, req.Kind, inviteEmail); err != nil {
			return err
		}
		inv, err := store.FindInvite(tx, token)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(inviteFromRecord(inv))
		return store.AppendSync(tx, "invite", token, "upsert", payload, visibleToAdmins, req.ClubID)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if req.Kind == "email" {
		// send email with invite link
		body := fmt.Sprintf(
			`<p>You've been invited to FreiPadel.</p>
<p><a href="%s/register?token=%s">Accept invitation</a></p>`,
			template.HTMLEscapeString(req.Origin),
			template.HTMLEscapeString(token),
		)
		a.emailer.Send(req.Email, "You've been invited to FreiPadel", body)
	}
	a.hub.notify()
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "kind": req.Kind})
}

// GET /api/invites — every invite for an admin, a club owner's own otherwise.
func (a *App) handleListInvites(w http.ResponseWriter, r *http.Request, u *User) {
	scope, err := a.inviteScope(r, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	records, err := store.ListInvites(a.orm.WithContext(r.Context()), scope)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	invites := make([]Invite, len(records))
	for i, record := range records {
		invites[i] = inviteFromRecord(record)
	}
	writeJSON(w, http.StatusOK, invites)
}

// POST /api/invites/{token}/disable — stops the link from accepting registrations.
func (a *App) handleDisableInvite(w http.ResponseWriter, r *http.Request, u *User) {
	token := r.PathValue("token")
	clubID, ok := a.authorizeInvite(w, r, u, token)
	if !ok {
		return
	}
	notFound := false
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		affected, err := store.DisableInvite(tx, token)
		if err != nil {
			return err
		}
		if affected == 0 {
			notFound = true
			return gorm.ErrRecordNotFound
		}
		inv, err := store.FindInvite(tx, token)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(inviteFromRecord(inv))
		return store.AppendSync(tx, "invite", token, "upsert", payload, visibleToAdmins, clubID)
	})
	if notFound {
		httpError(w, http.StatusNotFound, "invite not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/invites/{token} — group invites can always be removed (deleting
// also stops them working); single invites only while unused.
func (a *App) handleDeleteInvite(w http.ResponseWriter, r *http.Request, u *User) {
	token := r.PathValue("token")
	clubID, ok := a.authorizeInvite(w, r, u, token)
	if !ok {
		return
	}
	notFound := false
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		affected, err := store.DeleteInvite(tx, token)
		if err != nil {
			return err
		}
		if affected == 0 {
			notFound = true
			return gorm.ErrRecordNotFound
		}
		return store.AppendSync(tx, "invite", token, "delete", nil, visibleToAdmins, clubID)
	})
	if notFound {
		httpError(w, http.StatusNotFound, "invite not found or already used")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/invites/{token}/check — public; lets the register page validate a link.
func (a *App) handleCheckInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	invite, err := store.FindInvite(a.orm.WithContext(r.Context()), token)
	if store.IsNotFound(err) {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "unknown"})
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if invite.Disabled {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "disabled"})
		return
	}
	if invite.Kind != "group" && invite.UsedByID != nil {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "used"})
		return
	}
	email := ""
	if invite.Email != nil {
		email = *invite.Email
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"valid": true, "email": email,
		"club_id": invite.ClubID, "club_name": invite.ClubName,
	})
}

// inviteScope is the set of clubs whose invites the caller may see: nil for a
// global admin (meaning all), otherwise the clubs they own.
func (a *App) inviteScope(r *http.Request, u *User) ([]int64, error) {
	if u.IsAdmin {
		return nil, nil
	}
	owned, err := store.ListOwnedClubIDs(a.orm.WithContext(r.Context()), u.ID)
	if err != nil {
		return nil, err
	}
	if owned == nil {
		owned = []int64{}
	}
	return owned, nil
}

// authorizeInvite resolves an invite's club and checks the caller manages it.
func (a *App) authorizeInvite(w http.ResponseWriter, r *http.Request, u *User, token string) (int64, bool) {
	invite, err := store.FindInvite(a.orm.WithContext(r.Context()), token)
	if store.IsNotFound(err) {
		httpError(w, http.StatusNotFound, "invite not found")
		return 0, false
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return 0, false
	}
	if !a.managesClub(r, u, invite.ClubID) {
		httpError(w, http.StatusForbidden, "not allowed to manage this club")
		return 0, false
	}
	return invite.ClubID, true
}

// inviteProblem returns the reason an invite cannot be redeemed, or "".
// Registration and the logged-in accept path share it so they cannot drift.
func inviteProblem(invite store.InviteRecord, email string) string {
	switch {
	case invite.Disabled:
		return "this invite link has been disabled"
	case invite.Kind != "group" && invite.UsedByID != nil:
		return "this invite link has already been used"
	case invite.Kind == "email" && (invite.Email == nil || email != *invite.Email):
		return "this invite belongs to another email"
	case invite.ClubID == 0:
		return "this invite is not attached to a club"
	}
	return ""
}

// POST /api/invites/{token}/accept — join the invite's club with the account
// already signed in, instead of registering a new one.
func (a *App) handleAcceptInvite(w http.ResponseWriter, r *http.Request, u *User) {
	token := r.PathValue("token")
	var (
		alreadyMember  bool
		clubID         int64
		responseStatus int
		problem        string
	)
	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		invite, err := store.FindInvite(tx, token)
		if store.IsNotFound(err) {
			responseStatus, problem = http.StatusForbidden, "invalid invite link"
			return errInviteRejected
		}
		if err != nil {
			return err
		}
		if reason := inviteProblem(invite, u.Email); reason != "" {
			responseStatus, problem = http.StatusForbidden, reason
			return errInviteRejected
		}
		clubID = invite.ClubID

		alreadyMember, err = store.IsClubMember(tx, clubID, u.ID)
		if err != nil {
			return err
		}
		if err := a.joinClub(tx, u.ID, clubID); err != nil {
			return err
		}
		if err := a.syncMembership(tx, u.ID); err != nil {
			return err
		}
		// Joining a club you are already in must not burn a one-time link.
		if alreadyMember {
			return nil
		}
		if err := store.RedeemInvite(tx, token, u.ID); err != nil {
			return err
		}
		redeemed, err := store.FindInvite(tx, token)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(inviteFromRecord(redeemed))
		return store.AppendSync(tx, "invite", token, "upsert", payload, visibleToAdmins, clubID)
	})
	if responseStatus != 0 {
		httpError(w, responseStatus, problem)
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]any{
		"already_member": alreadyMember,
		"club_id":        clubID,
	})
}

var errInviteRejected = errors.New("invite rejected")
