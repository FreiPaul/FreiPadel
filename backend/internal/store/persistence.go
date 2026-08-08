package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// GetMeta reads an application metadata value. A missing key returns an empty
// string, matching the behavior of the original database/sql helper.
func (s *Store) GetMeta(key string) string {
	var meta metaModel
	if err := s.ORM.First(&meta, "key = ?", key).Error; err != nil {
		return ""
	}
	return meta.Value
}

// SetMeta inserts or replaces an application metadata value.
func (s *Store) SetMeta(key, value string) error {
	meta := metaModel{Key: key, Value: value}
	return s.ORM.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "key"}},
		DoUpdates: clause.AssignmentColumns([]string{"value"}),
	}).Create(&meta).Error
}

// AppendSync records a delta using tx. Callers must pass the same transaction
// used for the domain mutation so both writes commit or roll back together.
// A nil payload is stored as SQL NULL; visibleTo 0 means visible to everyone.
func AppendSync(tx *gorm.DB, entity, entityID, action string, payload []byte, visibleTo int64) error {
	var storedPayload *string
	if payload != nil {
		value := string(payload)
		storedPayload = &value
	}
	var storedVisibility *int64
	if visibleTo != 0 {
		storedVisibility = &visibleTo
	}
	return tx.Create(&syncLogModel{
		Entity:    entity,
		EntityID:  entityID,
		Action:    action,
		Payload:   storedPayload,
		VisibleTo: storedVisibility,
	}).Error
}
