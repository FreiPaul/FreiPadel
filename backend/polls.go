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
	// Off the request path: the fan-out opens one SMTP connection per opted-in
	// user (10s dial timeout each), which would otherwise hold the response
	// open for minutes against a slow mailserver. It uses a.store.ORM rather
	// than the request context, so it outlives the request safely. A failed
	// notification must not fail the poll either — it is already committed and
	// broadcast over SSE.
	go func() {
		if err := a.notifyOnNewPoll(req.Origin); err != nil {
			log.Printf("notify on new poll %d: %v", pollID, err)
		}
	}()

	writeJSON(w, http.StatusCreated, map[string]int64{"id": pollID})
}

// wantsNotification reports whether a user opted into the given notification
// key. A missing settings row (older account that never saved settings) or
// unreadable JSON counts as "not opted in" rather than an error: one broken
// user must never stop the mails going out to everyone else. Reading through
// mergeNotifications keeps this consistent with GET/PUT /api/settings —
// unknown keys are ignored, absent ones fall back to the default.
func (a *App) wantsNotification(userID int64, key string) bool {
	settings, err := store.FindSettings(a.store.ORM, userID)
	if err != nil {
		if !store.IsNotFound(err) {
			log.Printf("notification settings for user %d: %v", userID, err)
		}
		return notificationDefaults[key]
	}
	var stored map[string]bool
	if err := json.Unmarshal([]byte(settings.Notifications), &stored); err != nil {
		log.Printf("notification settings for user %d: %v", userID, err)
		return notificationDefaults[key]
	}
	return mergeNotifications(stored)[key]
}

func (a *App) notifyOnNewPoll(origin string) error {
	allUsers, err := store.ListUsers(a.store.ORM)
	if err != nil {
		return err
	}

	for _, u := range allUsers {
		if a.wantsNotification(u.ID, "poll_created") {
			fmt.Printf("send to: %s\n", u.Email)
			link := template.HTMLEscapeString(a.linkOrigin(origin)) + "/polls"
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
		Origin        string `json:"origin"`
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

	// A closed poll with a winner means that slot got booked — tell everyone
	// who asked to hear about it.
	if req.WinningSlotID != nil {
		// notify admin via telegram
		a.telegramSender.SendMsg(a.scrapeCfg.Telegram.AdminChatID, fmt.Sprint("FreiPadel slot booked by ", u.Name))
		// Same as handleCreatePoll: mailing every voter must not block the
		// response.
		winningSlotID := *req.WinningSlotID
		go func() {
			if err := a.notifyOnSlotBooked(req.Origin, poll, winningSlotID); err != nil {
				log.Printf("slot booked notification failed: %v", err)
			}
		}()
	}

	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

// notifyOnSlotBooked emails the users who voted in this poll and enabled the
// "slot_booked" notification once the poll is closed on a winning slot. Users
// who voted yes on the winning slot get the "see you on court" mail; the other
// voters are told that a different slot was booked. People who never voted in
// this poll are not mailed at all. Mirrors notifyOnNewPoll — best effort, a
// failed send never fails the request.
func (a *App) notifyOnSlotBooked(origin string, poll store.PollRecord, winningSlotID int64) error {
	slots, err := store.ListPollSlots(a.store.ORM, &poll.ID)
	if err != nil {
		return err
	}
	var winner store.PollSlotRecord
	for _, s := range slots {
		if s.ID == winningSlotID {
			winner = s
			break
		}
	}
	if winner.ID == 0 {
		return fmt.Errorf("winning slot %d not found in poll %d", winningSlotID, poll.ID)
	}

	pollSlotIDs := map[int64]bool{}
	for _, s := range slots {
		pollSlotIDs[s.ID] = true
	}

	// Who to tell: everyone who cast a vote in this poll. Of those, the ones
	// who voted yes on the winning slot are actually playing.
	votes, err := store.ListVotes(a.store.ORM)
	if err != nil {
		return err
	}
	voted := map[int64]bool{}
	playing := map[int64]bool{}
	for _, v := range votes {
		if !pollSlotIDs[v.PollSlotID] {
			continue
		}
		voted[v.UserID] = true
		if v.PollSlotID == winningSlotID && v.Vote {
			playing[v.UserID] = true
		}
	}

	allUsers, err := store.ListUsers(a.store.ORM)
	if err != nil {
		return err
	}

	link := template.HTMLEscapeString(a.linkOrigin(origin)) + "/polls"
	when := template.HTMLEscapeString(formatSlotWhen(winner))
	// Location only — the concrete court is not worth mailing out.
	where := template.HTMLEscapeString(winner.Location)
	title := template.HTMLEscapeString(poll.Title)

	for _, u := range allUsers {
		if voted[u.ID] && a.wantsNotification(u.ID, "slot_booked") {
			subject, headline, intro := "Padel Slot Booked",
				"A padel slot has been booked",
				fmt.Sprintf("The poll \u201c%s\u201d is closed and your slot was picked. See you on court!", title)
			if !playing[u.ID] {
				subject, headline, intro = "Another Padel Slot Was Booked",
					"Another padel slot was booked",
					fmt.Sprintf("The poll \u201c%s\u201d is closed. A slot you did not vote for was booked, so you are not on the list for this one.", title)
			}
			body := fmt.Sprintf(`
<div style="background:#f5f7fa;padding:32px 16px;font-family:Arial,sans-serif;color:#17202a;">
  <div style="max-width:560px;margin:0 auto;background:#ffffff;border:1px solid #e2e8f0;border-radius:12px;padding:32px;">
    <h1 style="margin:0 0 16px;font-size:24px;line-height:1.3;color:#0f172a;">%s</h1>
    <p style="margin:0 0 24px;font-size:16px;line-height:1.6;color:#475569;">%s</p>
    <table style="margin:0 0 24px;font-size:16px;line-height:1.6;color:#0f172a;">
      <tr><td style="padding:0 16px 4px 0;color:#64748b;">When</td><td style="padding:0 0 4px;font-weight:600;">%s</td></tr>
      <tr><td style="padding:0 16px 4px 0;color:#64748b;">Where</td><td style="padding:0 0 4px;font-weight:600;">%s</td></tr>
    </table>
    <p style="margin:0 0 24px;text-align:center;"><a href="%s" style="display:inline-block;background:#0f766e;color:#ffffff;border-radius:8px;padding:12px 20px;text-decoration:none;font-weight:600;">View the poll</a></p>
    <p style="margin:0;font-size:13px;line-height:1.5;color:#64748b;">You can also open FreiPadel here: <a href="%s" style="color:#0f766e;">%s</a></p>
  </div>
</div>`, headline, intro, when, where, link, link, link)
			if err := a.emailer.Send(
				u.Email,
				subject,
				body,
			); err != nil {
				log.Printf("email send failed: %v", err)
			}
		}
	}

	return nil
}

// formatSlotWhen renders a poll slot as a human-readable date/time range,
// falling back to the raw values if the stored date can't be parsed.
func formatSlotWhen(s store.PollSlotRecord) string {
	day := s.Date
	if d, err := time.Parse("2006-01-02", s.Date); err == nil {
		day = d.Format("Mon, 2 Jan 2006")
	}
	if start, err := time.Parse("15:04", s.Time); err == nil && s.DurationMinutes > 0 {
		end := start.Add(time.Duration(s.DurationMinutes) * time.Minute)
		return fmt.Sprintf("%s, %s–%s (%d min)", day, s.Time, end.Format("15:04"), s.DurationMinutes)
	}
	return fmt.Sprintf("%s, %s", day, s.Time)
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
