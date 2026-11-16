package api

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
)

type taskAFixture struct {
	server    *Server
	store     *store.Store
	dataDir   string
	mux       *http.ServeMux
	now       time.Time
	cookie    *http.Cookie
	sessionID string
}

func newTaskAFixture(t *testing.T) *taskAFixture {
	t.Helper()
	fixture := &taskAFixture{now: time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC), dataDir: t.TempDir()}
	var err error
	fixture.store, err = store.Open(t.Context(), fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = fixture.store.Close() })
	if err := fixture.store.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.BootstrapAdmin(t.Context(), "admin", "unused", fixture.now); err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{3}, 32)
	cookieValue, sessionID, err := auth.NewSessionToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	fixture.sessionID = sessionID
	if err := fixture.store.CreateSession(t.Context(), sessionID, 1, fixture.now, fixture.now.Add(60*24*time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	fixture.cookie = &http.Cookie{Name: auth.CookieName(false), Value: cookieValue}
	fixture.server = NewServer(config.Config{
		PublicURL: "http://localhost:8080", SessionSecret: secret, SessionTTL: 60 * 24 * time.Hour,
	}, fixture.store, fstest.MapFS{}, BuildInfo{}, slog.New(slog.NewTextHandler(&bytes.Buffer{}, nil)), func() time.Time { return fixture.now })
	fixture.mux = http.NewServeMux()
	fixture.mux.Handle("PATCH /api/v1/projects/{id}", fixture.server.requireAuth(fixture.server.requireScope(auth.ScopeWrite, http.HandlerFunc(fixture.server.handlePatchProject))))
	return fixture
}

func (fixture *taskAFixture) request(t *testing.T, method, path, body, token string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if token == "" {
		request.AddCookie(fixture.cookie)
		request.Header.Set("X-Tack-Client", "web")
	} else {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response := httptest.NewRecorder()
	fixture.mux.ServeHTTP(response, request)
	return response
}

func (fixture *taskAFixture) token(t *testing.T, scopes ...string) string {
	t.Helper()
	plain, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateAPIToken(t.Context(), "worker", prefix, hash, scopes, fixture.now); err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestTaskAProjectArchiveRequiresSudo(t *testing.T) {
	for _, archived := range []bool{true, false} {
		for _, useToken := range []bool{true, false} {
			t.Run(strings.Join([]string{map[bool]string{true: "archive", false: "restore"}[archived], map[bool]string{true: "token", false: "session"}[useToken]}, "/"), func(t *testing.T) {
				fixture := newTaskAFixture(t)
				project, err := fixture.store.CreateProject(t.Context(), "project", "sage", fixture.now)
				if err != nil {
					t.Fatal(err)
				}
				token := ""
				if useToken {
					token = fixture.token(t, auth.ScopeWrite)
				}
				body, err := json.Marshal(map[string]bool{"archived": archived})
				if err != nil {
					t.Fatal(err)
				}
				response := fixture.request(t, http.MethodPatch, "/api/v1/projects/"+itoa(project.ID), string(body), token, nil)
				if response.Code != http.StatusForbidden {
					t.Fatalf("archive status = %d, want 403; body=%s", response.Code, response.Body.String())
				}
			})
		}
	}
}

func TestTaskAProjectArchiveWithSudo(t *testing.T) {
	fixture := newTaskAFixture(t)
	project, err := fixture.store.CreateProject(t.Context(), "project", "sage", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.store.SetSessionSudo(t.Context(), fixture.sessionID, fixture.now.Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"archived":true}`, `{"archived":false}`} {
		response := fixture.request(t, http.MethodPatch, "/api/v1/projects/"+itoa(project.ID), body, "", nil)
		if response.Code != http.StatusOK {
			t.Fatalf("sudo archive = %d: %s", response.Code, response.Body.String())
		}
	}
	project, err = fixture.store.ProjectByID(t.Context(), project.ID)
	if err != nil || project.ArchivedAt != nil {
		t.Fatalf("project restore = %+v, %v", project, err)
	}
}

func TestTaskATagPatchAPI(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.mux.Handle("PATCH /api/v1/tags/{id}", fixture.server.requireAuth(fixture.server.requireScope(auth.ScopeWrite, fixture.server.withIdempotency(http.HandlerFunc(fixture.server.handlePatchTag)))))
	tag, err := fixture.store.CreateTag(t.Context(), "original", "sage", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateTag(t.Context(), "taken", "sky", fixture.now); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tags/" + itoa(tag.ID)
	for _, test := range []struct {
		body string
		want int
	}{
		{`{"name":"renamed","color":"plum"}`, http.StatusOK},
		{`{"name":"taken"}`, http.StatusConflict},
		{`{"name":" "}`, http.StatusUnprocessableEntity},
		{`{"color":"#ff0000"}`, http.StatusUnprocessableEntity},
		{`{"color":"sky"}`, http.StatusOK},
		{`{`, http.StatusBadRequest},
	} {
		response := fixture.request(t, http.MethodPatch, path, test.body, fixture.token(t, auth.ScopeWrite), nil)
		if response.Code != test.want {
			t.Fatalf("patch %s = %d: %s", test.body, response.Code, response.Body.String())
		}
	}
	response := fixture.request(t, http.MethodPatch, path, `{"name":"forbidden"}`, fixture.token(t, auth.ScopeRead), nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read scope patch = %d", response.Code)
	}
	response = fixture.request(t, http.MethodPatch, "/api/v1/tags/99999", `{"name":"absent"}`, "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("missing tag patch = %d", response.Code)
	}
	updated, err := fixture.store.TagByID(t.Context(), tag.ID)
	if err != nil || updated.Name != "renamed" || updated.Color != "sky" {
		t.Fatalf("tag = %+v, %v", updated, err)
	}
}

func TestTaskATrashAPISessionCSRFAndIdempotency(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerTrashRoutes(fixture.mux)
	task := taskAAPIReady(t, fixture)
	if err := fixture.store.DeleteTask(t.Context(), task.ID, fixture.now); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/tasks/" + itoa(task.ID) + "/restore"
	token := fixture.token(t, auth.ScopeWrite)
	for _, test := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/trash"}, {http.MethodPost, path},
	} {
		response := fixture.request(t, test.method, test.path, "", token, nil)
		if response.Code != http.StatusForbidden {
			t.Fatalf("token trash = %d", response.Code)
		}
	}
	for _, headers := range []map[string]string{{"X-Tack-Client": ""}, {"Origin": "https://foreign.example"}} {
		response := fixture.request(t, http.MethodPost, path, "", "", headers)
		if response.Code != http.StatusForbidden {
			t.Fatalf("CSRF restore = %d", response.Code)
		}
	}
	response := fixture.request(t, http.MethodGet, "/api/v1/trash?limit=1", "", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"deleted_at":"2026-11-16T09:00:00Z"`) || !strings.Contains(response.Body.String(), `"status":"todo"`) {
		t.Fatalf("trash = %d: %s", response.Code, response.Body.String())
	}
	var first string
	for attempt := range 2 {
		response := fixture.request(t, http.MethodPost, path, "", "", map[string]string{"Idempotency-Key": "restore-task"})
		if response.Code != http.StatusOK {
			t.Fatalf("restore = %d: %s", response.Code, response.Body.String())
		}
		if attempt == 0 {
			first = response.Body.String()
		} else if response.Body.String() != first || response.Header().Get("X-Tack-Idempotent-Replay") != "true" {
			t.Fatal("restore replay changed response")
		}
	}
	response = fixture.request(t, http.MethodPost, path, "", "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("active restore = %d", response.Code)
	}
	for _, query := range []string{"limit=abc", "limit=0", "cursor=invalid"} {
		response := fixture.request(t, http.MethodGet, "/api/v1/trash?"+query, "", "", nil)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid pagination = %d: %s", response.Code, response.Body.String())
		}
	}
	events, err := fixture.store.TaskEvents(t.Context(), task.ID)
	if err != nil || len(events) != 3 {
		t.Fatalf("restore events = %+v, %v", events, err)
	}
}
