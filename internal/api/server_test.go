package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
)

const testPassword = "correct horse battery staple"

func TestHealthAndVersionEndpoints(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("healthz status = %d, want 200", resp.StatusCode)
	}
	if resp.Header.Get("X-Request-Id") == "" {
		t.Error("healthz missing X-Request-Id")
	}

	resp, err = http.Get(ts.URL + "/readyz")
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/v1/version")
	if err != nil {
		t.Fatalf("GET /api/v1/version: %v", err)
	}
	defer resp.Body.Close()
	var version struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&version); err != nil {
		t.Fatalf("decode version: %v", err)
	}
	if version.Version != "0.1.0-test" {
		t.Errorf("version = %q", version.Version)
	}
}

func TestLoginMeAndLogout(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)

	resp := login(t, client, ts.URL, testPassword, "")
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("me status = %d, want 200", resp.StatusCode)
	}
	var me struct {
		User struct {
			Username string `json:"username"`
		} `json:"user"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&me); err != nil {
		t.Fatalf("decode me: %v", err)
	}
	if me.User.Username != "admin" {
		t.Errorf("username = %q, want admin", me.User.Username)
	}

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("logout without client header status = %d, want 403", resp.StatusCode)
	}
	resp.Body.Close()

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", resp.StatusCode)
	}

	resp = request(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", "", nil)
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("me after logout status = %d, want 401", resp.StatusCode)
	}
}

func TestLoginRejectsWrongPasswordAndRateLimits(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)

	for attempt := 1; attempt <= 5; attempt++ {
		resp := login(t, client, ts.URL, "wrong-password", "")
		if resp.StatusCode != http.StatusUnauthorized {
			t.Fatalf("attempt %d status = %d, want 401", attempt, resp.StatusCode)
		}
		resp.Body.Close()
	}

	resp := login(t, client, ts.URL, testPassword, "")
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("sixth attempt status = %d, want 429", resp.StatusCode)
	}
}

func TestCookieMutationRejectsForeignOrigin(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	resp := login(t, client, ts.URL, testPassword, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
		"Origin":        "https://evil.example",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("foreign origin status = %d, want 403", resp.StatusCode)
	}
}

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if _, err := st.BootstrapAdmin(t.Context(), "admin", hash, time.Now().UTC()); err != nil {
		t.Fatalf("BootstrapAdmin: %v", err)
	}

	cfg := config.Config{
		Addr:          ":0",
		PublicURL:     "http://localhost:8080",
		DataDir:       t.TempDir(),
		AdminUsername: "admin",
		AdminPassword: testPassword,
		SessionSecret: bytes.Repeat([]byte{3}, 32),
		CookieSecure:  false,
		SessionTTL:    30 * 24 * time.Hour,
	}
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte("<!doctype html><title>Tack</title>")},
	}
	server := NewServer(
		cfg,
		st,
		assets,
		BuildInfo{Version: "0.1.0-test", Commit: "test", BuildTime: "2026-11-16T00:00:00Z"},
		slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)),
		func() time.Time { return time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC) },
	)
	return httptest.NewServer(server.Handler())
}

func newClient(t *testing.T) *http.Client {
	t.Helper()
	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookiejar.New: %v", err)
	}
	return &http.Client{Jar: jar}
}

func login(t *testing.T, client *http.Client, baseURL, password, origin string) *http.Response {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if origin != "" {
		headers["Origin"] = origin
	}
	body, err := json.Marshal(map[string]string{
		"username": "admin",
		"password": password,
	})
	if err != nil {
		t.Fatalf("marshal login: %v", err)
	}
	resp, err := requestWithBody(t, client, http.MethodPost, baseURL+"/api/v1/auth/login", bytes.NewReader(body), headers)
	if err != nil {
		t.Fatalf("login request: %v", err)
	}
	return resp
}

func request(t *testing.T, client *http.Client, method, rawURL, body string, headers map[string]string) *http.Response {
	t.Helper()
	var reader *strings.Reader
	if body == "" {
		reader = strings.NewReader("")
	} else {
		reader = strings.NewReader(body)
	}
	resp, err := requestWithBody(t, client, method, rawURL, reader, headers)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	return resp
}

func requestWithBody(t *testing.T, client *http.Client, method, rawURL string, body io.Reader, headers map[string]string) (*http.Response, error) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), method, rawURL, body)
	if err != nil {
		return nil, err
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	return client.Do(req)
}

func TestSessionCookieUsesLocalName(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	resp := login(t, client, ts.URL, testPassword, "")
	defer resp.Body.Close()

	cookies := resp.Cookies()
	if len(cookies) == 0 {
		t.Fatal("login did not set a cookie")
	}
	if cookies[0].Name != "tack_session" {
		t.Errorf("cookie name = %q, want tack_session", cookies[0].Name)
	}
	if !cookies[0].HttpOnly {
		t.Error("cookie is not HttpOnly")
	}
	if cookies[0].Secure {
		t.Error("cookie is Secure over local HTTP")
	}
}

func TestPublicURLOriginIsAccepted(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	resp := login(t, client, ts.URL, testPassword, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
		"Origin":        "http://localhost:8080",
	})
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("same origin status = %d, want 204", resp.StatusCode)
	}
}

func TestSessionSecretIsDecodedByConfig(t *testing.T) {
	t.Parallel()

	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{9}, 32))
	cfg, err := config.Load(func(key string) string {
		return map[string]string{
			"TACK_PUBLIC_URL":     "https://tack.example.com",
			"TACK_SESSION_SECRET": secret,
		}[key]
	})
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}
	if len(cfg.SessionSecret) != 32 {
		t.Fatalf("secret length = %d", len(cfg.SessionSecret))
	}
}
