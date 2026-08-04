package main

// These persistence models describe the existing SQLite schema. They stay
// separate from HTTP and sync wire types so database changes cannot silently
// change the API's JSON representation.

type userModel struct {
	ID           int64  `gorm:"column:id;primaryKey;autoIncrement"`
	Email        string `gorm:"column:email;not null;unique;collate:nocase"`
	Name         string `gorm:"column:name;not null"`
	PasswordHash string `gorm:"column:password_hash;not null"`
	IsAdmin      bool   `gorm:"column:is_admin;not null;default:false"`
	CreatedAt    string `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
}

func (userModel) TableName() string { return "users" }

type sessionModel struct {
	Token     string `gorm:"column:token;primaryKey"`
	UserID    int64  `gorm:"column:user_id;not null"`
	ExpiresAt string `gorm:"column:expires_at;not null"`
}

func (sessionModel) TableName() string { return "sessions" }

type inviteModel struct {
	Token     string  `gorm:"column:token;primaryKey"`
	CreatedBy int64   `gorm:"column:created_by;not null"`
	CreatedAt string  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
	UsedBy    *int64  `gorm:"column:used_by"`
	UsedAt    *string `gorm:"column:used_at"`
	Email     *string `gorm:"column:email"`
	Kind      string  `gorm:"column:kind;not null;default:single"`
	Disabled  bool    `gorm:"column:disabled;not null;default:false"`
	Uses      int     `gorm:"column:uses;not null;default:0"`
}

func (inviteModel) TableName() string { return "invites" }

type userSettingsModel struct {
	UserID        int64  `gorm:"column:user_id;primaryKey"`
	Weekdays      string `gorm:"column:weekdays;not null;default:[0,1,2,3,4]"`
	TimeStart     string `gorm:"column:time_start;not null;default:19:00"`
	TimeEnd       string `gorm:"column:time_end;not null;default:21:00"`
	DaysAhead     int    `gorm:"column:days_ahead;not null;default:10"`
	MinDuration   int    `gorm:"column:min_duration;not null;default:60"`
	Locations     string `gorm:"column:locations;not null;default:[]"`
	Notifications string `gorm:"column:notifications;not null;default:{}"`
}

func (userSettingsModel) TableName() string { return "user_settings" }

type slotModel struct {
	ID              int64   `gorm:"column:id;primaryKey;autoIncrement"`
	Source          string  `gorm:"column:source;not null"`
	Location        string  `gorm:"column:location;not null"`
	Court           string  `gorm:"column:court;not null"`
	Date            string  `gorm:"column:date;not null;index:idx_slots_date"`
	Time            string  `gorm:"column:time;not null"`
	DurationMinutes int     `gorm:"column:duration_minutes;not null"`
	Price           float64 `gorm:"column:price;not null;default:0"`
	Currency        string  `gorm:"column:currency;not null;default:EUR"`
}

func (slotModel) TableName() string { return "slots" }

type metaModel struct {
	Key   string `gorm:"column:key;primaryKey"`
	Value string `gorm:"column:value;not null"`
}

func (metaModel) TableName() string { return "meta" }

type pollModel struct {
	ID            int64   `gorm:"column:id;primaryKey;autoIncrement"`
	CreatorID     int64   `gorm:"column:creator_id;not null"`
	Title         string  `gorm:"column:title;not null"`
	Status        string  `gorm:"column:status;not null;default:active"`
	WinningSlotID *int64  `gorm:"column:winning_slot_id"`
	CreatedAt     string  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
	ClosedAt      *string `gorm:"column:closed_at"`
}

func (pollModel) TableName() string { return "polls" }

type pollSlotModel struct {
	ID              int64   `gorm:"column:id;primaryKey;autoIncrement"`
	PollID          int64   `gorm:"column:poll_id;not null;index:idx_poll_slots_poll"`
	Date            string  `gorm:"column:date;not null"`
	Time            string  `gorm:"column:time;not null"`
	DurationMinutes int     `gorm:"column:duration_minutes;not null"`
	Location        string  `gorm:"column:location;not null"`
	Court           string  `gorm:"column:court;not null;default:''"`
	Price           float64 `gorm:"column:price;not null;default:0"`
	Currency        string  `gorm:"column:currency;not null;default:EUR"`
}

func (pollSlotModel) TableName() string { return "poll_slots" }

type voteModel struct {
	PollSlotID int64  `gorm:"column:poll_slot_id;primaryKey"`
	UserID     int64  `gorm:"column:user_id;primaryKey"`
	Vote       bool   `gorm:"column:vote;not null"`
	UpdatedAt  string `gorm:"column:updated_at;not null;default:CURRENT_TIMESTAMP"`
}

func (voteModel) TableName() string { return "votes" }

type syncLogModel struct {
	ID        int64   `gorm:"column:id;primaryKey;autoIncrement"`
	Entity    string  `gorm:"column:entity;not null"`
	EntityID  string  `gorm:"column:entity_id;not null"`
	Action    string  `gorm:"column:action;not null"`
	Payload   *string `gorm:"column:payload"`
	VisibleTo *int64  `gorm:"column:visible_to"`
	CreatedAt string  `gorm:"column:created_at;not null;default:CURRENT_TIMESTAMP"`
}

func (syncLogModel) TableName() string { return "sync_log" }
