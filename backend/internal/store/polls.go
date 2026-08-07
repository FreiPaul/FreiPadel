package store

import (
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type PollRecord struct {
	ID            int64
	Title         string
	CreatorID     int64
	CreatorName   string
	Status        string
	WinningSlotID *int64
	CreatedAt     string
	ClosedAt      *string
}

type PollSlotRecord struct {
	ID              int64
	PollID          int64
	Date            string
	Time            string
	DurationMinutes int
	Location        string
	Court           string
	Price           float64
	Currency        string
}

type VoteRecord struct {
	PollSlotID int64
	UserID     int64
	Name       string
	Vote       bool
}

func ListPolls(db *gorm.DB) ([]PollRecord, error) {
	var polls []PollRecord
	err := db.Table("polls AS p").
		Select(`p.id, p.title, p.creator_id, u.name AS creator_name, p.status,
			p.winning_slot_id, p.created_at, p.closed_at`).
		Joins("JOIN users AS u ON u.id = p.creator_id").
		Order("p.status = 'active' DESC, p.created_at DESC").Scan(&polls).Error
	return polls, err
}

func FindPoll(db *gorm.DB, id int64) (PollRecord, error) {
	var poll PollRecord
	err := db.Table("polls AS p").
		Select(`p.id, p.title, p.creator_id, u.name AS creator_name, p.status,
			p.winning_slot_id, p.created_at, p.closed_at`).
		Joins("JOIN users AS u ON u.id = p.creator_id").
		Where("p.id = ?", id).Take(&poll).Error
	return poll, err
}

func ListPollSlots(db *gorm.DB, pollID *int64) ([]PollSlotRecord, error) {
	query := db.Model(&pollSlotModel{}).Order("date, time, location, duration_minutes")
	if pollID != nil {
		query = query.Where("poll_id = ?", *pollID)
	}
	var models []pollSlotModel
	if err := query.Find(&models).Error; err != nil {
		return nil, err
	}
	slots := make([]PollSlotRecord, len(models))
	for i, model := range models {
		slots[i] = pollSlotRecord(model)
	}
	return slots, nil
}

func ListVotes(db *gorm.DB) ([]VoteRecord, error) {
	var votes []VoteRecord
	err := db.Table("votes AS v").
		Select("v.poll_slot_id, v.user_id, u.name, v.vote").
		Joins("JOIN users AS u ON u.id = v.user_id").Order("u.name").Scan(&votes).Error
	return votes, err
}

func CreatePoll(db *gorm.DB, creatorID int64, title string, slots []PollSlotRecord) (int64, error) {
	poll := pollModel{CreatorID: creatorID, Title: title}
	if err := db.Create(&poll).Error; err != nil {
		return 0, err
	}
	models := make([]pollSlotModel, len(slots))
	for i, slot := range slots {
		models[i] = pollSlotModel{
			PollID: poll.ID, Date: slot.Date, Time: slot.Time,
			DurationMinutes: slot.DurationMinutes, Location: slot.Location,
			Court: slot.Court, Price: slot.Price, Currency: slot.Currency,
		}
	}
	if len(models) != 0 {
		if err := db.Create(&models).Error; err != nil {
			return 0, err
		}
	}
	return poll.ID, nil
}

func FindPollForSlot(db *gorm.DB, slotID int64) (int64, string, error) {
	var result struct {
		PollID int64
		Status string
	}
	err := db.Table("poll_slots AS ps").Select("ps.poll_id, p.status").
		Joins("JOIN polls AS p ON p.id = ps.poll_id").Where("ps.id = ?", slotID).Take(&result).Error
	return result.PollID, result.Status, err
}

func DeleteVote(db *gorm.DB, pollSlotID, userID int64) error {
	return db.Where("poll_slot_id = ? AND user_id = ?", pollSlotID, userID).Delete(&voteModel{}).Error
}

func UpsertVote(db *gorm.DB, pollSlotID, userID int64, vote bool) error {
	model := voteModel{PollSlotID: pollSlotID, UserID: userID, Vote: vote}
	return db.Clauses(clause.OnConflict{
		Columns: []clause.Column{{Name: "poll_slot_id"}, {Name: "user_id"}},
		DoUpdates: clause.Assignments(map[string]any{
			"vote": vote, "updated_at": gorm.Expr("datetime('now')"),
		}),
	}).Create(&model).Error
}

func PollSlotBelongsTo(db *gorm.DB, slotID, pollID int64) (bool, error) {
	var count int64
	err := db.Model(&pollSlotModel{}).Where("id = ? AND poll_id = ?", slotID, pollID).Count(&count).Error
	return count != 0, err
}

func ClosePoll(db *gorm.DB, pollID int64, winningSlotID *int64) error {
	return db.Model(&pollModel{}).Where("id = ?", pollID).Updates(map[string]any{
		"status": "closed", "winning_slot_id": winningSlotID, "closed_at": gorm.Expr("datetime('now')"),
	}).Error
}

func DeletePoll(db *gorm.DB, pollID int64) error {
	return db.Delete(&pollModel{}, pollID).Error
}

func pollSlotRecord(model pollSlotModel) PollSlotRecord {
	return PollSlotRecord{
		ID: model.ID, PollID: model.PollID, Date: model.Date, Time: model.Time,
		DurationMinutes: model.DurationMinutes, Location: model.Location,
		Court: model.Court, Price: model.Price, Currency: model.Currency,
	}
}
