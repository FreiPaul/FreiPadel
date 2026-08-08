package store

import (
	"sort"

	"gorm.io/gorm"
)

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
	models, err := gorm.G[slotModel](db).
		Where("date >= ? AND date <= ?", filter.MinDate, filter.MaxDate).
		Where("time >= ? AND time <= ?", filter.TimeStart, filter.TimeEnd).
		Where("duration_minutes >= ?", filter.MinDuration).
		Where("court NOT LIKE ?", "%single%").
		Order("date, time, location, duration_minutes").
		Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}

	type groupKey struct {
		date, time, location, source, currency string
		duration                               int
	}
	indices := make(map[groupKey]int)
	groups := make([]SlotGroupRecord, 0, len(models))
	for _, model := range models {
		if model.Date == filter.MinDate && model.Time <= filter.NowTime {
			continue
		}
		key := groupKey{
			date: model.Date, time: model.Time, duration: model.DurationMinutes,
			location: model.Location, source: model.Source, currency: model.Currency,
		}
		if index, ok := indices[key]; ok {
			if model.Price < groups[index].MinPrice {
				groups[index].MinPrice = model.Price
			}
			groups[index].Courts += "|" + model.Court
			continue
		}
		indices[key] = len(groups)
		groups = append(groups, SlotGroupRecord{
			Date: model.Date, Time: model.Time, DurationMinutes: model.DurationMinutes,
			Location: model.Location, Source: model.Source, Currency: model.Currency,
			MinPrice: model.Price, Courts: model.Court,
		})
	}
	return groups, nil
}

func ListLocations(db *gorm.DB) ([]string, error) {
	models, err := gorm.G[slotModel](db).Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	unique := make(map[string]struct{}, len(models))
	for _, model := range models {
		unique[model.Location] = struct{}{}
	}
	locations := make([]string, 0, len(unique))
	for location := range unique {
		locations = append(locations, location)
	}
	sort.Strings(locations)
	return locations, nil
}

func ListSlotAvailability(db *gorm.DB) ([]SlotRecord, error) {
	models, err := gorm.G[slotModel](db).Find(db.Statement.Context)
	if err != nil {
		return nil, err
	}
	type availabilityKey struct {
		date, time, location string
		duration             int
	}
	seen := make(map[availabilityKey]struct{}, len(models))
	slots := make([]SlotRecord, 0, len(models))
	for _, model := range models {
		key := availabilityKey{
			date: model.Date, time: model.Time,
			duration: model.DurationMinutes, location: model.Location,
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		slots = append(slots, SlotRecord{
			Date: model.Date, Time: model.Time, DurationMinutes: model.DurationMinutes,
			Location: model.Location,
		})
	}
	return slots, nil
}
