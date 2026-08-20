package store

import (
	"errors"
	"time"

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
	session, err := gorm.G[sessionModel](db).
		Preload("User", nil).
		Where("token = ? AND expires_at > ?", tokenHash, sqliteTime(time.Now().UTC())).
		First(db.Statement.Context)
	if err != nil {
		return UserRecord{}, err
	}
	return userRecord(session.User), nil
}

func FindUserByEmail(db *gorm.DB, email string) (UserRecord, error) {
	model, err := gorm.G[userModel](db).Where("email = ?", email).First(db.Statement.Context)
	if err != nil {
		return UserRecord{}, err
	}
	return userRecord(model), nil
}

func ListUsers(db *gorm.DB) ([]UserRecord, error) {
	models, err := gorm.G[userModel](db).Order("id").Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	users := make([]UserRecord, len(models))
	for i, model := range models {
		users[i] = userRecord(model)
	}
	return users, nil
}

func ListMembers(db *gorm.DB) ([]UserRecord, error) {
	models, err := gorm.G[userModel](db).Order("name").Find(db.Statement.Context)
	if err != nil {
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
	user, err := gorm.G[userModel](db).Where("email = ?", email).First(db.Statement.Context)
	if IsNotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return db.Where(&sessionModel{UserID: user.ID}).Delete(&sessionModel{}).Error
}

func DeleteExpiredSessions(db *gorm.DB) error {
	return db.Where("expires_at <= ?", sqliteTime(time.Now().UTC())).Delete(&sessionModel{}).Error
}

func FindInvite(db *gorm.DB, token string) (InviteRecord, error) {
	model, err := gorm.G[inviteModel](db).
		Preload("UsedByUser", nil).
		Where("token = ?", token).
		First(db.Statement.Context)
	if err != nil {
		return InviteRecord{}, err
	}
	return inviteRecord(model), nil
}

func ListInvites(db *gorm.DB) ([]InviteRecord, error) {
	models, err := gorm.G[inviteModel](db).
		Preload("UsedByUser", nil).
		Order("created_at DESC").
		Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	invites := make([]InviteRecord, len(models))
	for i, model := range models {
		invites[i] = inviteRecord(model)
	}
	return invites, nil
}

// UserOrInviteEmailExists reports whether email already belongs to an account or
// to an outstanding invite.
func UserOrInviteEmailExists(db *gorm.DB, email string) (bool, error) {
	var userCount int64
	if err := db.Model(&userModel{}).Where("email = ?", email).Count(&userCount).Error; err != nil {
		return false, err
	}
	if userCount != 0 {
		return true, nil
	}
	var inviteCount int64
	if err := db.Model(&inviteModel{}).Where("email = ?", email).Count(&inviteCount).Error; err != nil {
		return false, err
	}
	return inviteCount != 0, nil
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
	model, err := gorm.G[inviteModel](db).Where("token = ?", token).First(db.Statement.Context)
	if err != nil {
		return err
	}
	model.Uses++
	fields := []string{"Uses"}
	if model.Kind != "group" {
		now := sqliteTime(time.Now().UTC())
		model.UsedBy = &userID
		model.UsedAt = &now
		fields = append(fields, "UsedBy", "UsedAt")
	}
	return db.Model(&model).Select(fields).Updates(&model).Error
}

func CreateDefaultSettings(db *gorm.DB, userID int64) error {
	return db.Create(&userSettingsModel{UserID: userID}).Error
}

func FindSettings(db *gorm.DB, userID int64) (SettingsRecord, error) {
	model, err := gorm.G[userSettingsModel](db).Where("user_id = ?", userID).First(db.Statement.Context)
	if err != nil {
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

func inviteRecord(model inviteModel) InviteRecord {
	var usedByName *string
	if model.UsedByUser != nil {
		name := model.UsedByUser.Name
		usedByName = &name
	}
	return InviteRecord{
		Token: model.Token, Kind: model.Kind, Email: model.Email, CreatedAt: model.CreatedAt,
		UsedByID: model.UsedBy, UsedByName: usedByName, UsedAt: model.UsedAt,
		Disabled: model.Disabled, Uses: model.Uses,
	}
}

func sqliteTime(value time.Time) string { return value.Format("2006-01-02 15:04:05") }

func IsNotFound(err error) bool { return errors.Is(err, gorm.ErrRecordNotFound) }
