package main

import (
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"freipadel/internal/store"

	"gorm.io/gorm"
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
	records, err := store.ListPolls(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	polls := []*Poll{}
	byID := map[int64]*Poll{}
	for _, record := range records {
		p := Poll{
			ID: record.ID, Title: record.Title, CreatorID: record.CreatorID, CreatorName: record.CreatorName,
			Status: record.Status, WinningSlotID: record.WinningSlotID,
			CreatedAt: record.CreatedAt, ClosedAt: record.ClosedAt, Slots: []PollSlot{},
		}
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
	availability, err := store.ListSlotAvailability(a.orm.WithContext(r.Context()))
	if err == nil {
		for _, slot := range availability {
			availSet[slotKey(slot.Date, slot.Time, slot.DurationMinutes, slot.Location)] = true
		}
	}
	now := time.Now().In(a.tz)
	today, nowTime := now.Format("2006-01-02"), now.Format("15:04")

	slotRecords, err := store.ListPollSlots(a.orm.WithContext(r.Context()), nil)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	slotByID := map[int64]*PollSlot{}
	for _, record := range slotRecords {
		s := PollSlot{
			ID: record.ID, Date: record.Date, Time: record.Time, DurationMinutes: record.DurationMinutes,
			Location: record.Location, Court: record.Court, Price: record.Price, Currency: record.Currency,
		}
		s.Votes = []Voter{}
		s.Expired = s.Date < today || (s.Date == today && s.Time <= nowTime)
		s.Available = !s.Expired && availSet[slotKey(s.Date, s.Time, s.DurationMinutes, s.Location)]
		if strings.Contains(strings.ToLower(s.Court), "single") {
			s.Available = false
		}
		if p, ok := byID[record.PollID]; ok {
			p.Slots = append(p.Slots, s)
		}
	}
	// Re-index into the slices (appends above copied the structs).
	for _, p := range byID {
		for i := range p.Slots {
			slotByID[p.Slots[i].ID] = &p.Slots[i]
		}
	}

	voteRecords, err := store.ListVotes(a.orm.WithContext(r.Context()))
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	for _, record := range voteRecords {
		voter := Voter{UserID: record.UserID, Name: record.Name, Vote: record.Vote}
		s, ok := slotByID[record.PollSlotID]
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
		Title  string `json:"title"`
		Origin string `json:"origin"`
		Slots  []struct {
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

	slots := make([]store.PollSlotRecord, len(req.Slots))
	for i, s := range req.Slots {
		currency := s.Currency
		if currency == "" {
			currency = "EUR"
		}
		slots[i] = store.PollSlotRecord{
			Date: s.Date, Time: s.Time, DurationMinutes: s.DurationMinutes,
			Location: s.Location, Court: strings.Join(s.Courts, ", "), Price: s.MinPrice, Currency: currency,
		}
	}
	var pollID int64

	err := a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		var err error
		pollID, err = store.CreatePoll(tx, u.ID, req.Title, slots)
		if err != nil {
			return err
		}
		poll, err := loadSyncPollGORM(tx, pollID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(poll)
		return store.AppendSync(tx, "poll", strconv.FormatInt(pollID, 10), "upsert", payload, 0)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()

	// notify admin via telegram
	a.telegramSender.SendMsg(a.scrapeCfg.Telegram.AdminChatID, fmt.Sprint("New FreiPadel Poll from ", u.Name))
	err = a.notifyOnNewPoll(req.Origin)
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, fmt.Sprintf("Error happenedf: %s", err))
	}

	writeJSON(w, http.StatusCreated, map[string]int64{"id": pollID})
}

func (a *App) notifyOnNewPoll(origin string) error {
	allUsers, err := store.ListUsers(a.store.ORM)
	if err != nil {
		return err
	}

	for _, u := range allUsers {
		userNotificationsMap := make(map[string]bool)
		userSettings, err := store.FindSettings(a.store.ORM, u.ID)
		if err != nil {
			return err
		}
		err = json.Unmarshal([]byte(userSettings.Notifications), &userNotificationsMap)
		if err != nil {
			return err
		}
		// fmt.Printf("ID: %d, Mail: %s, notification: %s \n", u.ID, u.Email, userNotificationsMap)

		if userNotificationsMap["poll_created"] {
			fmt.Printf("send to: %s\n", u.Email)
			link := template.HTMLEscapeString(strings.TrimRight(strings.TrimSpace(origin), "/")) + "/polls"
			body := fmt.Sprintf(`
<div style="background:#f5f7fa;padding:32px 16px;font-family:Arial,sans-serif;color:#17202a;">
  <div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e2e8f0;border-radius:12px;padding:32px;">
    <h1 style="margin:0 0 16px;font-size:24px;line-height:1.3;color:#0f172a;">A new padel poll is ready</h1>
    <p style="margin:0 0 24px;font-size:16px;line-height:1.6;color:#475569;">Someone started a new poll on FreiPadel. Add your availability so the group can find a time that works.</p>
    <p style="margin:0 0 24px;text-align:center;"><a href="%s" style="display:inline-block;background:#0f766e;color:#ffffff;border-radius:8px;padding:12px 20px;text-decoration:none;font-weight:600;">View the poll</a></p>
    <p style="margin:0;font-size:13px;line-height:1.5;color:#64748b;">You can also open FreiPadel here: <a href="%s" style="color:#0f766e;">%s</a></p>
  </div>
</div>`, link, link, link)
			if err := a.emailer.Send(
				u.Email,
				"New Padel Poll",
				body,
			); err != nil {
				log.Printf("email send failed: %v", err)
			}
		}
	}

	return nil
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

	slotPollID, status, err := store.FindPollForSlot(a.orm.WithContext(r.Context()), req.PollSlotID)
	if store.IsNotFound(err) || slotPollID != pollID {
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

	voteEntityID := fmt.Sprintf("%d|%d", req.PollSlotID, u.ID)
	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if req.Vote == nil {
			if err := store.DeleteVote(tx, req.PollSlotID, u.ID); err != nil {
				return err
			}
			return store.AppendSync(tx, "vote", voteEntityID, "delete", nil, 0)
		}
		if err := store.UpsertVote(tx, req.PollSlotID, u.ID, *req.Vote); err != nil {
			return err
		}
		payload, _ := json.Marshal(syncVote{PollSlotID: req.PollSlotID, UserID: u.ID, Name: u.Name, Vote: *req.Vote})
		return store.AppendSync(tx, "vote", voteEntityID, "upsert", payload, 0)
	})
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

	poll, err := store.FindPoll(a.orm.WithContext(r.Context()), pollID)
	if store.IsNotFound(err) {
		httpError(w, http.StatusNotFound, "poll not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if poll.CreatorID != u.ID && !u.IsAdmin {
		httpError(w, http.StatusForbidden, "only the poll creator can close it")
		return
	}
	if poll.Status != "active" {
		httpError(w, http.StatusConflict, "poll is already closed")
		return
	}
	if req.WinningSlotID != nil {
		belongs, err := store.PollSlotBelongsTo(a.orm.WithContext(r.Context()), *req.WinningSlotID, pollID)
		if err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		if !belongs {
			httpError(w, http.StatusBadRequest, "winning slot does not belong to this poll")
			return
		}
	}

	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.ClosePoll(tx, pollID, req.WinningSlotID); err != nil {
			return err
		}
		poll, err := loadSyncPollGORM(tx, pollID)
		if err != nil {
			return err
		}
		payload, _ := json.Marshal(poll)
		return store.AppendSync(tx, "poll", strconv.FormatInt(pollID, 10), "upsert", payload, 0)
	})
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
	poll, err := store.FindPoll(a.orm.WithContext(r.Context()), pollID)
	if store.IsNotFound(err) {
		httpError(w, http.StatusNotFound, "poll not found")
		return
	}
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	if poll.CreatorID != u.ID && !u.IsAdmin {
		httpError(w, http.StatusForbidden, "only the poll creator can delete it")
		return
	}
	err = a.orm.WithContext(r.Context()).Transaction(func(tx *gorm.DB) error {
		if err := store.DeletePoll(tx, pollID); err != nil {
			return err
		}
		return store.AppendSync(tx, "poll", strconv.FormatInt(pollID, 10), "delete", nil, 0)
	})
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	a.hub.notify()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
