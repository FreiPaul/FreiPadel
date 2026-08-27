package store

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

type SyncLogRecord struct {
	ID        int64
	Entity    string
	EntityID  string
	Action    string
	Payload   string
	VisibleTo int64
	ClubID    int64
}

func MaxSyncID(db *gorm.DB) (int64, error) {
	model, err := gorm.G[syncLogModel](db).Order("id DESC").First(db.Statement.Context)
	if IsNotFound(err) {
		return 0, nil
	}
	return model.ID, err
}

// VisibleToAdmins marks deltas only admins may receive — and, on a club-scoped
// row, that club's owner.
const VisibleToAdmins = -1

// SyncAudience is who is reading the log. It carries every input the caller's
// own visibility check uses, so the SQL below can reproduce that check exactly
// instead of approximating it.
type SyncAudience struct {
	UserID   int64
	IsAdmin  bool
	MemberOf []int64 // clubs the reader belongs to
	Owns     []int64 // clubs the reader owns
}

// condition renders the audience as SQL. It mirrors the caller's own check row
// for row: a global row reaches everyone, its addressee, or any admin; a
// club-scoped row reaches that club's members, plus admins and the club's owner
// when the row is admin-only, plus its addressee wherever they are.
//
// Membership is deliberately not required for the last two: an admin watching
// invites in a club they never joined, and an owner who is not a member, both
// have to survive an SSE replay, not just live fan-out.
func (a SyncAudience) condition() (string, []any) {
	clauses := []string{"(club_id IS NULL AND visible_to IS NULL)", "visible_to = ?"}
	args := []any{a.UserID}
	if a.IsAdmin {
		clauses = append(clauses, "visible_to = ?")
		args = append(args, VisibleToAdmins)
	}
	if len(a.MemberOf) > 0 {
		clauses = append(clauses, "(visible_to IS NULL AND club_id IN ?)")
		args = append(args, a.MemberOf)
	}
	if len(a.Owns) > 0 {
		clauses = append(clauses, "(visible_to = ? AND club_id IN ?)")
		args = append(args, VisibleToAdmins, a.Owns)
	}
	return strings.Join(clauses, " OR "), args
}

// ReadSyncLog returns deltas newer than since, oldest first. A nil audience
// reads every row, which is what the dispatcher wants; otherwise the rows are
// narrowed to what that audience may read. The caller's own check stays the
// authority — this only spares it rows it would refuse anyway.
func ReadSyncLog(db *gorm.DB, since int64, audience *SyncAudience) ([]SyncLogRecord, error) {
	query := db.Model(&syncLogModel{}).Where("id > ?", since)
	if audience != nil {
		clause, args := audience.condition()
		query = query.Where(clause, args...)
	}
	var models []syncLogModel
	if err := query.Order("id").Find(&models).Error; err != nil {
		return nil, err
	}
	records := make([]SyncLogRecord, len(models))
	for i, model := range models {
		records[i] = syncLogRecord(model)
	}
	return records, nil
}

func MaxExpiredSyncID(db *gorm.DB) (int64, error) {
	model, err := gorm.G[syncLogModel](db).
		Where("created_at < ?", sqliteTime(time.Now().UTC().Add(-7*24*time.Hour))).
		Order("id DESC").
		First(db.Statement.Context)
	if IsNotFound(err) {
		return 0, nil
	}
	return model.ID, err
}

func syncLogRecord(model syncLogModel) SyncLogRecord {
	record := SyncLogRecord{
		ID: model.ID, Entity: model.Entity, EntityID: model.EntityID, Action: model.Action,
	}
	if model.Payload != nil {
		record.Payload = *model.Payload
	}
	if model.VisibleTo != nil {
		record.VisibleTo = *model.VisibleTo
	}
	if model.ClubID != nil {
		record.ClubID = *model.ClubID
	}
	return record
}

func DeleteSyncThrough(db *gorm.DB, id int64) error {
	return db.Where("id <= ?", id).Delete(&syncLogModel{}).Error
}
