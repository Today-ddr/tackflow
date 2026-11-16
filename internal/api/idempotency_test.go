package api

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
)

func TestIdempotencyEncryptsSecretsAndRejectsTampering(t *testing.T) {
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	server := NewServer(config.Config{SessionSecret: bytes.Repeat([]byte{9}, 32)}, st, nil, BuildInfo{}, nil, func() time.Time { return now })
	secret := []byte(`{"claim":{"id":"a-secret-that-must-not-be-stored-as-plaintext"}}`)
	calls := 0
	handler := server.withIdempotency(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = writer.Write(secret)
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/agent/claim", nil)
	request.Header.Set("Idempotency-Key", "test-key")
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authContext{Session: &store.Session{ID: "test-session"}}))
	first := httptest.NewRecorder()
	handler.ServeHTTP(first, request)
	second := httptest.NewRecorder()
	handler.ServeHTTP(second, request)
	if calls != 1 || !bytes.Equal(first.Body.Bytes(), secret) || !bytes.Equal(second.Body.Bytes(), secret) {
		t.Fatalf("replay calls=%d, first=%s second=%s", calls, first.Body.String(), second.Body.String())
	}
	digest := sha256.Sum256([]byte("session:test-session\x00POST\x00/api/v1/agent/claim\x00test-key"))
	key := "v2:" + hex.EncodeToString(digest[:])
	cached, found, err := st.GetIdempotency(t.Context(), key, now)
	if err != nil || !found {
		t.Fatalf("cache = %v %v", found, err)
	}
	if bytes.Contains(cached.Body, []byte("a-secret-that-must-not-be-stored-as-plaintext")) {
		t.Fatal("cached response stores a secret in plaintext")
	}
	if err := st.PutIdempotency(t.Context(), key, http.StatusOK, []byte("corrupted"), now); err != nil {
		t.Fatal(err)
	}
	corrupt := httptest.NewRecorder()
	handler.ServeHTTP(corrupt, request)
	if corrupt.Code != http.StatusInternalServerError || calls != 1 {
		t.Fatalf("tampered cache replayed or repeated mutation: status=%d calls=%d", corrupt.Code, calls)
	}
}

func TestIdempotencyStorageFailureCannotRepeatMutation(t *testing.T) {
	dataDir := t.TempDir()
	st, err := store.Open(t.Context(), dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	database, err := sql.Open("sqlite", filepath.Join(dataDir, "tack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	for _, operation := range []string{"INSERT", "UPDATE"} {
		if _, err := database.ExecContext(t.Context(), "CREATE TRIGGER fail_cache_"+operation+" BEFORE "+operation+" ON idempotency WHEN NEW.status >= 200 BEGIN SELECT RAISE(FAIL,'injected cache write failure'); END"); err != nil {
			t.Fatal(err)
		}
	}
	server := NewServer(config.Config{SessionSecret: bytes.Repeat([]byte{9}, 32)}, st, nil, BuildInfo{}, nil, time.Now)
	calls := 0
	handler := server.withIdempotency(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		calls++
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"id":1}`))
	}))
	request := httptest.NewRequest(http.MethodPost, "/api/v1/tasks", nil)
	request.Header.Set("Idempotency-Key", "fault-key")
	request = request.WithContext(context.WithValue(request.Context(), authContextKey{}, authContext{Session: &store.Session{ID: "test-session"}}))
	for attempt := 0; attempt < 2; attempt++ {
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if response.Code < 400 {
			t.Errorf("storage failure reported success: %d", response.Code)
		}
	}
	if calls != 1 {
		t.Fatalf("storage failure repeated mutation %d times", calls)
	}
}
