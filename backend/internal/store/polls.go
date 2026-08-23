package store

import (
	"sort"
	"time"

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
	models, err := gorm.G[pollModel](db).
		Preload("Creator", nil).
		Order("created_at DESC").
		Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	// Keep active polls first without encoding application status rules in SQL.
	sort.SliceStable(models, func(i, j int) bool {
		return models[i].Status == "active" && models[j].Status != "active"
	})
	polls := make([]PollRecord, len(models))
	for i, model := range models {
		polls[i] = pollRecord(model)
	}
	return polls, nil
}

func FindPoll(db *gorm.DB, id int64) (PollRecord, error) {
	model, err := gorm.G[pollModel](db).
		Preload("Creator", nil).
		Where("id = ?", id).
		First(db.Statement.Context)
	if err != nil {
		return PollRecord{}, err
	}
	return pollRecord(model), nil
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
	models, err := gorm.G[voteModel](db).
		Preload("User", nil).
		Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	sort.Slice(models, func(i, j int) bool { return models[i].User.Name < models[j].User.Name })
	votes := make([]VoteRecord, len(models))
	for i, model := range models {
		votes[i] = VoteRecord{
			PollSlotID: model.PollSlotID, UserID: model.UserID,
			Name: model.User.Name, Vote: model.Vote,
		}
	}
	return votes, nil
}

func ListVotesForPoll(db *gorm.DB, pollID int64) ([]VoteRecord, error) {
	var models []voteModel
	err := db.Model(&voteModel{}).
		Joins("JOIN poll_slots ON poll_slots.id = votes.poll_slot_id").
		Where("poll_slots.poll_id = ?", pollID).
		Find(&models).Error
	if err != nil {
		return nil, err
	}
	votes := make([]VoteRecord, len(models))
	for i, model := range models {
		votes[i] = VoteRecord{
			PollSlotID: model.PollSlotID, UserID: model.UserID, Vote: model.Vote,
		}
	}
	return votes, nil
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
	model, err := gorm.G[pollSlotModel](db).
		Preload("Poll", nil).
		Where("id = ?", slotID).
		First(db.Statement.Context)
	return model.PollID, model.Poll.Status, err
}

func DeleteVote(db *gorm.DB, pollSlotID, userID int64) error {
	return db.Where("poll_slot_id = ? AND user_id = ?", pollSlotID, userID).Delete(&voteModel{}).Error
}

func UpsertVote(db *gorm.DB, pollSlotID, userID int64, vote bool) error {
	model := voteModel{
		PollSlotID: pollSlotID, UserID: userID, Vote: vote,
		UpdatedAt: sqliteTime(time.Now().UTC()),
	}
	return db.Clauses(clause.OnConflict{
		Columns:   []clause.Column{{Name: "poll_slot_id"}, {Name: "user_id"}},
		DoUpdates: clause.AssignmentColumns([]string{"vote", "updated_at"}),
	}).Create(&model).Error
}

func PollSlotBelongsTo(db *gorm.DB, slotID, pollID int64) (bool, error) {
	var count int64
	err := db.Model(&pollSlotModel{}).Where("id = ? AND poll_id = ?", slotID, pollID).Count(&count).Error
	return count != 0, err
}

func ClosePoll(db *gorm.DB, pollID int64, winningSlotID *int64) error {
	model, err := gorm.G[pollModel](db).Where("id = ?", pollID).First(db.Statement.Context)
	if err != nil {
		return err
	}
	now := sqliteTime(time.Now().UTC())
	model.Status = "closed"
	model.WinningSlotID = winningSlotID
	model.ClosedAt = &now
	return db.Model(&model).Select("Status", "WinningSlotID", "ClosedAt").Updates(&model).Error
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

func pollRecord(model pollModel) PollRecord {
	return PollRecord{
		ID: model.ID, Title: model.Title, CreatorID: model.CreatorID,
		CreatorName: model.Creator.Name, Status: model.Status,
		WinningSlotID: model.WinningSlotID, CreatedAt: model.CreatedAt, ClosedAt: model.ClosedAt,
	}
}
