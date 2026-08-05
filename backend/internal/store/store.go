package store

import (
	"database/sql"
	"fmt"

	"freipadel/internal/sessiontoken"
	"github.com/libtnb/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Store exposes GORM and database/sql views of the same connection pool.
// The SQL handle is temporary compatibility scaffolding while callers are
// migrated to GORM feature by feature.
type Store struct {
	ORM *gorm.DB
	SQL *sql.DB
}

func (s *Store) Close() error {
	return s.SQL.Close()
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

	if err := migrateHashSessions(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("hash existing sessions: %w", err)
	}
	return &Store{ORM: orm, SQL: db}, nil
}

// migrateHashSessions rewrites plaintext session tokens to their hashes once, so
// the database no longer holds directly usable tokens. Existing sessions keep
// working: the browser still sends the raw token, which we now hash before
// lookup. Guarded by a meta flag because a hashed token is indistinguishable
// from a raw one by length/format, so re-running would double-hash and log
// everyone out.
func migrateHashSessions(db *sql.DB) error {
	if getMeta(db, "sessions_hashed") == "1" {
		return nil
	}
	rows, err := db.Query(`SELECT token FROM sessions`)
	if err != nil {
		return err
	}
	var tokens []string
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			rows.Close()
			return err
		}
		tokens = append(tokens, t)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	for _, t := range tokens {
		if _, err := db.Exec(`UPDATE sessions SET token = ? WHERE token = ?`, sessiontoken.Hash(t), t); err != nil {
			return err
		}
	}
	return setMeta(db, "sessions_hashed", "1")
}

func getMeta(db *sql.DB, key string) string {
	var v string
	_ = db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&v)
	return v
}

func setMeta(db *sql.DB, key, value string) error {
	_, err := db.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

// GetMeta reads an application metadata value. A missing key returns an empty
// string, matching the behavior of the compatibility layer it replaces.
func (s *Store) GetMeta(key string) string {
	return getMeta(s.SQL, key)
}

// SetMeta inserts or replaces an application metadata value.
func (s *Store) SetMeta(key, value string) error {
	return setMeta(s.SQL, key, value)
}
