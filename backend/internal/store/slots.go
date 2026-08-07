package store

import "gorm.io/gorm"

type SlotRecord struct {
	ID              int64
	Source          string
	Location        string
	Court           string
	Date            string
	Time            string
	DurationMinutes int
	Price           float64
	Currency        string
}

type SlotGroupRecord struct {
	Date            string
	Time            string
	DurationMinutes int
	Location        string
	Source          string
	Currency        string
	MinPrice        float64
	Courts          string
}

type SlotFilter struct {
	MinDate     string
	MaxDate     string
	TimeStart   string
	TimeEnd     string
	MinDuration int
	NowTime     string
}

func ReplaceSlots(db *gorm.DB, slots []SlotRecord) error {
	if err := db.Session(&gorm.Session{AllowGlobalUpdate: true}).Delete(&slotModel{}).Error; err != nil {
		return err
	}
	if len(slots) == 0 {
		return nil
	}
	models := make([]slotModel, len(slots))
	for i, slot := range slots {
		models[i] = slotModel{
			Source: slot.Source, Location: slot.Location, Court: slot.Court,
			Date: slot.Date, Time: slot.Time, DurationMinutes: slot.DurationMinutes,
			Price: slot.Price, Currency: slot.Currency,
		}
	}
	return db.CreateInBatches(models, 200).Error
}

func ListSlotGroups(db *gorm.DB, filter SlotFilter) ([]SlotGroupRecord, error) {
	var groups []SlotGroupRecord
	err := db.Raw(`
		SELECT date, time, duration_minutes, location, source, currency,
		       MIN(price) AS min_price,
		       GROUP_CONCAT(court, '|') AS courts
		FROM slots
		WHERE date >= ? AND date <= ?
		  AND time >= ? AND time <= ?
		  AND duration_minutes >= ?
		  AND court NOT LIKE '%single%'
		  AND NOT (date = ? AND time <= ?)
		GROUP BY date, time, duration_minutes, location, source, currency
		ORDER BY date, time, location, duration_minutes`,
		filter.MinDate, filter.MaxDate, filter.TimeStart, filter.TimeEnd,
		filter.MinDuration, filter.MinDate, filter.NowTime).Scan(&groups).Error
	return groups, err
}

func ListLocations(db *gorm.DB) ([]string, error) {
	var locations []string
	err := db.Model(&slotModel{}).Distinct("location").Order("location").Pluck("location", &locations).Error
	return locations, err
}

func ListSlotAvailability(db *gorm.DB) ([]SlotRecord, error) {
	var slots []SlotRecord
	err := db.Model(&slotModel{}).
		Distinct("date", "time", "duration_minutes", "location").
		Find(&slots).Error
	return slots, err
}
