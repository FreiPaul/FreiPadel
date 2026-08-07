package store

import (
	"database/sql"
	"fmt"

	"freipadel/internal/sessiontoken"
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store owns the GORM database and its underlying connection pool. The SQL
// handle remains private for bootstrapping and versioned schema migrations.
type Store struct {
	ORM *gorm.DB
	sql *sql.DB
}

func (s *Store) Close() error {
	return s.sql.Close()
}

func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)", path)
	orm, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{
		// Schema changes remain explicit while the query layer is migrated.
		DisableForeignKeyConstraintWhenMigrating: true,
		// Match the old database/sql layer, which only logged errors at the
		// call site.
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, err
	}
	db, err := orm.DB()
	if err != nil {
		return nil, fmt.Errorf("get sql connection pool: %w", err)
	}
	// modernc/sqlite works best with a single writer connection.
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	// The migration version is stored in meta, so this one table must exist
	// before the versioned runner can inspect the database.
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS meta (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL
	)`); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("bootstrap migration metadata: %w", err)
	}
	if err := applySchemaMigrations(db, schemaMigrations); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("migrate schema: %w", err)
	}

	if err := migrateHashSessions(orm); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("hash existing sessions: %w", err)
	}
	return &Store{ORM: orm, sql: db}, nil
}

// migrateHashSessions rewrites plaintext session tokens to their hashes once, so
// the database no longer holds directly usable tokens. Existing sessions keep
// working: the browser still sends the raw token, which we now hash before
// lookup. Guarded by a meta flag because a hashed token is indistinguishable
// from a raw one by length/format, so re-running would double-hash and log
// everyone out.
func migrateHashSessions(db *gorm.DB) error {
	var meta metaModel
	if err := db.Where(&metaModel{Key: "sessions_hashed"}).First(&meta).Error; err == nil && meta.Value == "1" {
		return nil
	} else if err != nil && !IsNotFound(err) {
		return err
	}
	var sessions []sessionModel
	if err := db.Find(&sessions).Error; err != nil {
		return err
	}
	for _, session := range sessions {
		hashed := sessiontoken.Hash(session.Token)
		if err := db.Model(&session).Update("Token", hashed).Error; err != nil {
			return err
		}
	}
	meta = metaModel{Key: "sessions_hashed", Value: "1"}
	return db.Save(&meta).Error
}
