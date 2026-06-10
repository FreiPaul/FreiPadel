package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
)

func slotKey(date, tm string, duration int, location string) string {
	return fmt.Sprintf("%s|%s|%d|%s", date, tm, duration, location)
}

type Voter struct {
	UserID int64  `json:"user_id"`
	Name   string `json:"name"`
	Vote   bool   `json:"vote"`
}

type PollSlot struct {
	ID              int64   `json:"id"`
	Date            string  `json:"date"`
	Time            string  `json:"time"`
	DurationMinutes int     `json:"duration_minutes"`
	Location        string  `json:"location"`
	Court           string  `json:"court"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
	Votes           []Voter `json:"votes"`
	YesCount        int     `json:"yes_count"`
	NoCount         int     `json:"no_count"`
	MyVote          *bool   `json:"my_vote"`
	// Compared against the latest scrape: false when no court at this
	// date/time/duration/location is free anymore.
	Available bool `json:"available"`
	// True when the slot's start time has passed.
	Expired bool `json:"expired"`
}

type Poll struct {
	ID            int64      `json:"id"`
	Title         string     `json:"title"`
	CreatorID     int64      `json:"creator_id"`
	CreatorName   string     `json:"creator_name"`
	Status        string     `json:"status"`
	WinningSlotID *int64     `json:"winning_slot_id"`
	CreatedAt     string     `json:"created_at"`
	ClosedAt      *string    `json:"closed_at"`
	Slots         []PollSlot `json:"slots"`
}

// GET /api/polls — all polls with full details (small group, cheap enough).
func (a *App) handleListPolls(w http.ResponseWriter, r *http.Request, u *User) {
	rows, err := a.db.Query(`
		SELECT p.id, p.title, p.creator_id, usr.name, p.status, p.winning_slot_id, p.created_at, p.closed_at
		FROM polls p JOIN users usr ON usr.id = p.creator_id
		ORDER BY p.status = 'active' DESC, p.created_at DESC`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer rows.Close()

	polls := []*Poll{}
	byID := map[int64]*Poll{}
	for rows.Next() {
		var p Poll
		if err := rows.Scan(&p.ID, &p.Title, &p.CreatorID, &p.CreatorName, &p.Status,
			&p.WinningSlotID, &p.CreatedAt, &p.ClosedAt); err != nil {
			continue
		}
		p.Slots = []PollSlot{}
		polls = append(polls, &p)
		byID[p.ID] = &p
	}
	if len(polls) == 0 {
		writeJSON(w, http.StatusOK, polls)
		return
	}

	// Current availability per (date, time, duration, location), so polls can
	// flag slots that were booked away since the poll was created. Must be
	// fully read BEFORE the next query: the pool has a single connection, and
	// a second query while a result set is open would deadlock.
	availSet := map[string]bool{}
	availRows, err := a.db.Query(`SELECT DISTINCT date, time, duration_minutes, location FROM slots`)
	if err == nil {
		for availRows.Next() {
			var date, tm, location string
			var dur int
			if err := availRows.Scan(&date, &tm, &dur, &location); err == nil {
				availSet[slotKey(date, tm, dur, location)] = true
			}
		}
		availRows.Close()
	}
	now := time.Now().In(a.tz)
	today, nowTime := now.Format("2006-01-02"), now.Format("15:04")

	slotRows, err := a.db.Query(`
		SELECT id, poll_id, date, time, duration_minutes, location, court, price, currency
		FROM poll_slots ORDER BY date, time, location, duration_minutes`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer slotRows.Close()

	slotByID := map[int64]*PollSlot{}
	for slotRows.Next() {
		var s PollSlot
		var pollID int64
		if err := slotRows.Scan(&s.ID, &pollID, &s.Date, &s.Time, &s.DurationMinutes,
			&s.Location, &s.Court, &s.Price, &s.Currency); err != nil {
			continue
		}
		s.Votes = []Voter{}
		s.Expired = s.Date < today || (s.Date == today && s.Time <= nowTime)
		s.Available = !s.Expired && availSet[slotKey(s.Date, s.Time, s.DurationMinutes, s.Location)]
		if strings.Contains(strings.ToLower(s.Court), "single") {
			s.Available = false
		}
		if p, ok := byID[pollID]; ok {
			p.Slots = append(p.Slots, s)
		}
	}
	// Re-index into the slices (appends above copied the structs).
	for _, p := range byID {
		for i := range p.Slots {
			slotByID[p.Slots[i].ID] = &p.Slots[i]
		}
	}

	voteRows, err := a.db.Query(`
		SELECT v.poll_slot_id, v.user_id, usr.name, v.vote
		FROM votes v JOIN users usr ON usr.id = v.user_id
		ORDER BY usr.name`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer voteRows.Close()
	for voteRows.Next() {
		var slotID int64
		var voter Voter
		var vote int
		if err := voteRows.Scan(&slotID, &voter.UserID, &voter.Name, &vote); err != nil {
			continue
		}
		voter.Vote = vote == 1
		s, ok := slotByID[slotID]
		if !ok {
			continue
		}
		s.Votes = append(s.Votes, voter)
		if voter.Vote {
			s.YesCount++
		} else {
			s.NoCount++
		}
		if voter.UserID == u.ID {
			v := voter.Vote
			s.MyVote = &v
		}
	}

	writeJSON(w, http.StatusOK, polls)
}

// POST /api/polls — start a new slot poll from a selection of slot groups.
func (a *App) handleCreatePoll(w http.ResponseWriter, r *http.Request, u *User) {
	var req struct {
		Title string `json:"title"`
		Slots []struct {
			Date            string   `json:"date"`
			Time            string   `json:"time"`
			DurationMinutes int      `json:"duration_minutes"`
			Location        string   `json:"location"`
			Courts          []string `json:"courts"`
			MinPrice        float64  `json:"min_price"`
			Currency        string   `json:"currency"`
		} `json:"slots"`
	}
	if !readJSON(w, r, &req) {
		return
	}
	req.Title = strings.TrimSpace(req.Title)
	if req.Title == "" {
		req.Title = "Padel?"
	}
	if len(req.Slots) == 0 {
		httpError(w, http.StatusBadRequest, "select at least one slot")
		return
	}
	if len(req.Slots) > 30 {
		httpError(w, http.StatusBadRequest, "too many slots (max 30)")
		return
	}
	for _, s := range req.Slots {
		if !dateRe.MatchString(s.Date) || !timeRe.MatchString(s.Time) || s.DurationMinutes <= 0 {
			httpError(w, http.StatusBadRequest, "invalid slot data")
			return
		}
	}

	tx, err := a.db.Begin()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer tx.Rollback()

	res, err := tx.Exec(`INSERT INTO polls (creator_id, title) VALUES (?, ?)`, u.ID, req.Title)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	pollID, _ := res.LastInsertId()

	for _, s := range req.Slots {
		currency := s.Currency
		if currency == "" {
			currency = "EUR"
		}
		_, err := tx.Exec(`INSERT INTO poll_slots (poll_id, date, time, duration_minutes, location, court, price, currency)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
			pollID, s.Date, s.Time, s.DurationMinutes, s.Location, strings.Join(s.Courts, ", "), s.MinPrice, currency)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
	}
	syncP, err := loadSyncPoll(tx, pollID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if err := appendSync(tx, "poll", strconv.FormatInt(pollID, 10), "upsert", syncP, 0); err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if err := tx.Commit(); err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusCreated, map[string]int64{"id": pollID})

	// notify admin via telegram
	a.telegramSender.SendMsg(a.scrapeCfg.Telegram.AdminChatID, fmt.Sprint("New FreiPadel Poll from ", u.Name))
}

// POST /api/polls/{id}/vote — cast or change a yes/no vote on one poll slot.
func (a *App) handleVote(w http.ResponseWriter, r *http.Request, u *User) {
	pollID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid poll id")
		return
	}
	var req struct {
		PollSlotID int64 `json:"poll_slot_id"`
		Vote       *bool `json:"vote"` // null clears the vote
	}
	if !readJSON(w, r, &req) {
		return
	}

	var status string
	var slotPollID int64
	err = a.db.QueryRow(`
		SELECT p.status, ps.poll_id FROM poll_slots ps JOIN polls p ON p.id = ps.poll_id
		WHERE ps.id = ?`, req.PollSlotID).Scan(&status, &slotPollID)
	if err == sql.ErrNoRows || slotPollID != pollID {
		httpError(w, http.StatusNotFound, "slot not found in this poll")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if status != "active" {
		httpError(w, http.StatusConflict, "poll is closed")
		return
	}

	tx, err := a.db.Begin()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer tx.Rollback()
	voteEntityID := fmt.Sprintf("%d|%d", req.PollSlotID, u.ID)
	if req.Vote == nil {
		_, err = tx.Exec(`DELETE FROM votes WHERE poll_slot_id = ? AND user_id = ?`, req.PollSlotID, u.ID)
		if err == nil {
			err = appendSync(tx, "vote", voteEntityID, "delete", nil, 0)
		}
	} else {
		v := 0
		if *req.Vote {
			v = 1
		}
		_, err = tx.Exec(`INSERT INTO votes (poll_slot_id, user_id, vote) VALUES (?, ?, ?)
			ON CONFLICT(poll_slot_id, user_id) DO UPDATE SET vote = excluded.vote, updated_at = datetime('now')`,
			req.PollSlotID, u.ID, v)
		if err == nil {
			err = appendSync(tx, "vote", voteEntityID, "upsert",
				syncVote{PollSlotID: req.PollSlotID, UserID: u.ID, Name: u.Name, Vote: *req.Vote}, 0)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// POST /api/polls/{id}/close — poll owner (or admin) closes, optionally picking a winner.
func (a *App) handleClosePoll(w http.ResponseWriter, r *http.Request, u *User) {
	pollID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid poll id")
		return
	}
	var req struct {
		WinningSlotID *int64 `json:"winning_slot_id"`
	}
	if !readJSON(w, r, &req) {
		return
	}

	var creatorID int64
	var status string
	err = a.db.QueryRow(`SELECT creator_id, status FROM polls WHERE id = ?`, pollID).Scan(&creatorID, &status)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "poll not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if creatorID != u.ID && !u.IsAdmin {
		httpError(w, http.StatusForbidden, "only the poll creator can close it")
		return
	}
	if status != "active" {
		httpError(w, http.StatusConflict, "poll is already closed")
		return
	}
	if req.WinningSlotID != nil {
		var n int
		_ = a.db.QueryRow(`SELECT COUNT(*) FROM poll_slots WHERE id = ? AND poll_id = ?`,
			*req.WinningSlotID, pollID).Scan(&n)
		if n == 0 {
			httpError(w, http.StatusBadRequest, "winning slot does not belong to this poll")
			return
		}
	}

	tx, err := a.db.Begin()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`UPDATE polls SET status = 'closed', winning_slot_id = ?, closed_at = datetime('now') WHERE id = ?`,
		req.WinningSlotID, pollID)
	if err == nil {
		var p *syncPoll
		if p, err = loadSyncPoll(tx, pollID); err == nil {
			err = appendSync(tx, "poll", strconv.FormatInt(pollID, 10), "upsert", p, 0)
		}
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// DELETE /api/polls/{id} — poll owner or admin.
func (a *App) handleDeletePoll(w http.ResponseWriter, r *http.Request, u *User) {
	pollID, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		httpError(w, http.StatusBadRequest, "invalid poll id")
		return
	}
	var creatorID int64
	err = a.db.QueryRow(`SELECT creator_id FROM polls WHERE id = ?`, pollID).Scan(&creatorID)
	if err == sql.ErrNoRows {
		httpError(w, http.StatusNotFound, "poll not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if creatorID != u.ID && !u.IsAdmin {
		httpError(w, http.StatusForbidden, "only the poll creator can delete it")
		return
	}
	tx, err := a.db.Begin()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	defer tx.Rollback()
	_, err = tx.Exec(`DELETE FROM polls WHERE id = ?`, pollID)
	if err == nil {
		err = appendSync(tx, "poll", strconv.FormatInt(pollID, 10), "delete", nil, 0)
	}
	if err == nil {
		err = tx.Commit()
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
