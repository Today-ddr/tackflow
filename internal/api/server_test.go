package api

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	defer func() { _ = resp.Body.Close() }()
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
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("readyz status = %d, want 200", resp.StatusCode)
	}

	resp, err = http.Get(ts.URL + "/api/v1/version")
	if err != nil {
		t.Fatalf("GET /api/v1/version: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
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

func TestManifestContentType(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/manifest.webmanifest")
	if err != nil {
		t.Fatalf("GET /manifest.webmanifest: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("manifest status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "application/manifest+json" {
		t.Fatalf("manifest content type = %q", got)
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
	_ = resp.Body.Close()

	resp = request(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", "", nil)
	defer func() { _ = resp.Body.Close() }()
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
	_ = resp.Body.Close()

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("logout status = %d, want 204", resp.StatusCode)
	}

	resp = request(t, client, http.MethodGet, ts.URL+"/api/v1/auth/me", "", nil)
	defer func() { _ = resp.Body.Close() }()
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
		_ = resp.Body.Close()
	}

	resp := login(t, client, ts.URL, testPassword, "")
	defer func() { _ = resp.Body.Close() }()
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
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d, want 200", resp.StatusCode)
	}

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
		"Origin":        "https://evil.example",
	})
	defer func() { _ = resp.Body.Close() }()
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
		"index.html":           &fstest.MapFile{Data: []byte("<!doctype html><title>Tack</title>")},
		"manifest.webmanifest": &fstest.MapFile{Data: []byte(`{"name":"Tack"}`)},
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
	defer func() { _ = resp.Body.Close() }()

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
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	resp = request(t, client, http.MethodPost, ts.URL+"/api/v1/auth/logout", "", map[string]string{
		"X-Tack-Client": "web",
		"Origin":        "http://localhost:8080",
	})
	defer func() { _ = resp.Body.Close() }()
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

func TestTaskLifecycleAndCapture(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	if resp := login(t, client, ts.URL, testPassword, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/projects", map[string]any{
		"name": "codeg", "color": "clay",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create project status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/capture", map[string]any{
		"text": "修登录跳转 #bug @codeg !p2 ^明天\n补充说明",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("capture status = %d", resp.StatusCode)
	}
	var captured taskDTO
	decodeResponse(t, resp, &captured)
	if captured.Status != "inbox" || captured.Priority != 2 || captured.Project == nil ||
		captured.Project.Name != "codeg" || len(captured.Tags) != 1 || captured.Tags[0].Name != "bug" {
		t.Fatalf("captured = %+v", captured)
	}
	if captured.Notes != "补充说明" || captured.DueAt == nil {
		t.Fatalf("captured notes/due = %+v / %+v", captured.Notes, captured.DueAt)
	}

	resp = webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks?status=inbox&limit=20", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("list tasks status = %d", resp.StatusCode)
	}
	var list struct {
		Tasks []taskDTO `json:"tasks"`
	}
	decodeResponse(t, resp, &list)
	if len(list.Tasks) != 1 || list.Tasks[0].ID != captured.ID {
		t.Fatalf("list = %+v", list)
	}

	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks/"+itoa(captured.ID)+"/move", map[string]any{
		"status": "doing",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move status = %d", resp.StatusCode)
	}
	var moved taskDTO
	decodeResponse(t, resp, &moved)
	if moved.Status != "doing" {
		t.Fatalf("moved status = %q", moved.Status)
	}

	resp = webJSON(t, client, http.MethodPatch, ts.URL+"/api/v1/tasks/"+itoa(captured.ID), map[string]any{
		"title":       "新标题",
		"agent_ready": true,
		"tags":        []string{"bug", "后端"},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("patch status = %d", resp.StatusCode)
	}
	var patched taskDTO
	decodeResponse(t, resp, &patched)
	if patched.Title != "新标题" || !patched.AgentReady || len(patched.Tags) != 2 {
		t.Fatalf("patched = %+v", patched)
	}

	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks/"+itoa(captured.ID)+"/comments", map[string]any{
		"text": "等验收",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("comment status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks/"+itoa(captured.ID), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("task detail status = %d", resp.StatusCode)
	}
	var detail struct {
		Task   taskDTO    `json:"task"`
		Events []eventDTO `json:"events"`
	}
	decodeResponse(t, resp, &detail)
	if len(detail.Events) < 4 {
		t.Fatalf("events = %+v", detail.Events)
	}

	resp = webJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/tasks/"+itoa(captured.ID), nil)
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("delete status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks/"+itoa(captured.ID), nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("deleted detail status = %d", resp.StatusCode)
	}
	closeBody(t, resp)
}

func TestAPITokenScopesAndIdempotency(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	if resp := login(t, client, ts.URL, testPassword, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/auth/sudo", map[string]string{
		"password": testPassword,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sudo status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "shortcut", "scopes": []string{"capture"},
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create token status = %d", resp.StatusCode)
	}
	var tokenResponse struct {
		Token string `json:"token"`
	}
	decodeResponse(t, resp, &tokenResponse)
	if tokenResponse.Token == "" {
		t.Fatal("empty token")
	}

	resp = bearerJSON(t, ts, tokenResponse.Token, http.MethodPost, "/api/v1/capture", map[string]string{
		"text": "用快捷指令记一笔",
	})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("token capture status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = bearerJSON(t, ts, tokenResponse.Token, http.MethodGet, "/api/v1/tasks", nil)
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token read status = %d, want 403", resp.StatusCode)
	}
	closeBody(t, resp)

	headers := map[string]string{"Idempotency-Key": "idem-create-1"}
	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks", map[string]string{
		"title": "只创建一次",
	}, headers)
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("first idempotent create status = %d", resp.StatusCode)
	}
	var first taskDTO
	decodeResponse(t, resp, &first)
	resp = webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks", map[string]string{
		"title": "不应覆盖",
	}, headers)
	if resp.StatusCode != http.StatusCreated || resp.Header.Get("X-Tack-Idempotent-Replay") != "true" {
		t.Fatalf("replay status = %d, replay header = %q", resp.StatusCode, resp.Header.Get("X-Tack-Idempotent-Replay"))
	}
	var second taskDTO
	decodeResponse(t, resp, &second)
	if second.ID != first.ID || second.Title != "只创建一次" {
		t.Fatalf("replay task = %+v, first = %+v", second, first)
	}
}

func TestSudoAndSettings(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	if resp := login(t, client, ts.URL, testPassword, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}

	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "blocked", "scopes": []string{"read"},
	})
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("token without sudo status = %d", resp.StatusCode)
	}
	closeBody(t, resp)

	resp = webJSON(t, client, http.MethodPatch, ts.URL+"/api/v1/settings", map[string]any{
		"update_interval_hours": 6,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("settings patch status = %d", resp.StatusCode)
	}
	var settings settingsResponse
	decodeResponse(t, resp, &settings)
	if settings.UpdateIntervalHours != 6 {
		t.Fatalf("interval = %d", settings.UpdateIntervalHours)
	}

	resp = webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/update/status", nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("update status = %d", resp.StatusCode)
	}
	var updateStatus updateStatusResponse
	decodeResponse(t, resp, &updateStatus)
	if updateStatus.CurrentVersion != "0.1.0-test" {
		t.Fatalf("update current = %q", updateStatus.CurrentVersion)
	}
}

func TestCreateTokenAcceptsPasswordWithoutPriorSudo(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	if resp := login(t, client, ts.URL, testPassword, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "shortcut", "scopes": []string{"capture"}, "password": testPassword,
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token status = %d, body = %s", resp.StatusCode, body)
	}
	closeBody(t, resp)
}

func TestRevokeTokenAcceptsEmptyChunkedBody(t *testing.T) {
	t.Parallel()

	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	if resp := login(t, client, ts.URL, testPassword, ""); resp.StatusCode != http.StatusOK {
		t.Fatalf("login status = %d", resp.StatusCode)
	}
	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "chunked-revoke", "scopes": []string{"read"}, "password": testPassword,
	})
	if resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("create token status = %d, body = %s", resp.StatusCode, body)
	}
	var created struct {
		APIToken struct {
			ID int64 `json:"id"`
		} `json:"api_token"`
	}
	decodeResponse(t, resp, &created)

	req, err := http.NewRequestWithContext(
		t.Context(),
		http.MethodDelete,
		ts.URL+"/api/v1/tokens/"+itoa(created.APIToken.ID),
		io.NopCloser(strings.NewReader("")),
	)
	if err != nil {
		t.Fatalf("new revoke request: %v", err)
	}
	req.ContentLength = -1
	req.TransferEncoding = []string{"chunked"}
	req.Header.Set("X-Tack-Client", "web")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatalf("revoke token: %v", err)
	}
	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("revoke token status = %d, body = %s", resp.StatusCode, body)
	}
	closeBody(t, resp)
}

func TestParseDueValueUsesConfiguredTimezone(t *testing.T) {
	t.Parallel()

	location := time.FixedZone("Asia/Shanghai", 8*60*60)
	value := "2026-11-20"
	due, err := parseDueValue(&value, location)
	if err != nil {
		t.Fatalf("parseDueValue: %v", err)
	}
	if due == nil || due.Format(time.RFC3339) != "2026-11-20T23:59:59+08:00" {
		t.Fatalf("due = %v", due)
	}
}

func webJSON(t *testing.T, client *http.Client, method, rawURL string, body any, extra ...map[string]string) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, rawURL, reader)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if method != http.MethodGet && method != http.MethodHead {
		req.Header.Set("X-Tack-Client", "web")
	}
	for _, headers := range extra {
		for key, value := range headers {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, rawURL, err)
	}
	return resp
}

func bearerJSON(t *testing.T, ts *httptest.Server, token, method, path string, body any) *http.Response {
	t.Helper()
	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal bearer request: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}
	req, err := http.NewRequestWithContext(t.Context(), method, ts.URL+path, reader)
	if err != nil {
		t.Fatalf("new bearer request: %v", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := ts.Client().Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	return resp
}

func decodeResponse(t *testing.T, resp *http.Response, value any) {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	if err := json.NewDecoder(resp.Body).Decode(value); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}

func closeBody(t *testing.T, resp *http.Response) {
	t.Helper()
	_ = resp.Body.Close()
}

func itoa(value int64) string {
	return fmt.Sprintf("%d", value)
}
