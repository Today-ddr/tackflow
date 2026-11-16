package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestOpenMigrateAndBootstrapAdmin(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("first Migrate() error = %v", err)
	}
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("second Migrate() error = %v", err)
	}

	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 1 {
		t.Errorf("SchemaVersion() = %d, want 1", version)
	}

	var journalMode string
	if err := st.db.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %q, want wal", journalMode)
	}

	var foreignKeys int
	if err := st.db.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		t.Fatalf("read foreign_keys: %v", err)
	}
	if foreignKeys != 1 {
		t.Errorf("foreign_keys = %d, want 1", foreignKeys)
	}

	var busyTimeout int
	if err := st.db.QueryRowContext(ctx, "PRAGMA busy_timeout").Scan(&busyTimeout); err != nil {
		t.Fatalf("read busy_timeout: %v", err)
	}
	if busyTimeout != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busyTimeout)
	}

	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	created, err := st.BootstrapAdmin(ctx, "admin", "hash-one", now)
	if err != nil {
		t.Fatalf("BootstrapAdmin(first) error = %v", err)
	}
	if !created {
		t.Error("BootstrapAdmin(first) = false, want true")
	}

	created, err = st.BootstrapAdmin(ctx, "other", "hash-two", now.Add(time.Hour))
	if err != nil {
		t.Fatalf("BootstrapAdmin(second) error = %v", err)
	}
	if created {
		t.Error("BootstrapAdmin(second) = true, want false")
	}

	user, err := st.UserByUsername(ctx, "admin")
	if err != nil {
		t.Fatalf("UserByUsername() error = %v", err)
	}
	if user.PasswordHash != "hash-one" {
		t.Errorf("PasswordHash = %q, want hash-one", user.PasswordHash)
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tack.db")); err != nil {
		t.Errorf("tack.db not created: %v", err)
	}
}

func TestSessionLifecycle(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := st.BootstrapAdmin(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}

	expiresAt := now.Add(30 * 24 * time.Hour)
	if err := st.CreateSession(ctx, "session-one", 1, now, expiresAt, "test-agent", "127.0.0.1"); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	session, err := st.SessionByID(ctx, "session-one", now.Add(time.Minute))
	if err != nil {
		t.Fatalf("SessionByID() error = %v", err)
	}
	if session.UserID != 1 {
		t.Errorf("UserID = %d, want 1", session.UserID)
	}
	if !session.ExpiresAt.Equal(expiresAt) {
		t.Errorf("ExpiresAt = %v, want %v", session.ExpiresAt, expiresAt)
	}

	newExpiry := now.Add(31 * 24 * time.Hour)
	if err := st.TouchSession(ctx, "session-one", now.Add(2*time.Minute), newExpiry); err != nil {
		t.Fatalf("TouchSession() error = %v", err)
	}
	session, err = st.SessionByID(ctx, "session-one", now.Add(3*time.Minute))
	if err != nil {
		t.Fatalf("SessionByID(after touch) error = %v", err)
	}
	if !session.ExpiresAt.Equal(newExpiry) {
		t.Errorf("ExpiresAt after touch = %v, want %v", session.ExpiresAt, newExpiry)
	}

	if err := st.DeleteSession(ctx, "session-one"); err != nil {
		t.Fatalf("DeleteSession() error = %v", err)
	}
	if _, err := st.SessionByID(ctx, "session-one", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SessionByID(after delete) error = %v, want ErrNotFound", err)
	}
}

func TestSessionByIDRejectsExpiredSession(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := st.BootstrapAdmin(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if err := st.CreateSession(ctx, "expired", 1, now, now.Add(-time.Minute), "", ""); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if _, err := st.SessionByID(ctx, "expired", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("SessionByID(expired) error = %v, want ErrNotFound", err)
	}
}
