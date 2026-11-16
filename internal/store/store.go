package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

var ErrNotFound = errors.New("not found")

// Store owns the SQLite connection used by the application.
type Store struct {
	db *sql.DB
}

// Open creates the data directory and opens the single-user SQLite database.
func Open(ctx context.Context, dataDir string) (*Store, error) {
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		return nil, fmt.Errorf("create data directory: %w", err)
	}

	dbPath, err := filepath.Abs(filepath.Join(dataDir, "tack.db"))
	if err != nil {
		return nil, fmt.Errorf("resolve database path: %w", err)
	}
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("prepare database file: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, err
	}
	for _, filename := range []string{dbPath, dbPath + "-wal", dbPath + "-shm"} {
		if err := os.Chmod(filename, 0o600); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("secure database file: %w", err)
		}
	}
	dsn := (&url.URL{
		Scheme:   "file",
		Path:     dbPath,
		RawQuery: "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)",
	}).String()

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return &Store{db: db}, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) Ping(ctx context.Context) error {
	return s.db.PingContext(ctx)
}
