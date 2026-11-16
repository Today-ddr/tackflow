package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/today/tack/migrations"
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
	if version != 9 {
		t.Errorf("SchemaVersion() = %d, want 9", version)
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
	if info, err := os.Stat(filepath.Join(dataDir, "tack.db")); err != nil {
		t.Errorf("tack.db not created: %v", err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("database permissions = %o, want 600", info.Mode().Perm())
	}
}

func TestMigrateUpgradesV1Database(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	st, err := Open(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })

	if _, err := st.db.ExecContext(ctx, `
		CREATE TABLE schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TEXT NOT NULL
		)
	`); err != nil {
		t.Fatalf("create schema_migrations: %v", err)
	}
	body, err := migrations.FS.ReadFile("0001_init.sql")
	if err != nil {
		t.Fatalf("read migration: %v", err)
	}
	if err := st.applyMigration(ctx, 1, string(body)); err != nil {
		t.Fatalf("apply v1 migration: %v", err)
	}

	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := st.BootstrapAdmin(ctx, "admin", "hash", now); err != nil {
		t.Fatalf("BootstrapAdmin() error = %v", err)
	}
	if err := st.CreateSession(ctx, "existing", 1, now, now.Add(time.Hour), "", ""); err != nil {
		t.Fatalf("CreateSession() error = %v", err)
	}

	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	version, err := st.SchemaVersion(ctx)
	if err != nil {
		t.Fatalf("SchemaVersion() error = %v", err)
	}
	if version != 9 {
		t.Fatalf("SchemaVersion() = %d, want 9", version)
	}
	if _, err := st.SessionByID(ctx, "existing", now); err != nil {
		t.Fatalf("existing session lost after migration: %v", err)
	}
	if err := st.SetSessionSudo(ctx, "existing", now.Add(5*time.Minute)); err != nil {
		t.Fatalf("SetSessionSudo() after migration: %v", err)
	}
}

func TestMigratePreservesExistingUnrestrictedToken(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.ExecContext(t.Context(), "CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"0001_init.sql", "0002_m1_core.sql"} {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(t.Context(), index+1, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.ExecContext(t.Context(), `INSERT INTO api_token(name,prefix,hash,scopes,created_at) VALUES ('legacy','tk_old','hash','["read"]','2026-11-16T09:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	token, err := st.APITokenByHash(t.Context(), "hash")
	if err != nil || token.Name != "legacy" || len(token.ProjectIDs) != 0 || len(token.TagIDs) != 0 {
		t.Fatalf("migration changed existing token: %+v, %v", token, err)
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

func TestBackupCreatesReadableCopy(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	dataDir := t.TempDir()
	st, err := Open(ctx, dataDir)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}

	destination := filepath.Join(dataDir, "backups", "manual.db")
	if err := st.Backup(ctx, destination); err != nil {
		t.Fatalf("Backup() error = %v", err)
	}
	if info, err := os.Stat(destination); err != nil || info.Size() == 0 {
		t.Fatalf("backup file is empty or missing: info=%v err=%v", info, err)
	} else if info.Mode().Perm() != 0o600 {
		t.Errorf("backup permissions = %o, want 600", info.Mode().Perm())
	}
}
