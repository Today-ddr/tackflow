package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
)

type sudoReplayFixture struct {
	server    *Server
	http      *httptest.Server
	client    *http.Client
	sessionID string
	database  *sql.DB
}

func newSudoReplayFixture(t *testing.T) sudoReplayFixture {
	t.Helper()
	directory := t.TempDir()
	st, err := store.Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	if _, err := st.BootstrapAdmin(t.Context(), "admin", hash, now); err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{
		SessionSecret: bytes.Repeat([]byte{6}, 32), SessionTTL: 24 * time.Hour,
	}, st, nil, BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	httpServer := httptest.NewServer(server.Handler())
	t.Cleanup(httpServer.Close)
	client := newClient(t)
	response := login(t, client, httpServer.URL, testPassword, "")
	if response.StatusCode != http.StatusOK {
		t.Fatalf("login = %d", response.StatusCode)
	}
	closeBody(t, response)
	sessions, err := st.ListSessions(t.Context(), 1, now)
	if err != nil || len(sessions) != 1 {
		t.Fatalf("sessions = %+v, %v", sessions, err)
	}
	database, err := sql.Open("sqlite", filepath.Join(directory, "tack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return sudoReplayFixture{server: server, http: httpServer, client: client, sessionID: sessions[0].ID, database: database}
}

func TestTokenSecretReplayRequiresCurrentSudo(t *testing.T) {
	fixture := newSudoReplayFixture(t)
	headers := map[string]string{"Idempotency-Key": "token-secret"}
	response := webJSON(t, fixture.client, http.MethodPost, fixture.http.URL+"/api/v1/tokens", map[string]any{
		"name": "original", "scopes": []string{"read", "write"}, "password": testPassword,
	}, headers)
	if response.StatusCode != http.StatusCreated {
		t.Fatalf("create token = %d", response.StatusCode)
	}
	original, err := io.ReadAll(response.Body)
	closeBody(t, response)
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(original, &created); err != nil || created.Token == "" {
		t.Fatalf("create token response invalid: %v", err)
	}
	var originalCache string
	if err := fixture.database.QueryRowContext(t.Context(), "SELECT body FROM idempotency WHERE status = 201").Scan(&originalCache); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name       string
		sudo       string
		body       any
		wantStatus int
		wantCode   string
	}{
		{"current sudo", "current", map[string]any{}, http.StatusCreated, ""},
		{"current sudo empty body", "current", nil, http.StatusCreated, ""},
		{"expired sudo", "expired", map[string]any{}, http.StatusForbidden, "sudo_required"},
		{"missing sudo", "missing", map[string]any{}, http.StatusForbidden, "sudo_required"},
		{"exact expiry", "boundary", map[string]any{}, http.StatusForbidden, "sudo_required"},
		{"missing body", "expired", nil, http.StatusForbidden, "sudo_required"},
		{"wrong password", "expired", map[string]any{"password": "incorrect"}, http.StatusForbidden, "invalid_password"},
		{"invalid password type", "expired", map[string]any{"password": 123}, http.StatusBadRequest, "bad_request"},
		{"correct inline password", "expired", map[string]any{"password": testPassword, "name": "must not create", "scopes": []string{"capture"}}, http.StatusCreated, ""},
	} {
		t.Run(test.name, func(t *testing.T) {
			var until any
			switch test.sudo {
			case "current":
				until = fixture.server.now().Add(time.Minute).Format(time.RFC3339)
			case "expired":
				until = fixture.server.now().Add(-time.Second).Format(time.RFC3339)
			case "boundary":
				until = fixture.server.now().Format(time.RFC3339)
			}
			if _, err := fixture.database.ExecContext(t.Context(), "UPDATE session SET sudo_until = ? WHERE id = ?", until, fixture.sessionID); err != nil {
				t.Fatal(err)
			}
			response := webJSON(t, fixture.client, http.MethodPost, fixture.http.URL+"/api/v1/tokens", test.body, headers)
			body, err := io.ReadAll(response.Body)
			closeBody(t, response)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != test.wantStatus {
				t.Errorf("replay status = %d, want %d", response.StatusCode, test.wantStatus)
			}
			if test.wantStatus == http.StatusCreated {
				if !bytes.Equal(body, original) || response.Header.Get("X-Tack-Idempotent-Replay") != "true" {
					t.Error("authorized replay did not return the original token response")
				}
			} else {
				if bytes.Contains(body, []byte(created.Token)) || response.Header.Get("X-Tack-Idempotent-Replay") != "" {
					t.Error("rejected replay revealed the cached token")
				}
				var failure struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(body, &failure); err != nil || failure.Error.Code != test.wantCode {
					t.Errorf("error code = %q, want %q; decode = %v", failure.Error.Code, test.wantCode, err)
				}
			}
			tokens, err := fixture.server.store.ListAPITokens(t.Context())
			if err != nil || len(tokens) != 1 || tokens[0].Name != "original" {
				t.Errorf("replay changed token records: %+v, %v", tokens, err)
			}
			var cached string
			if err := fixture.database.QueryRowContext(t.Context(), "SELECT body FROM idempotency WHERE status = 201").Scan(&cached); err != nil || cached != originalCache {
				t.Errorf("replay cleared or replaced the cached result: %v", err)
			}
		})
	}
	session, err := fixture.server.store.SessionByID(t.Context(), fixture.sessionID, fixture.server.now())
	if err != nil || session.SudoUntil == nil || !session.SudoUntil.Equal(fixture.server.now().Add(5*time.Minute)) {
		t.Fatalf("inline password did not refresh sudo: %+v, %v", session, err)
	}
	response = webJSON(t, fixture.client, http.MethodPost, fixture.http.URL+"/api/v1/tokens", nil, headers)
	if response.StatusCode != http.StatusCreated || response.Header.Get("X-Tack-Idempotent-Replay") != "true" {
		t.Errorf("refreshed sudo cannot replay: %d", response.StatusCode)
	}
	closeBody(t, response)
}

func TestOrdinaryMetadataReplayDoesNotRequireSudo(t *testing.T) {
	fixture := newSudoReplayFixture(t)
	var project projectDTO
	decodeResponse(t, webJSON(t, fixture.client, http.MethodPost, fixture.http.URL+"/api/v1/projects", map[string]string{"name": "original"}), &project)
	token := resourceTestToken(t, fixture.http, fixture.client, nil, nil)
	if err := fixture.server.store.SetSessionSudo(t.Context(), fixture.sessionID, fixture.server.now().Add(-time.Second)); err != nil {
		t.Fatal(err)
	}
	for _, principal := range []string{"session", "token"} {
		t.Run(principal, func(t *testing.T) {
			headers := map[string]string{"Idempotency-Key": "rename-" + principal}
			if principal == "token" {
				headers["Authorization"] = "Bearer " + token
			}
			path := fixture.http.URL + "/api/v1/projects/" + itoa(project.ID)
			for attempt := range 2 {
				response := webJSON(t, fixture.client, http.MethodPatch, path, map[string]string{"name": "renamed " + principal}, headers)
				if response.StatusCode != http.StatusOK || attempt == 1 && response.Header.Get("X-Tack-Idempotent-Replay") != "true" {
					t.Errorf("ordinary rename attempt %d = %d", attempt, response.StatusCode)
				}
				closeBody(t, response)
			}
		})
	}
}
