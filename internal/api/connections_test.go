package api

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/today/tack/internal/auth"
)

func connectionFixture(t *testing.T) *taskAFixture {
	t.Helper()
	fixture := newTaskAFixture(t)
	fixture.mux.Handle("/", fixture.server.Handler())
	if err := fixture.store.SetSessionSudo(t.Context(), fixture.sessionID, fixture.now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	return fixture
}

func connectionToken(t *testing.T, fixture *taskAFixture, expiry string) (string, int64) {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/api/v1/tokens", `{"name":"test agent","scopes":["write"],"expires_at":`+expiry+`}`, "", nil)
	if response.Code != http.StatusCreated {
		t.Fatalf("create token status = %d", response.Code)
	}
	var result struct {
		Token string `json:"token"`
		Meta  struct {
			ID int64 `json:"id"`
		} `json:"api_token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result.Token, result.Meta.ID
}

func TestTokenExpiryValidation(t *testing.T) {
	fixture := connectionFixture(t)
	for _, expiry := range []string{`""`, `"tomorrow"`, `"2026-11-16"`, `"2026-11-16T09:00:00Z"`, `"2026-11-15T09:00:00Z"`, `"2027-11-17T09:00:00Z"`} {
		response := fixture.request(t, http.MethodPost, "/api/v1/tokens", `{"name":"invalid expiry","scopes":["read"],"expires_at":`+expiry+`}`, "", nil)
		if response.Code != http.StatusUnprocessableEntity || !strings.Contains(response.Body.String(), `"invalid_expiry"`) {
			t.Fatalf("expiry %s status = %d, want 422 invalid_expiry", expiry, response.Code)
		}
	}
	response := fixture.request(t, http.MethodGet, "/api/v1/tokens", "", "", nil)
	if response.Body.String() != "{\"tokens\":[]}\n" {
		t.Fatal("invalid creation persisted tokens")
	}
}

func TestTokenExpiryEnforcedAndLegacyRetained(t *testing.T) {
	fixture := connectionFixture(t)
	plain, tokenID := connectionToken(t, fixture, `"2026-11-16T17:01:00+08:00"`)
	legacy := fixture.token(t, auth.ScopeRead)
	permanent, _ := connectionToken(t, fixture, "null")
	response := fixture.request(t, http.MethodGet, "/api/v1/tokens", "", "", nil)
	if !strings.Contains(response.Body.String(), `"expires_at":"2026-11-16T09:01:00Z"`) || !strings.Contains(response.Body.String(), `"expires_at":null`) {
		t.Fatal("token list must report normalized expiry and legacy null")
	}
	if strings.Contains(response.Body.String(), plain) || strings.Contains(response.Body.String(), auth.HashAPIToken(plain)) {
		t.Fatal("token list leaked credentials")
	}
	if response = fixture.request(t, http.MethodGet, "/api/v1/tasks", "", plain, nil); response.Code != http.StatusOK {
		t.Fatalf("active token status = %d", response.Code)
	}
	fixture.now = fixture.now.Add(time.Minute)
	for _, request := range []struct{ method, path, body string }{
		{http.MethodGet, "/api/v1/tasks", ""},
		{http.MethodPost, "/api/v1/capture", `{"text":"expired mutation"}`},
		{http.MethodGet, "/api/v1/agent/connection", ""},
	} {
		response = fixture.request(t, request.method, request.path, request.body, plain, nil)
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("expired %s status = %d", request.path, response.Code)
		}
	}
	for _, token := range []string{legacy, permanent} {
		if response = fixture.request(t, http.MethodGet, "/api/v1/tasks", "", token, nil); response.Code != http.StatusOK {
			t.Fatalf("non-expiring token status = %d", response.Code)
		}
	}
	response = fixture.request(t, http.MethodGet, "/api/v1/tokens", "", "", nil)
	if !strings.Contains(response.Body.String(), fmt.Sprintf(`"id":%d`, tokenID)) {
		t.Fatal("expired token disappeared from management")
	}
	response = fixture.request(t, http.MethodDelete, "/api/v1/tokens/"+itoa(tokenID), "", "", nil)
	if response.Code != http.StatusNoContent {
		t.Fatalf("revoke expired token status = %d", response.Code)
	}
}

func TestConnectionMetadataUsesOwnCredentialAndConfiguredURL(t *testing.T) {
	fixture := connectionFixture(t)
	fixture.server.cfg.PublicURL = "https://tack.example"
	for _, scope := range []string{auth.ScopeRead, auth.ScopeCapture, auth.ScopeWrite} {
		plain := fixture.token(t, scope)
		response := fixture.request(t, http.MethodGet, "/api/v1/agent/connection", "", plain, nil)
		if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("connection scope %s status = %d", scope, response.Code)
		}
		var result struct {
			Base      string         `json:"api_base_url"`
			Kind      string         `json:"auth_kind"`
			Transport string         `json:"transport"`
			MCP       bool           `json:"mcp_supported"`
			Token     *tokenResponse `json:"token"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		if result.Base != "https://tack.example/api/v1" || result.Kind != "token" || result.Transport != "rest" || result.MCP || result.Token == nil || len(result.Token.Scopes) != 1 || result.Token.Scopes[0] != scope {
			t.Fatalf("incorrect connection metadata for %s", scope)
		}
		if strings.Contains(response.Body.String(), plain) || strings.Contains(response.Body.String(), auth.HashAPIToken(plain)) {
			t.Fatal("connection metadata leaked secret")
		}
		response = fixture.request(t, http.MethodGet, "/api/v1/tokens", "", plain, nil)
		if response.Code != http.StatusForbidden {
			t.Fatal("connection key can manage credentials")
		}
	}
	response := fixture.request(t, http.MethodGet, "/api/v1/agent/connection", "", "", nil)
	if !strings.Contains(response.Body.String(), `"auth_kind":"session"`) || !strings.Contains(response.Body.String(), `"token":null`) {
		t.Fatal("session must not masquerade as a key connection")
	}
	plain, tokenID := connectionToken(t, fixture, "null")
	if err := fixture.store.RevokeAPIToken(t.Context(), tokenID, fixture.now); err != nil {
		t.Fatal(err)
	}
	for _, token := range []string{plain, "tk_invalid"} {
		response = fixture.request(t, http.MethodGet, "/api/v1/agent/connection", "", "", map[string]string{"Authorization": "Bearer " + token})
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid key fell back to session: %d", response.Code)
		}
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/agent/connection", nil)
	request.Host = "untrusted.example"
	response = httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous metadata status = %d", response.Code)
	}
}

type expiryReader struct {
	io.Reader
	onRead func()
}

func (reader expiryReader) Read(buffer []byte) (int, error) {
	reader.onRead()
	return reader.Reader.Read(buffer)
}

func TestTokenExpiryRecheckedAfterReadingMutation(t *testing.T) {
	fixture := connectionFixture(t)
	plain, _ := connectionToken(t, fixture, `"2026-11-16T09:01:00Z"`)
	request := httptest.NewRequest(http.MethodPost, "/api/v1/capture", expiryReader{
		Reader: strings.NewReader(`{"text":"must not be saved"}`),
		onRead: func() { fixture.now = time.Date(2026, 11, 16, 9, 1, 0, 0, time.UTC) },
	})
	request.Header.Set("Authorization", "Bearer "+plain)
	response := httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("expired during body read status = %d", response.Code)
	}
}
