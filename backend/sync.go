package main

// The sync engine. Every mutation appends a delta to sync_log inside the same
// transaction as the domain write; a single dispatcher goroutine reads new
// rows in order and fans them out to connected SSE clients. The log id doubles
// as the global logical clock: clients resume with `Last-Event-ID` (or
// ?last_id after a bootstrap) and the server replays missed rows, or tells
// them to re-bootstrap when the log has been compacted past their cursor.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"sync"
	"time"

	"freipadel/internal/store"
	"gorm.io/gorm"
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
	ClubID        int64          `json:"club_id"`
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

// visibleToAdmins marks deltas (e.g. invites) only admins may receive. The
// store shares the constant so its SQL and canSee cannot drift apart.
const visibleToAdmins = store.VisibleToAdmins

type syncEvent struct {
	ID       int64           `json:"id"` // 0 = ephemeral (not persisted, no SSE id)
	Entity   string          `json:"entity"`
	EntityID string          `json:"entity_id"`
	Action   string          `json:"action"`
	Payload  json.RawMessage `json:"payload,omitempty"`

	visibleTo int64 // 0 = everyone; -1 = admins; otherwise only this user id
	clubID    int64 // 0 = not scoped to a club
}

type subscriber struct {
	userID  int64
	isAdmin bool
	clubIDs map[int64]bool // clubs this user belongs to
	owned   map[int64]bool // clubs this user owns
}

// canSee decides what one connection may read. It is the single authority;
// ReadSyncLog's SQL reproduces it so that replaying the log and live fan-out
// deliver the same rows. Change one and you must change the other.
//
// A club-scoped delta reaches that club's members. Global admins are not
// implicitly members — otherwise every admin client would accumulate polls it
// never renders — but they do see club management deltas (invites) everywhere,
// as does the club's own owner.
func (s subscriber) canSee(visibleTo, clubID int64) bool {
	if clubID == 0 {
		switch visibleTo {
		case 0:
			return true
		case visibleToAdmins:
			return s.isAdmin
		default:
			return visibleTo == s.userID
		}
	}
	switch visibleTo {
	case 0:
		return s.clubIDs[clubID]
	case visibleToAdmins:
		return s.isAdmin || s.owned[clubID]
	default:
		return visibleTo == s.userID
	}
}

// newSubscriber snapshots a user's club membership for the life of one SSE
// connection. A membership change emits a "me" delta, the client re-bootstraps
// and reconnects, and this is rebuilt.
func (a *App) newSubscriber(r *http.Request, u *User) (subscriber, error) {
	db := a.orm.WithContext(r.Context())
	memberOf, err := store.ListClubMemberIDs(db, u.ID)
	if err != nil {
		return subscriber{}, err
	}
	owned, err := store.ListOwnedClubIDs(db, u.ID)
	if err != nil {
		return subscriber{}, err
	}
	sub := subscriber{
		userID: u.ID, isAdmin: u.IsAdmin,
		clubIDs: make(map[int64]bool, len(memberOf)),
		owned:   make(map[int64]bool, len(owned)),
	}
	for _, clubID := range memberOf {
		sub.clubIDs[clubID] = true
	}
	for _, clubID := range owned {
		sub.owned[clubID] = true
	}
	return sub, nil
}

// audience describes this subscriber to the store in the same terms canSee
// uses. Membership and ownership stay separate: they grant different rows.
func (s subscriber) audience() *store.SyncAudience {
	return &store.SyncAudience{
		UserID: s.userID, IsAdmin: s.isAdmin,
		MemberOf: clubIDList(s.clubIDs), Owns: clubIDList(s.owned),
	}
}

func clubIDList(set map[int64]bool) []int64 {
	clubIDs := make([]int64, 0, len(set))
	for clubID := range set {
		clubIDs = append(clubIDs, clubID)
	}
	return clubIDs
}

func loadSyncPollGORM(db *gorm.DB, id int64) (*syncPoll, error) {
	record, err := store.FindPoll(db, id)
	if err != nil {
		return nil, err
	}
	slots, err := store.ListPollSlots(db, &id, nil)
	if err != nil {
		return nil, err
	}
	poll := &syncPoll{
		ID: record.ID, ClubID: record.ClubID, Title: record.Title,
		CreatorID: record.CreatorID, CreatorName: record.CreatorName,
		Status: record.Status, WinningSlotID: record.WinningSlotID,
		CreatedAt: record.CreatedAt, ClosedAt: record.ClosedAt, Slots: make([]syncPollSlot, len(slots)),
	}
	for i, slot := range slots {
		poll.Slots[i] = syncPollSlot{
			ID: slot.ID, Date: slot.Date, Time: slot.Time, DurationMinutes: slot.DurationMinutes,
			Location: slot.Location, Court: slot.Court, Price: slot.Price, Currency: slot.Currency,
		}
	}
	return poll, nil
}

// --- Hub: fans persisted (and ephemeral) deltas out to SSE connections ---

type syncHub struct {
	db *gorm.DB

	mu             sync.Mutex
	subs           map[chan syncEvent]subscriber
	lastDispatched int64

	wake chan struct{}
}

func newSyncHub(db *gorm.DB) *syncHub {
	h := &syncHub{db: db, subs: map[chan syncEvent]subscriber{}, wake: make(chan struct{}, 1)}
	h.lastDispatched, _ = store.MaxSyncID(db)
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
		if !sub.canSee(ev.visibleTo, ev.clubID) {
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
	var audience *store.SyncAudience
	if sub != nil {
		audience = sub.audience()
	}
	records, err := store.ReadSyncLog(h.db, since, audience)
	if err != nil {
		return nil, err
	}
	events := make([]syncEvent, 0, len(records))
	for _, record := range records {
		event := syncEvent{
			ID: record.ID, Entity: record.Entity, EntityID: record.EntityID,
			Action: record.Action, visibleTo: record.VisibleTo, clubID: record.ClubID,
		}
		if record.Payload != "" {
			event.Payload = json.RawMessage(record.Payload)
		}
		// The SQL mirrors canSee, but canSee is still what decides.
		if sub != nil && !sub.canSee(event.visibleTo, event.clubID) {
			continue
		}
		events = append(events, event)
	}
	return events, nil
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
	maxOld, err := store.MaxExpiredSyncID(a.orm)
	if err != nil {
		return
	}
	if maxOld == 0 {
		return
	}
	if err := store.DeleteSyncThrough(a.orm, maxOld); err != nil {
		return
	}
	_ = a.store.SetMeta("sync_trimmed_to", strconv.FormatInt(maxOld, 10))
}

// --- HTTP handlers ---

// GET /api/sync/bootstrap — full snapshot plus the sync id to resume from.
func (a *App) handleSyncBootstrap(w http.ResponseWriter, r *http.Request, u *User) {
	// Read the cursor BEFORE the snapshot: anything committed in between is
	// replayed by the event stream, and deltas apply idempotently.
	syncID, _ := store.MaxSyncID(a.orm.WithContext(r.Context()))

	// Everything below is scoped to the clubs this user belongs to — all of
	// them, not just the active one, so the switcher's badges are already
	// there when they open it.
	clubRecords, err := store.ListClubsForUser(a.orm.WithContext(r.Context()), u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	clubs := clubList(clubRecords, u.ID)
	clubIDs := make([]int64, len(clubRecords))
	for i, club := range clubRecords {
		clubIDs[i] = club.ID
	}

	// The pool has a single connection: each result set must be fully read
	// before the next query starts.
	memberRecords, err := store.ListVisibleMembers(a.orm.WithContext(r.Context()), u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	users := make([]syncMember, len(memberRecords))
	for i, member := range memberRecords {
		users[i] = syncMember{ID: member.ID, Name: member.Name, IsAdmin: member.IsAdmin}
	}

	settings, err := a.loadSettings(u.ID)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	polls, err := loadSyncPollsGORM(a.orm.WithContext(r.Context()), clubIDs)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	voteRecords, err := store.ListVotes(a.orm.WithContext(r.Context()), clubIDs)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	votes := make([]syncVote, len(voteRecords))
	for i, vote := range voteRecords {
		votes[i] = syncVote{PollSlotID: vote.PollSlotID, UserID: vote.UserID, Name: vote.Name, Vote: vote.Vote}
	}

	keys, err := slotSnapshotKeysGORM(a.orm.WithContext(r.Context()), clubRecords)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}

	// Invites go to whoever may manage the club they belong to: global admins
	// everywhere, club owners for their own.
	var invites []Invite
	if u.IsAdmin || ownsAnyClub(clubRecords, u.ID) {
		scope, scopeErr := a.inviteScope(r, u)
		if scopeErr != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		records, loadErr := store.ListInvites(a.orm.WithContext(r.Context()), scope)
		if loadErr != nil {
			httpError(w, http.StatusInternalServerError, "database error")
			return
		}
		invites = make([]Invite, len(records))
		for i, record := range records {
			invites[i] = inviteFromRecord(record)
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"sync_id":         syncID,
		"clubs":           clubs,
		"active_club_id":  u.ActiveClubID,
		"users":           users,
		"settings":        settings,
		"polls":           polls,
		"votes":           votes,
		"invites":         invites,
		"slot_keys":       keys,
		"last_fetched_at": a.store.GetMeta("last_fetched_at"),
		"scraping":        a.isScraping(),
	})
}

func ownsAnyClub(clubs []store.ClubRecord, userID int64) bool {
	for _, club := range clubs {
		if club.OwnerID == userID {
			return true
		}
	}
	return false
}

func loadSyncPollsGORM(db *gorm.DB, clubIDs []int64) ([]*syncPoll, error) {
	records, err := store.ListPolls(db, clubIDs)
	if err != nil {
		return nil, err
	}
	polls := make([]*syncPoll, len(records))
	byID := make(map[int64]*syncPoll, len(records))
	for i, record := range records {
		polls[i] = &syncPoll{
			ID: record.ID, ClubID: record.ClubID, Title: record.Title,
			CreatorID: record.CreatorID, CreatorName: record.CreatorName,
			Status: record.Status, WinningSlotID: record.WinningSlotID,
			CreatedAt: record.CreatedAt, ClosedAt: record.ClosedAt, Slots: []syncPollSlot{},
		}
		byID[record.ID] = polls[i]
	}
	slots, err := store.ListPollSlots(db, nil, clubIDs)
	if err != nil {
		return nil, err
	}
	for _, slot := range slots {
		if poll := byID[slot.PollID]; poll != nil {
			poll.Slots = append(poll.Slots, syncPollSlot{
				ID: slot.ID, Date: slot.Date, Time: slot.Time, DurationMinutes: slot.DurationMinutes,
				Location: slot.Location, Court: slot.Court, Price: slot.Price, Currency: slot.Currency,
			})
		}
	}
	return polls, nil
}

// slotSnapshotKeysGORM returns the bookable slot keys of each club, keyed by
// club id. Only the keys are synced; the slot rows themselves stay lazy and are
// fetched per club by /api/slots when the page opens.
func slotSnapshotKeysGORM(db *gorm.DB, clubs []store.ClubRecord) (map[string][]string, error) {
	slots, err := store.ListSlotAvailability(db)
	if err != nil {
		return nil, err
	}
	byClub := make(map[string][]string, len(clubs))
	for _, club := range clubs {
		byClub[strconv.FormatInt(club.ID, 10)] = clubSlotKeys(slots, parseLocations(club.Locations))
	}
	return byClub, nil
}

func clubSlotKeys(slots []store.SlotRecord, locations []string) []string {
	var allowed map[string]bool
	if len(locations) != 0 {
		allowed = make(map[string]bool, len(locations))
		for _, location := range locations {
			allowed[location] = true
		}
	}
	keys := make([]string, 0, len(slots))
	for _, slot := range slots {
		if allowed != nil && !allowed[slot.Location] {
			continue
		}
		keys = append(keys, slotKey(slot.Date, slot.Time, slot.DurationMinutes, slot.Location))
	}
	return keys
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
	sub, err := a.newSubscriber(r, u)
	if err != nil {
		httpError(w, http.StatusInternalServerError, "database error")
		return
	}
	ch, since := a.hub.subscribe(sub)
	defer a.hub.unsubscribe(ch)

	// If compaction trimmed past the client's cursor the replay would be
	// incomplete — tell it to re-bootstrap instead.
	trimmedTo, _ := strconv.ParseInt(a.store.GetMeta("sync_trimmed_to"), 10, 64)
	if lastID < trimmedTo {
		fmt.Fprint(w, "event: reset\ndata: {}\n\n")
		lastID = since
	}

	sent := lastID
	if lastID < since {
		evs, err := a.hub.readLog(lastID, &sub)
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

	// A real `ping` event (not an SSE comment) so the client can run a
	// staleness watchdog — comments are invisible to the EventSource API.
	keepalive := time.NewTicker(5 * time.Second)
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
			fmt.Fprint(w, "event: ping\ndata: {}\n\n")
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
