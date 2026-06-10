package main

// The sync engine. Every mutation appends a delta to sync_log inside the same
// transaction as the domain write; a single dispatcher goroutine reads new
// rows in order and fans them out to connected SSE clients. The log id doubles
// as the global logical clock: clients resume with `Last-Event-ID` (or
// ?last_id after a bootstrap) and the server replays missed rows, or tells
// them to re-bootstrap when the log has been compacted past their cursor.

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"
)

// --- Wire shapes (consumed by frontend/src/lib/sync.svelte.ts) ---

// syncPollSlot is the immutable part of a poll slot; votes are separate
// entities and availability/expiry are derived client-side.
type syncPollSlot struct {
	ID              int64   `json:"id"`
	Date            string  `json:"date"`
	Time            string  `json:"time"`
	DurationMinutes int     `json:"duration_minutes"`
	Location        string  `json:"location"`
	Court           string  `json:"court"`
	Price           float64 `json:"price"`
	Currency        string  `json:"currency"`
}

type syncPoll struct {
	ID            int64          `json:"id"`
	Title         string         `json:"title"`
	CreatorID     int64          `json:"creator_id"`
	CreatorName   string         `json:"creator_name"`
	Status        string         `json:"status"`
	WinningSlotID *int64         `json:"winning_slot_id"`
	CreatedAt     string         `json:"created_at"`
	ClosedAt      *string        `json:"closed_at"`
	Slots         []syncPollSlot `json:"slots"`
}

type syncVote struct {
	PollSlotID int64  `json:"poll_slot_id"`
	UserID     int64  `json:"user_id"`
	Name       string `json:"name"`
	Vote       bool   `json:"vote"`
}

type syncMember struct {
	ID      int64  `json:"id"`
	Name    string `json:"name"`
	IsAdmin bool   `json:"is_admin"`
}

// visibleToAdmins marks deltas (e.g. invites) only admins may receive.
const visibleToAdmins = -1

type syncEvent struct {
	ID       int64           `json:"id"` // 0 = ephemeral (not persisted, no SSE id)
	Entity   string          `json:"entity"`
	EntityID string          `json:"entity_id"`
	Action   string          `json:"action"`
	Payload  json.RawMessage `json:"payload,omitempty"`

	visibleTo int64 // 0 = everyone; -1 = admins; otherwise only this user id
}

type subscriber struct {
	userID  int64
	isAdmin bool
}

func (s subscriber) canSee(visibleTo int64) bool {
	switch visibleTo {
	case 0:
		return true
	case visibleToAdmins:
		return s.isAdmin
	default:
		return visibleTo == s.userID
	}
}

type execer interface {
	Exec(query string, args ...any) (sql.Result, error)
}

type queryer interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

// appendSync records a delta. Call it inside the transaction that performs the
// domain write, then hub.notify() after commit. visibleTo 0 = all users.
func appendSync(e execer, entity, entityID, action string, payload any, visibleTo int64) error {
	var data any
	if payload != nil {
		b, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		data = string(b)
	}
	var vis any
	if visibleTo != 0 {
		vis = visibleTo
	}
	_, err := e.Exec(`INSERT INTO sync_log (entity, entity_id, action, payload, visible_to)
		VALUES (?, ?, ?, ?, ?)`, entity, entityID, action, data, vis)
	return err
}

// loadSyncPoll loads one poll with its slots, in the shape the client store
// expects. Works inside a transaction (q = *sql.Tx) or on the pool.
func loadSyncPoll(q queryer, id int64) (*syncPoll, error) {
	var p syncPoll
	err := q.QueryRow(`
		SELECT p.id, p.title, p.creator_id, usr.name, p.status, p.winning_slot_id, p.created_at, p.closed_at
		FROM polls p JOIN users usr ON usr.id = p.creator_id WHERE p.id = ?`, id).
		Scan(&p.ID, &p.Title, &p.CreatorID, &p.CreatorName, &p.Status, &p.WinningSlotID, &p.CreatedAt, &p.ClosedAt)
	if err != nil {
		return nil, err
	}
	p.Slots = []syncPollSlot{}
	rows, err := q.Query(`SELECT id, date, time, duration_minutes, location, court, price, currency
		FROM poll_slots WHERE poll_id = ? ORDER BY date, time, location, duration_minutes`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var s syncPollSlot
		if err := rows.Scan(&s.ID, &s.Date, &s.Time, &s.DurationMinutes, &s.Location, &s.Court, &s.Price, &s.Currency); err == nil {
			p.Slots = append(p.Slots, s)
		}
	}
	return &p, nil
}

// --- Hub: fans persisted (and ephemeral) deltas out to SSE connections ---

type syncHub struct {
	db *sql.DB

	mu             sync.Mutex
	subs           map[chan syncEvent]subscriber
	lastDispatched int64

	wake chan struct{}
}

func newSyncHub(db *sql.DB) *syncHub {
	h := &syncHub{db: db, subs: map[chan syncEvent]subscriber{}, wake: make(chan struct{}, 1)}
	_ = db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM sync_log`).Scan(&h.lastDispatched)
	go h.dispatchLoop()
	return h
}

// notify wakes the dispatcher after a sync_log append was committed.
func (h *syncHub) notify() {
	select {
	case h.wake <- struct{}{}:
	default:
	}
}

func (h *syncHub) dispatchLoop() {
	for range h.wake {
		h.mu.Lock()
		since := h.lastDispatched
		h.mu.Unlock()
		evs, err := h.readLog(since, nil)
		if err != nil {
			continue
		}
		for _, ev := range evs {
			h.fanOut(ev)
		}
	}
}

// fanOut delivers one event to all subscribers it is visible to. Subscribers
// that cannot keep up are dropped; their client reconnects and resumes from
// its Last-Event-ID.
func (h *syncHub) fanOut(ev syncEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if ev.ID > h.lastDispatched {
		h.lastDispatched = ev.ID
	}
	for ch, sub := range h.subs {
		if !sub.canSee(ev.visibleTo) {
			continue
		}
		select {
		case ch <- ev:
		default:
			delete(h.subs, ch)
			close(ch)
		}
	}
}

// broadcastEphemeral sends a transient event (scrape status) that is not
// persisted to the log — it carries no SSE id, so it never moves a client's
// resume cursor.
func (h *syncHub) broadcastEphemeral(entity, entityID string, payload any) {
	b, _ := json.Marshal(payload)
	h.fanOut(syncEvent{Entity: entity, EntityID: entityID, Action: "upsert", Payload: b})
}

// readLog returns persisted deltas with id > since, oldest first. A nil sub
// returns rows of all visibilities (dispatcher); otherwise only rows visible
// to that subscriber (SSE replay).
func (h *syncHub) readLog(since int64, sub *subscriber) ([]syncEvent, error) {
	q := `SELECT id, entity, entity_id, action, COALESCE(payload, ''), COALESCE(visible_to, 0)
		FROM sync_log WHERE id > ?`
	args := []any{since}
	if sub != nil {
		q += ` AND (visible_to IS NULL OR visible_to = ? OR (visible_to = -1 AND ?))`
		args = append(args, sub.userID, sub.isAdmin)
	}
	q += ` ORDER BY id`
	rows, err := h.db.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var evs []syncEvent
	for rows.Next() {
		var ev syncEvent
		var payload string
		if err := rows.Scan(&ev.ID, &ev.Entity, &ev.EntityID, &ev.Action, &payload, &ev.visibleTo); err != nil {
			return nil, err
		}
		if payload != "" {
			ev.Payload = json.RawMessage(payload)
		}
		evs = append(evs, ev)
	}
	return evs, rows.Err()
}

// subscribe registers a listener. Events with id > since arrive on the
// channel; anything older must be replayed from the log by the caller.
func (h *syncHub) subscribe(sub subscriber) (ch chan syncEvent, since int64) {
	ch = make(chan syncEvent, 64)
	h.mu.Lock()
	defer h.mu.Unlock()
	h.subs[ch] = sub
	return ch, h.lastDispatched
}

func (h *syncHub) unsubscribe(ch chan syncEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.subs[ch]; ok {
		delete(h.subs, ch)
		close(ch)
	}
}

// compactSyncLog trims deltas older than 7 days. Clients resuming from before
// the trim point are told to re-bootstrap (via the sync_trimmed_to meta key).
func (a *App) compactSyncLog() {
	var maxOld int64
	_ = a.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM sync_log
		WHERE created_at < datetime('now', '-7 days')`).Scan(&maxOld)
	if maxOld == 0 {
		return
	}
	if _, err := a.db.Exec(`DELETE FROM sync_log WHERE id <= ?`, maxOld); err != nil {
		return
	}
	_ = setMeta(a.db, "sync_trimmed_to", strconv.FormatInt(maxOld, 10))
}

// --- HTTP handlers ---

// GET /api/sync/bootstrap — full snapshot plus the sync id to resume from.
func (a *App) handleSyncBootstrap(w http.ResponseWriter, r *http.Request, u *User) {
	// Read the cursor BEFORE the snapshot: anything committed in between is
	// replayed by the event stream, and deltas apply idempotently.
	var syncID int64
	_ = a.db.QueryRow(`SELECT COALESCE(MAX(id), 0) FROM sync_log`).Scan(&syncID)

	// The pool has a single connection: each result set must be fully read
	// before the next query starts.
	users := []syncMember{}
	rows, err := a.db.Query(`SELECT id, name, is_admin FROM users ORDER BY name`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	for rows.Next() {
		var m syncMember
		var isAdmin int
		if err := rows.Scan(&m.ID, &m.Name, &isAdmin); err == nil {
			m.IsAdmin = isAdmin == 1
			users = append(users, m)
		}
	}
	rows.Close()

	settings, err := a.loadSettings(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	polls, err := loadSyncPolls(a.db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	votes := []syncVote{}
	rows, err = a.db.Query(`SELECT v.poll_slot_id, v.user_id, usr.name, v.vote
		FROM votes v JOIN users usr ON usr.id = v.user_id ORDER BY usr.name`)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	for rows.Next() {
		var v syncVote
		var vote int
		if err := rows.Scan(&v.PollSlotID, &v.UserID, &v.Name, &vote); err == nil {
			v.Vote = vote == 1
			votes = append(votes, v)
		}
	}
	rows.Close()

	keys, err := slotSnapshotKeys(a.db)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	// Invites are admin-only, like their deltas.
	var invites []Invite
	if u.IsAdmin {
		if invites, err = loadInvites(a.db); err != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sync_id":         syncID,
		"users":           users,
		"settings":        settings,
		"polls":           polls,
		"votes":           votes,
		"invites":         invites,
		"slot_keys":       keys,
		"last_fetched_at": getMeta(a.db, "last_fetched_at"),
		"scraping":        a.isScraping(),
	})
}

// loadSyncPolls loads all polls with their slots (two queries, joined in Go).
func loadSyncPolls(q queryer) ([]*syncPoll, error) {
	polls := []*syncPoll{}
	byID := map[int64]*syncPoll{}
	rows, err := q.Query(`
		SELECT p.id, p.title, p.creator_id, usr.name, p.status, p.winning_slot_id, p.created_at, p.closed_at
		FROM polls p JOIN users usr ON usr.id = p.creator_id ORDER BY p.created_at DESC`)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var p syncPoll
		if err := rows.Scan(&p.ID, &p.Title, &p.CreatorID, &p.CreatorName, &p.Status,
			&p.WinningSlotID, &p.CreatedAt, &p.ClosedAt); err != nil {
			continue
		}
		p.Slots = []syncPollSlot{}
		polls = append(polls, &p)
		byID[p.ID] = &p
	}
	rows.Close()

	slotRows, err := q.Query(`SELECT id, poll_id, date, time, duration_minutes, location, court, price, currency
		FROM poll_slots ORDER BY date, time, location, duration_minutes`)
	if err != nil {
		return nil, err
	}
	defer slotRows.Close()
	for slotRows.Next() {
		var s syncPollSlot
		var pollID int64
		if err := slotRows.Scan(&s.ID, &pollID, &s.Date, &s.Time, &s.DurationMinutes,
			&s.Location, &s.Court, &s.Price, &s.Currency); err != nil {
			continue
		}
		if p, ok := byID[pollID]; ok {
			p.Slots = append(p.Slots, s)
		}
	}
	return polls, nil
}

// slotSnapshotKeys returns the distinct date|time|duration|location keys of
// the latest scrape — what the client needs to derive slot availability.
func slotSnapshotKeys(q queryer) ([]string, error) {
	keys := []string{}
	rows, err := q.Query(`SELECT DISTINCT date, time, duration_minutes, location FROM slots`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var date, tm, location string
		var dur int
		if err := rows.Scan(&date, &tm, &dur, &location); err == nil {
			keys = append(keys, slotKey(date, tm, dur, location))
		}
	}
	return keys, rows.Err()
}

// GET /api/sync/events — SSE delta stream. Resumes from Last-Event-ID (sent
// by the browser on automatic reconnects) or ?last_id (set on the first
// connect after a bootstrap); replays missed deltas, then streams live.
func (a *App) handleSyncEvents(w http.ResponseWriter, r *http.Request, u *User) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpError(w, http.StatusInternalServerError, "streaming unsupported")
		return
	}
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no") // no buffering in nginx-style proxies

	lastID, _ := strconv.ParseInt(r.Header.Get("Last-Event-ID"), 10, 64)
	if lastID == 0 {
		lastID, _ = strconv.ParseInt(r.URL.Query().Get("last_id"), 10, 64)
	}

	// Subscribe before replaying: events ≤ since come from the log below,
	// events > since arrive on the channel — no gap, duplicates filtered by id.
	ch, since := a.hub.subscribe(subscriber{userID: u.ID, isAdmin: u.IsAdmin})
	defer a.hub.unsubscribe(ch)

	// If compaction trimmed past the client's cursor the replay would be
	// incomplete — tell it to re-bootstrap instead.
	trimmedTo, _ := strconv.ParseInt(getMeta(a.db, "sync_trimmed_to"), 10, 64)
	if lastID < trimmedTo {
		fmt.Fprint(w, "event: reset\ndata: {}\n\n")
		lastID = since
	}

	sent := lastID
	if lastID < since {
		evs, err := a.hub.readLog(lastID, &subscriber{userID: u.ID, isAdmin: u.IsAdmin})
		if err == nil {
			for _, ev := range evs {
				if ev.ID > since {
					break // the channel delivers these
				}
				writeSSE(w, ev)
				sent = ev.ID
			}
		}
	}
	flusher.Flush()

	keepalive := time.NewTicker(25 * time.Second)
	defer keepalive.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return // dropped as a slow consumer; the client reconnects
			}
			if ev.ID != 0 {
				if ev.ID <= sent {
					continue
				}
				sent = ev.ID
			}
			writeSSE(w, ev)
			flusher.Flush()
		case <-keepalive.C:
			fmt.Fprint(w, ": ping\n\n")
			flusher.Flush()
		}
	}
}

func writeSSE(w io.Writer, ev syncEvent) {
	data, _ := json.Marshal(ev)
	if ev.ID > 0 {
		fmt.Fprintf(w, "id: %d\n", ev.ID)
	}
	fmt.Fprintf(w, "event: delta\ndata: %s\n\n", data)
}
