package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// A club is a group of users.

type ClubRecord struct {
	ID        int64
	Name      string
	OwnerID   int64
	Locations string // JSON array of venue names; "[]" means every venue
	CreatedAt string
}

// MemberRecord is a user as other members see them.
type MemberRecord struct {
	ID      int64
	Name    string
	IsAdmin bool
}

func CreateClub(db *gorm.DB, name string, ownerID int64) (ClubRecord, error) {
	model := clubModel{Name: name, OwnerID: ownerID, Locations: "[]"}
	if err := db.Create(&model).Error; err != nil {
		return ClubRecord{}, err
	}
	return clubRecord(model), nil
}

func FindClub(db *gorm.DB, id int64) (ClubRecord, error) {
	model, err := gorm.G[clubModel](db).Where("id = ?", id).First(db.Statement.Context)
	if err != nil {
		return ClubRecord{}, err
	}
	return clubRecord(model), nil
}

func ListClubs(db *gorm.DB) ([]ClubRecord, error) {
	models, err := gorm.G[clubModel](db).Order("name").Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	return clubRecords(models), nil
}

func ListClubsForUser(db *gorm.DB, userID int64) ([]ClubRecord, error) {
	var models []clubModel
	err := db.Model(&clubModel{}).
		Joins("JOIN club_members ON club_members.club_id = clubs.id").
		Where("club_members.user_id = ?", userID).
		Order("clubs.name").
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	return clubRecords(models), nil
}

func UpdateClub(db *gorm.DB, id int64, name string, ownerID int64, locationsJSON string) error {
	return db.Model(&clubModel{}).Where("id = ?", id).Updates(map[string]any{
		"name":      name,
		"owner_id":  ownerID,
		"locations": locationsJSON,
	}).Error
}

// DeleteClub removes the club and everything that belonged to it. Callers must
// check it is empty of members first;
func DeleteClub(db *gorm.DB, id int64) error {
	if err := db.Where("club_id = ?", id).Delete(&inviteModel{}).Error; err != nil {
		return err
	}
	var pollIDs []int64
	if err := db.Model(&pollModel{}).Where("club_id = ?", id).Pluck("id", &pollIDs).Error; err != nil {
		return err
	}
	for _, pollID := range pollIDs {
		if err := DeletePoll(db, pollID); err != nil {
			return err
		}
	}
	if err := db.Where("club_id = ?", id).Delete(&clubMemberModel{}).Error; err != nil {
		return err
	}
	if err := db.Model(&userModel{}).Where("active_club_id = ?", id).
		Update("active_club_id", nil).Error; err != nil {
		return err
	}
	return db.Delete(&clubModel{}, id).Error
}

// AddClubMember is idempotent so redeeming an invite twice is harmless.
func AddClubMember(db *gorm.DB, clubID, userID int64) error {
	model := clubMemberModel{ClubID: clubID, UserID: userID}
	return db.Clauses(clause.OnConflict{DoNothing: true}).Create(&model).Error
}

func RemoveClubMember(db *gorm.DB, clubID, userID int64) error {
	if err := db.Where("club_id = ? AND user_id = ?", clubID, userID).
		Delete(&clubMemberModel{}).Error; err != nil {
		return err
	}
	// Whoever just lost their active club falls back to another one they are
	// still in, or to no club at all.
	remaining, err := ListClubsForUser(db, userID)
	if err != nil {
		return err
	}
	var next *int64
	if len(remaining) != 0 {
		next = &remaining[0].ID
	}
	return db.Model(&userModel{}).
		Where("id = ? AND active_club_id = ?", userID, clubID).
		Update("active_club_id", next).Error
}

func IsClubMember(db *gorm.DB, clubID, userID int64) (bool, error) {
	var count int64
	err := db.Model(&clubMemberModel{}).
		Where("club_id = ? AND user_id = ?", clubID, userID).Count(&count).Error
	return count != 0, err
}

// ListClubMembers returns full user rows, for the notification fan-out.
func ListClubMembers(db *gorm.DB, clubID int64) ([]UserRecord, error) {
	var models []userModel
	err := db.Model(&userModel{}).
		Joins("JOIN club_members ON club_members.user_id = users.id").
		Where("club_members.club_id = ?", clubID).
		Order("users.name").
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	users := make([]UserRecord, len(models))
	for i, model := range models {
		users[i] = userRecord(model)
	}
	return users, nil
}

func ListClubMemberIDs(db *gorm.DB, userID int64) ([]int64, error) {
	var clubIDs []int64
	err := db.Model(&clubMemberModel{}).
		Where("user_id = ?", userID).
		Order("club_id").
		Pluck("club_id", &clubIDs).Error
	return clubIDs, err
}

func ListOwnedClubIDs(db *gorm.DB, userID int64) ([]int64, error) {
	var clubIDs []int64
	err := db.Model(&clubModel{}).
		Where("owner_id = ?", userID).
		Order("id").
		Pluck("id", &clubIDs).Error
	return clubIDs, err
}

// ListVisibleMembers returns everyone who shares at least one club with the
// user, including the user themselves.
func ListVisibleMembers(db *gorm.DB, userID int64) ([]MemberRecord, error) {
	var models []userModel
	err := db.Model(&userModel{}).
		Where(`users.id IN (
			SELECT mine.user_id FROM club_members AS mine
			WHERE mine.club_id IN (SELECT club_id FROM club_members WHERE user_id = ?)
		) OR users.id = ?`, userID, userID).
		Order("users.name").
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	members := make([]MemberRecord, len(models))
	for i, model := range models {
		members[i] = MemberRecord{ID: model.ID, Name: model.Name, IsAdmin: model.IsAdmin}
	}
	return members, nil
}

func CountClubMembers(db *gorm.DB, clubID int64) (int64, error) {
	var count int64
	err := db.Model(&clubMemberModel{}).Where("club_id = ?", clubID).Count(&count).Error
	return count, err
}

func SetActiveClub(db *gorm.DB, userID int64, clubID *int64) error {
	return db.Model(&userModel{}).Where("id = ?", userID).
		Update("active_club_id", clubID).Error
}

func clubRecord(model clubModel) ClubRecord {
	return ClubRecord{
		ID: model.ID, Name: model.Name, OwnerID: model.OwnerID,
		Locations: model.Locations, CreatedAt: model.CreatedAt,
	}
}

func clubRecords(models []clubModel) []ClubRecord {
	clubs := make([]ClubRecord, len(models))
	for i, model := range models {
		clubs[i] = clubRecord(model)
	}
	return clubs
}
