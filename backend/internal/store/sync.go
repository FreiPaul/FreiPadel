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
}

func MaxSyncID(db *gorm.DB) (int64, error) {
	model, err := gorm.G[syncLogModel](db).Order("id DESC").First(db.Statement.Context)
	if IsNotFound(err) {
		return 0, nil
	}
	return model.ID, err
}

func ReadSyncLog(db *gorm.DB, since int64, userID int64, isAdmin bool, filterVisibility bool) ([]SyncLogRecord, error) {
	query := gorm.G[syncLogModel](db).Where("id > ?", since)
	if filterVisibility {
		query = query.Where("visible_to IS NULL OR visible_to = ? OR (visible_to = ? AND ?)", userID, -1, isAdmin)
	}
	models, err := query.Order("id").Find(db.Statement.Context)
	if err != nil {
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
	return record
}

func DeleteSyncThrough(db *gorm.DB, id int64) error {
	return db.Where("id <= ?", id).Delete(&syncLogModel{}).Error
}
