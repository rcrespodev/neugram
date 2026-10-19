package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"
)

// SchemaVersion is the value of meta.schema_version for the DDL in schema.sql.
const SchemaVersion = "1"

// TimeLayout is the ISO 8601 UTC form every timestamp column holds as text.
const TimeLayout = "2006-01-02T15:04:05Z"

const (
	walAttempts   = 100
	walRetryDelay = 20 * time.Millisecond
)

// Store is the only owner of the SQLite connection.
type Store struct {
	db  *sql.DB
	now func() time.Time
}

// DefaultPath returns ~/.neugram/neugram.db.
func DefaultPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".neugram", "neugram.db"), nil
}

// Open creates the parent directory, opens the database in WAL mode with
// foreign keys on, and applies the DDL when the file is new.
func Open(path string) (*Store, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o700); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", dsn(abs))
	if err != nil {
		return nil, err
	}
	s := &Store{db: db, now: time.Now}
	if err := s.enableWAL(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.migrate(context.Background()); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func dsn(abs string) string {
	u := url.URL{Scheme: "file", Path: abs}
	return u.String() +
		"?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
}

// enableWAL switches the file to WAL, which persists in the file itself.
// SQLite does not run the busy handler for this switch, so a concurrent
// first open is retried here.
func (s *Store) enableWAL(ctx context.Context) error {
	var err error
	for range walAttempts {
		var mode string
		if err = s.db.QueryRowContext(ctx, `PRAGMA journal_mode = WAL`).Scan(&mode); err == nil {
			if mode != "wal" {
				return fmt.Errorf("store: journal_mode is %s, want wal", mode)
			}
			return nil
		}
		if !isBusy(err) {
			return err
		}
		time.Sleep(walRetryDelay)
	}
	return err
}

func isBusy(err error) bool {
	var se *sqlite.Error
	return errors.As(err, &se) && (se.Code()&0xff == sqlite3.SQLITE_BUSY || se.Code()&0xff == sqlite3.SQLITE_LOCKED)
}

// Close releases the connection pool.
func (s *Store) Close() error { return s.db.Close() }

// migrate checks and creates the schema inside one IMMEDIATE transaction, so
// two processes opening a new file at once apply the DDL exactly once.
func (s *Store) migrate(ctx context.Context) error {
	return s.withTx(ctx, func(tx *sql.Tx) error {
		var hasMeta, tables int
		err := tx.QueryRowContext(ctx, `SELECT
			  (SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'meta'),
			  (SELECT count(*) FROM sqlite_master WHERE type = 'table')`).Scan(&hasMeta, &tables)
		if err != nil {
			return err
		}
		if hasMeta == 0 {
			if tables > 0 {
				return fmt.Errorf("store: database has tables but no meta table")
			}
			if _, err := tx.ExecContext(ctx, DDL); err != nil {
				return err
			}
			_, err := tx.ExecContext(ctx, `INSERT INTO meta(key, value) VALUES ('schema_version', ?)`, SchemaVersion)
			return err
		}
		var version string
		err = tx.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = 'schema_version'`).Scan(&version)
		if err == sql.ErrNoRows {
			return fmt.Errorf("store: meta has no schema_version")
		}
		if err != nil {
			return err
		}
		if version != SchemaVersion {
			return fmt.Errorf("store: schema_version %s, this build expects %s", version, SchemaVersion)
		}
		return nil
	})
}

func (s *Store) withTx(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) timestamp() string { return s.now().UTC().Format(TimeLayout) }

func newSyncID(prefix string) string {
	b := make([]byte, 16)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}
