package store

import (
	"bytes"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/today/tack/migrations"
)

func TestPasskeyMigrationPreservesExistingUserSessionAndTask(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.ExecContext(t.Context(), `CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index, filename := range []string{"0001_init.sql", "0002_m1_core.sql"} {
		body, err := migrations.FS.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(t.Context(), index+1, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := st.BootstrapAdmin(t.Context(), "admin", "existing-password-hash", now); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(t.Context(), "existing-session", 1, now, now.Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(t.Context(), `INSERT INTO task(title,status,position,created_at,updated_at) VALUES ('existing task','todo',1,?,?)`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	user, err := st.UserByID(t.Context(), 1)
	if err != nil || user.PasswordHash != "existing-password-hash" {
		t.Fatalf("existing user changed: %+v, %v", user, err)
	}
	if _, err := st.SessionByID(t.Context(), "existing-session", now); err != nil {
		t.Fatalf("existing session lost: %v", err)
	}
	var title string
	if err := st.db.QueryRowContext(t.Context(), `SELECT title FROM task WHERE id=1`).Scan(&title); err != nil || title != "existing task" {
		t.Fatalf("existing task changed: %q, %v", title, err)
	}
	for _, table := range []string{"passkey", "passkey_challenge"} {
		var count int
		if err := st.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatalf("missing passkey schema %s: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("new %s table not empty: %d", table, count)
		}
	}
}

func TestPasskeyPersistenceAndConcurrentCredentialUpdate(t *testing.T) {
	directory := t.TempDir()
	st, err := Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := st.BootstrapAdmin(t.Context(), "admin", "existing-hash", now); err != nil {
		t.Fatal(err)
	}
	credential := webauthn.Credential{
		ID: []byte("credential-id"), PublicKey: []byte("public-key"),
		Transport:     []protocol.AuthenticatorTransport{protocol.Internal},
		Authenticator: webauthn.Authenticator{SignCount: 1},
	}
	if err := st.CreatePasskey(t.Context(), Passkey{ID: "public-id", UserID: 1, Name: "Device", Credential: credential, CreatedAt: now}); err != nil {
		t.Fatal(err)
	}
	challenge := PasskeyChallenge{ID: "challenge", Purpose: "login", UserID: 1, Binding: "hashed-binding", SessionData: []byte(`{"challenge":"stored"}`), ExpiresAt: now.Add(5 * time.Minute)}
	if err := st.SavePasskeyChallenge(t.Context(), challenge, now); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	st, err = Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	passkeys, err := st.ListPasskeys(t.Context(), 1)
	if err != nil || len(passkeys) != 1 || !bytes.Equal(passkeys[0].Credential.PublicKey, credential.PublicKey) {
		t.Fatalf("credential did not survive reopen: %+v, %v", passkeys, err)
	}
	if _, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "register", "hashed-binding", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong purpose accepted: %v", err)
	}
	if _, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "login", "wrong-browser", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("wrong browser accepted: %v", err)
	}
	consumed, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "login", "hashed-binding", now)
	if err != nil || !bytes.Equal(consumed.SessionData, challenge.SessionData) {
		t.Fatalf("challenge did not survive reopen: %+v, %v", consumed, err)
	}
	if _, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "login", "hashed-binding", now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("challenge replay accepted: %v", err)
	}
	credential.Authenticator.SignCount = 2
	if err := st.UpdatePasskeyUsage(t.Context(), passkeys[0], credential, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	credential.Authenticator.SignCount = 1
	if err := st.UpdatePasskeyUsage(t.Context(), passkeys[0], credential, now.Add(time.Second)); !errors.Is(err, ErrNotFound) {
		t.Fatalf("stale credential overwrite accepted: %v", err)
	}
	updated, err := st.ListPasskeys(t.Context(), 1)
	if err != nil || updated[0].Credential.Authenticator.SignCount != 2 || updated[0].LastUsedAt == nil {
		t.Fatalf("usage not persisted: %+v, %v", updated, err)
	}
	if err := st.DeletePasskey(t.Context(), 1, "public-id"); err != nil {
		t.Fatal(err)
	}
	if err := st.UpdatePasskeyUsage(t.Context(), updated[0], credential, now); !errors.Is(err, ErrNotFound) {
		t.Fatalf("revoked credential resurrected: %v", err)
	}
}

func TestPasskeyChallengeConcurrentConsumptionAndExpiryCleanup(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if _, err := st.BootstrapAdmin(t.Context(), "admin", "existing-hash", now); err != nil {
		t.Fatal(err)
	}
	challenge := PasskeyChallenge{ID: "challenge", Purpose: "login", UserID: 1, Binding: "binding", SessionData: []byte(`{}`), ExpiresAt: now.Add(5 * time.Minute)}
	if err := st.SavePasskeyChallenge(t.Context(), challenge, now); err != nil {
		t.Fatal(err)
	}
	var workers sync.WaitGroup
	results := make(chan error, 8)
	for attempt := 0; attempt < 8; attempt++ {
		workers.Go(func() {
			_, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "login", "binding", now)
			results <- err
		})
	}
	workers.Wait()
	close(results)
	successful := 0
	for err := range results {
		if err == nil {
			successful++
		} else if !errors.Is(err, ErrNotFound) {
			t.Fatalf("unexpected consume error: %v", err)
		}
	}
	if successful != 1 {
		t.Fatalf("challenge consumed %d times", successful)
	}
	if err := st.SavePasskeyChallenge(t.Context(), challenge, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ConsumePasskeyChallenge(t.Context(), "challenge", "login", "binding", challenge.ExpiresAt); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expired challenge accepted: %v", err)
	}
	challenge.ID = "new-challenge"
	if err := st.SavePasskeyChallenge(t.Context(), challenge, challenge.ExpiresAt); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := st.db.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM passkey_challenge WHERE id='challenge'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("old challenge retained: %d, %v", count, err)
	}
}
