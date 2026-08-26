package store

import (
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

// ReadSyncLog returns deltas newer than since. When filterVisibility is set the
// SQL narrows the rows coarsely — by the reader's own visibility and by the
// clubs they belong to — but it is only a prefilter: the caller's canSee is the
// authority, so the two can never disagree about what a subscriber may read.
func ReadSyncLog(db *gorm.DB, since int64, userID int64, isAdmin bool, clubIDs []int64, filterVisibility bool) ([]SyncLogRecord, error) {
	query := db.Model(&syncLogModel{}).Where("id > ?", since)
	if filterVisibility {
		query = query.Where("visible_to IS NULL OR visible_to = ? OR (visible_to = ? AND ?)", userID, -1, isAdmin)
		if len(clubIDs) == 0 {
			query = query.Where("club_id IS NULL")
		} else {
			query = query.Where("club_id IS NULL OR club_id IN ?", clubIDs)
		}
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
