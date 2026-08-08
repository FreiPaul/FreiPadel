package main

import (
	"encoding/json"
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
		return store.AppendSync(tx, "settings", strconv.FormatInt(u.ID, 10), "upsert", payload, u.ID)
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
	s, err := a.loadSettings(u.ID)
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
	wantedLoc := map[string]bool{}
	for _, l := range s.Locations {
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

// GET /api/locations — all locations currently present in the slot cache,
// for the location filter UI.
func (a *App) handleListLocations(w http.ResponseWriter, r *http.Request, u *User) {
	locations, err := store.ListLocations(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, locations)
}

// POST /api/slots/refresh — trigger a scrape unless one just ran or is running.
func (a *App) handleRefreshSlots(w http.ResponseWriter, r *http.Request, u *User) {
	started := a.triggerScrape()
	writeJSON(w, http.StatusAccepted, map[string]bool{"started": started, "scraping": true})
}

// GET /api/users — group members (for showing who voted).
func (a *App) handleListUsers(w http.ResponseWriter, r *http.Request, u *User) {
	records, err := store.ListMembers(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	type member struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	members := []member{}
	for _, record := range records {
		members = append(members, member{ID: record.ID, Name: record.Name, IsAdmin: record.IsAdmin})
	}
	writeJSON(w, http.StatusOK, members)
}

// --- Invites (admin) ---

type Invite struct {
	Token     string  `json:"token"`
	Kind      string  `json:"kind"` // 'single' | 'group' | 'email'
	Email     *string `json:"email"`
	CreatedAt string  `json:"created_at"`
	UsedBy    *string `json:"used_by"` // single invites: name of the user who redeemed it
	UsedAt    *string `json:"used_at"`
	Disabled  bool    `json:"disabled"`
	Uses      int     `json:"uses"`
}

func inviteFromRecord(record store.InviteRecord) Invite {
	return Invite{
		Token: record.Token, Kind: record.Kind, Email: record.Email,
		CreatedAt: record.CreatedAt, UsedBy: record.UsedByName, UsedAt: record.UsedAt,
		Disabled: record.Disabled, Uses: record.Uses,
	}
}

// POST /api/invites — body: {"kind": "single"|"group"} (defaults to single).
func (a *App) handleCreateInvite(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Kind   string `json:"kind"`
		Email  string `json:"email"`
		Origin string `json:"origin"`
	}
	// Body is optional; an empty body means a single-use invite.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	if req.Kind == "" {
		req.Kind = "single"
	}
	if req.Origin == "" {
		req.Origin = "https://freipadel.freipaul.com/"
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
		if err := store.CreateInvite(tx, token, u.ID, req.Kind, inviteEmail); err != nil {
			return err
		}
		inv, err := store.FindInvite(tx, token)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(inviteFromRecord(inv))
		return store.AppendSync(tx, "invite", token, "upsert", payload, visibleToAdmins)
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

// GET /api/invites
func (a *App) handleListInvites(w http.ResponseWriter, r *http.Request, u *User) {
	records, err := store.ListInvites(a.orm.WithContext(r.Context()))
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
		return store.AppendSync(tx, "invite", token, "upsert", payload, visibleToAdmins)
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
		return store.AppendSync(tx, "invite", token, "delete", nil, visibleToAdmins)
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
	writeJSON(w, http.StatusOK, map[string]any{"valid": true, "email": email})
}
