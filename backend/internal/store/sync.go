package store

import "gorm.io/gorm"

type SyncLogRecord struct {
	ID        int64
	Entity    string
	EntityID  string
	Action    string
	Payload   string
	VisibleTo int64
}

func MaxSyncID(db *gorm.DB) (int64, error) {
	var id int64
	err := db.Model(&syncLogModel{}).Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func ReadSyncLog(db *gorm.DB, since int64, userID int64, isAdmin bool, filterVisibility bool) ([]SyncLogRecord, error) {
	query := `SELECT id, entity, entity_id, action, COALESCE(payload, '') AS payload,
		COALESCE(visible_to, 0) AS visible_to FROM sync_log WHERE id > ?`
	args := []any{since}
	if filterVisibility {
		query += ` AND (visible_to IS NULL OR visible_to = ? OR (visible_to = -1 AND ?))`
		args = append(args, userID, isAdmin)
	}
	query += ` ORDER BY id`
	var records []SyncLogRecord
	err := db.Raw(query, args...).Scan(&records).Error
	return records, err
}

func MaxExpiredSyncID(db *gorm.DB) (int64, error) {
	var id int64
	err := db.Model(&syncLogModel{}).
		Where("created_at < datetime('now', '-7 days')").
		Select("COALESCE(MAX(id), 0)").Scan(&id).Error
	return id, err
}

func DeleteSyncThrough(db *gorm.DB, id int64) error {
	return db.Where("id <= ?", id).Delete(&syncLogModel{}).Error
}
