package store

import (
	"errors"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type UserRecord struct {
	ID           int64
	Email        string
	Name         string
	PasswordHash string
	IsAdmin      bool
	CreatedAt    string
}

type InviteRecord struct {
	Token      string
	Kind       string
	Email      *string
	CreatedAt  string
	UsedByID   *int64
	UsedByName *string
	UsedAt     *string
	Disabled   bool
	Uses       int
}

type SettingsRecord struct {
	UserID        int64
	Weekdays      string
	TimeStart     string
	TimeEnd       string
	DaysAhead     int
	MinDuration   int
	Locations     string
	Notifications string
}

func CountUsers(db *gorm.DB) (int64, error) {
	var count int64
	err := db.Model(&userModel{}).Count(&count).Error
	return count, err
}

func FindUserBySession(db *gorm.DB, tokenHash string) (UserRecord, error) {
	var user UserRecord
	err := db.Table("sessions AS s").
		Select("u.id, u.email, u.name, u.is_admin").
		Joins("JOIN users AS u ON u.id = s.user_id").
		Where("s.token = ? AND s.expires_at > datetime('now')", tokenHash).
		Take(&user).Error
	return user, err
}

func FindUserByEmail(db *gorm.DB, email string) (UserRecord, error) {
	var model userModel
	if err := db.Where("email = ?", email).Take(&model).Error; err != nil {
		return UserRecord{}, err
	}
	return userRecord(model), nil
}

func ListUsers(db *gorm.DB) ([]UserRecord, error) {
	var models []userModel
	if err := db.Order("id").Find(&models).Error; err != nil {
		return nil, err
	}
	users := make([]UserRecord, len(models))
	for i, model := range models {
		users[i] = userRecord(model)
	}
	return users, nil
}

func ListMembers(db *gorm.DB) ([]UserRecord, error) {
	var models []userModel
	if err := db.Select("id", "name", "is_admin").Order("name").Find(&models).Error; err != nil {
		return nil, err
	}
	members := make([]UserRecord, len(models))
	for i, model := range models {
		members[i] = userRecord(model)
	}
	return members, nil
}

func CreateUser(db *gorm.DB, email, name, passwordHash string, isAdmin bool) (UserRecord, error) {
	model := userModel{Email: email, Name: name, PasswordHash: passwordHash, IsAdmin: isAdmin}
	if err := db.Create(&model).Error; err != nil {
		return UserRecord{}, err
	}
	return userRecord(model), nil
}

func UpdatePassword(db *gorm.DB, email, passwordHash string) (int64, error) {
	result := db.Model(&userModel{}).Where("email = ?", email).Update("password_hash", passwordHash)
	return result.RowsAffected, result.Error
}

func PromoteAdmin(db *gorm.DB, email string) (int64, error) {
	result := db.Model(&userModel{}).Where("email = ?", email).Update("is_admin", true)
	return result.RowsAffected, result.Error
}

func CreateSession(db *gorm.DB, tokenHash string, userID int64, expiresAt string) error {
	return db.Create(&sessionModel{Token: tokenHash, UserID: userID, ExpiresAt: expiresAt}).Error
}

func DeleteSession(db *gorm.DB, tokenHash string) error {
	return db.Delete(&sessionModel{}, "token = ?", tokenHash).Error
}

func DeleteSessionsForUserEmail(db *gorm.DB, email string) error {
	subquery := db.Model(&userModel{}).Select("id").Where("email = ?", email)
	return db.Where("user_id = (?)", subquery).Delete(&sessionModel{}).Error
}

func DeleteExpiredSessions(db *gorm.DB) error {
	return db.Where("expires_at <= datetime('now')").Delete(&sessionModel{}).Error
}

func FindInvite(db *gorm.DB, token string) (InviteRecord, error) {
	var invite InviteRecord
	err := db.Table("invites AS i").
		Select(`i.token, i.kind, i.email, i.created_at, i.used_by AS used_by_id,
			u.name AS used_by_name, i.used_at, i.disabled, i.uses`).
		Joins("LEFT JOIN users AS u ON u.id = i.used_by").
		Where("i.token = ?", token).Take(&invite).Error
	return invite, err
}

func ListInvites(db *gorm.DB) ([]InviteRecord, error) {
	var invites []InviteRecord
	err := db.Table("invites AS i").
		Select(`i.token, i.kind, i.email, i.created_at, i.used_by AS used_by_id,
			u.name AS used_by_name, i.used_at, i.disabled, i.uses`).
		Joins("LEFT JOIN users AS u ON u.id = i.used_by").
		Order("i.created_at DESC").Scan(&invites).Error
	return invites, err
}

func UserOrInviteEmailExists(db *gorm.DB, email string) (bool, error) {
	var exists bool
	err := db.Raw(`SELECT (EXISTS(SELECT 1 FROM users WHERE email = ?)
		OR EXISTS(SELECT 1 FROM invites WHERE email = ?))`, email, email).Scan(&exists).Error
	return exists, err
}

func CreateInvite(db *gorm.DB, token string, createdBy int64, kind string, email *string) error {
	return db.Create(&inviteModel{Token: token, CreatedBy: createdBy, Kind: kind, Email: email}).Error
}

func DisableInvite(db *gorm.DB, token string) (int64, error) {
	result := db.Model(&inviteModel{}).Where("token = ?", token).Update("disabled", true)
	return result.RowsAffected, result.Error
}

func DeleteInvite(db *gorm.DB, token string) (int64, error) {
	result := db.Where("token = ? AND (kind = 'group' OR used_by IS NULL)", token).Delete(&inviteModel{})
	return result.RowsAffected, result.Error
}

func RedeemInvite(db *gorm.DB, token string, userID int64) error {
	return db.Model(&inviteModel{}).Where("token = ?", token).Updates(map[string]any{
		"uses":    gorm.Expr("uses + 1"),
		"used_by": gorm.Expr("CASE WHEN kind = 'group' THEN used_by ELSE ? END", userID),
		"used_at": gorm.Expr("CASE WHEN kind = 'group' THEN used_at ELSE datetime('now') END"),
	}).Error
}

func CreateDefaultSettings(db *gorm.DB, userID int64) error {
	return db.Create(&userSettingsModel{UserID: userID}).Error
}

func FindSettings(db *gorm.DB, userID int64) (SettingsRecord, error) {
	var model userSettingsModel
	if err := db.Where("user_id = ?", userID).Take(&model).Error; err != nil {
		return SettingsRecord{}, err
	}
	return settingsRecord(model), nil
}

func UpsertSettings(db *gorm.DB, settings SettingsRecord) error {
	model := userSettingsModel{
		UserID: settings.UserID, Weekdays: settings.Weekdays,
		TimeStart: settings.TimeStart, TimeEnd: settings.TimeEnd,
		DaysAhead: settings.DaysAhead, MinDuration: settings.MinDuration,
		Locations: settings.Locations, Notifications: settings.Notifications,
	}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{
			"weekdays", "time_start", "time_end", "days_ahead",
			"min_duration", "locations", "notifications",
		}),
	}).Create(&model).Error
}

func userRecord(model userModel) UserRecord {
	return UserRecord{
		ID: model.ID, Email: model.Email, Name: model.Name,
		PasswordHash: model.PasswordHash, IsAdmin: model.IsAdmin, CreatedAt: model.CreatedAt,
	}
}

func settingsRecord(model userSettingsModel) SettingsRecord {
	return SettingsRecord{
		UserID: model.UserID, Weekdays: model.Weekdays,
		TimeStart: model.TimeStart, TimeEnd: model.TimeEnd,
		DaysAhead: model.DaysAhead, MinDuration: model.MinDuration,
		Locations: model.Locations, Notifications: model.Notifications,
	}
}

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
