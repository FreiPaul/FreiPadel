package main

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"time"
)

var timeRe = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

type Settings struct {
	Weekdays    []int    `json:"weekdays"` // 0=Monday … 6=Sunday (matches the Python logic)
	TimeStart   string   `json:"time_start"`
	TimeEnd     string   `json:"time_end"`
	DaysAhead   int      `json:"days_ahead"`
	MinDuration int      `json:"min_duration"`
	Locations   []string `json:"locations"` // empty = all locations
}

func (a *App) loadSettings(userID int64) (Settings, error) {
	var s Settings
	var weekdaysJSON, locationsJSON string
	err := a.db.QueryRow(`SELECT weekdays, time_start, time_end, days_ahead, min_duration, locations
		FROM user_settings WHERE user_id = ?`, userID).
		Scan(&weekdaysJSON, &s.TimeStart, &s.TimeEnd, &s.DaysAhead, &s.MinDuration, &locationsJSON)
	if err == sql.ErrNoRows {
		// Older account without a settings row — use defaults.
		return Settings{Weekdays: []int{0, 1, 2, 3, 4}, TimeStart: "19:00", TimeEnd: "21:00",
			DaysAhead: 10, MinDuration: 60, Locations: []string{}}, nil
	}
	if err != nil {
		return s, err
	}
	if err := json.Unmarshal([]byte(weekdaysJSON), &s.Weekdays); err != nil {
		s.Weekdays = []int{0, 1, 2, 3, 4}
	}
	if err := json.Unmarshal([]byte(locationsJSON), &s.Locations); err != nil || s.Locations == nil {
		s.Locations = []string{}
	}
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
	weekdaysJSON, _ := json.Marshal(s.Weekdays)
	locationsJSON, _ := json.Marshal(s.Locations)
	_, err := a.db.Exec(`INSERT INTO user_settings (user_id, weekdays, time_start, time_end, days_ahead, min_duration, locations)
		VALUES (?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(user_id) DO UPDATE SET
			weekdays = excluded.weekdays, time_start = excluded.time_start,
			time_end = excluded.time_end, days_ahead = excluded.days_ahead,
			min_duration = excluded.min_duration, locations = excluded.locations`,
		u.ID, string(weekdaysJSON), s.TimeStart, s.TimeEnd, s.DaysAhead, s.MinDuration, string(locationsJSON))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusOK, s)
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

	rows, err := a.db.Query(`
		SELECT date, time, duration_minutes, location, source, currency,
		       MIN(price) AS min_price,
		       GROUP_CONCAT(court, '|') AS courts
		FROM slots
		WHERE date >= ? AND date <= ?
		  AND time >= ? AND time <= ?
		  AND duration_minutes >= ?
		  AND court NOT LIKE '%single%'
		  AND NOT (date = ? AND time <= ?) -- hide slots already in the past today
		GROUP BY date, time, duration_minutes, location, source, currency
		ORDER BY date, time, location, duration_minutes`,
		minDate, maxDate, s.TimeStart, s.TimeEnd, s.MinDuration, minDate, nowTime)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer rows.Close()

	wanted := map[int]bool{}
	for _, d := range s.Weekdays {
		wanted[d] = true
	}
	wantedLoc := map[string]bool{}
	for _, l := range s.Locations {
		wantedLoc[l] = true
	}

	groups := []SlotGroup{}
	for rows.Next() {
		var g SlotGroup
		var courts string
		if err := rows.Scan(&g.Date, &g.Time, &g.DurationMinutes, &g.Location, &g.Source, &g.Currency, &g.MinPrice, &courts); err != nil {
			continue
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
		g.Courts = splitCourts(courts)
		groups = append(groups, g)
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"slots":           groups,
		"last_fetched_at": getMeta(a.db, "last_fetched_at"),
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
	rows, err := a.db.Query(`SELECT DISTINCT location FROM slots ORDER BY location`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer rows.Close()
	locations := []string{}
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err == nil {
			locations = append(locations, l)
		}
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
	rows, err := a.db.Query(`SELECT id, name, is_admin FROM users ORDER BY name`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer rows.Close()
	type member struct {
		ID      int64  `json:"id"`
		Name    string `json:"name"`
		IsAdmin bool   `json:"is_admin"`
	}
	members := []member{}
	for rows.Next() {
		var m member
		var isAdmin int
		if err := rows.Scan(&m.ID, &m.Name, &isAdmin); err == nil {
			m.IsAdmin = isAdmin == 1
			members = append(members, m)
		}
	}
	writeJSON(w, http.StatusOK, members)
}

// --- Invites (admin) ---

type Invite struct {
	Token     string  `json:"token"`
	Kind      string  `json:"kind"` // 'single' | 'group'
	CreatedAt string  `json:"created_at"`
	UsedBy    *string `json:"used_by"` // single invites: name of the user who redeemed it
	UsedAt    *string `json:"used_at"`
	Disabled  bool    `json:"disabled"`
	Uses      int     `json:"uses"`
}

// POST /api/invites — body: {"kind": "single"|"group"} (defaults to single).
func (a *App) handleCreateInvite(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Kind string `json:"kind"`
	}
	// Body is optional; an empty body means a single-use invite.
	_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&req)
	if req.Kind == "" {
		req.Kind = "single"
	}
	if req.Kind != "single" && req.Kind != "group" {
		httpError(w, http.StatusBadRequest, "kind must be 'single' or 'group'")
		return
	}
	token := randomToken(16)
	_, err := a.db.Exec(`INSERT INTO invites (token, created_by, kind) VALUES (?, ?, ?)`, token, u.ID, req.Kind)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	writeJSON(w, http.StatusCreated, map[string]string{"token": token, "kind": req.Kind})
}

// GET /api/invites
func (a *App) handleListInvites(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(`
		SELECT i.token, i.kind, i.created_at, i.used_at, usr.name, i.disabled, i.uses
		FROM invites i LEFT JOIN users usr ON usr.id = i.used_by
		ORDER BY i.created_at DESC`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer rows.Close()
	invites := []Invite{}
	for rows.Next() {
		var inv Invite
		var disabled int
		if err := rows.Scan(&inv.Token, &inv.Kind, &inv.CreatedAt, &inv.UsedAt, &inv.UsedBy, &disabled, &inv.Uses); err == nil {
			inv.Disabled = disabled == 1
			invites = append(invites, inv)
		}
	}
	writeJSON(w, http.StatusOK, invites)
}

// POST /api/invites/{token}/disable — stops the link from accepting registrations.
func (a *App) handleDisableInvite(w http.ResponseWriter, r *http.Request, u *User) {
	token := r.PathValue("token")
	res, err := a.db.Exec(`UPDATE invites SET disabled = 1 WHERE token = ?`, token)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpError(w, http.StatusNotFound, "invite not found")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/invites/{token} — group invites can always be removed (deleting
// also stops them working); single invites only while unused.
func (a *App) handleDeleteInvite(w http.ResponseWriter, r *http.Request, u *User) {
	token := r.PathValue("token")
	res, err := a.db.Exec(`DELETE FROM invites WHERE token = ? AND (kind = 'group' OR used_by IS NULL)`, token)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		httpError(w, http.StatusNotFound, "invite not found or already used")
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// GET /api/invites/{token}/check — public; lets the register page validate a link.
func (a *App) handleCheckInvite(w http.ResponseWriter, r *http.Request) {
	token := r.PathValue("token")
	var used sql.NullInt64
	var kind string
	var disabled int
	err := a.db.QueryRow(`SELECT used_by, kind, disabled FROM invites WHERE token = ?`, token).
		Scan(&used, &kind, &disabled)
	if err == sql.ErrNoRows {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "unknown"})
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if disabled == 1 {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "disabled"})
		return
	}
	if kind != "group" && used.Valid {
		writeJSON(w, http.StatusOK, map[string]any{"valid": false, "reason": "used"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"valid": true})
}
