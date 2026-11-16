package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/store"
)

func webhookAPIRequest(server *Server, cookie *http.Cookie, method, target, body string, csrf bool) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, target, strings.NewReader(body))
	if cookie != nil {
		request.AddCookie(cookie)
	}
	if csrf {
		request.Header.Set("X-Tack-Client", "web")
	}
	request.Header.Set("Origin", "http://localhost:8080")
	recorder := httptest.NewRecorder()
	eventTestHandler(server).ServeHTTP(recorder, request)
	return recorder
}

func TestWebhookAPISecretOnceSudoCRUDAndHistory(t *testing.T) {
	server, cookie, _ := newEventAPITest(t)
	body := `{"name":"build","url":"http://127.0.0.1:4444/hook","events":["task.*"]}`
	if response := webhookAPIRequest(server, cookie, "POST", "/api/v1/webhooks", body, true); response.Code != 403 {
		t.Fatalf("create without sudo: %d %s", response.Code, response.Body.String())
	}
	body = `{"name":"build","url":"http://127.0.0.1:4444/hook","events":["task.*"],"password":"` + testPassword + `"}`
	response := webhookAPIRequest(server, cookie, "POST", "/api/v1/webhooks", body, true)
	if response.Code != 201 {
		t.Fatalf("create: %d %s", response.Code, response.Body.String())
	}
	var created struct {
		Webhook store.Webhook `json:"webhook"`
		Secret  string        `json:"secret"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &created); err != nil || len(created.Secret) < 40 || created.Webhook.ID == 0 || !created.Webhook.Enabled {
		t.Fatalf("invalid creation response: err=%v", err)
	}
	if _, err := server.store.CreateTask(t.Context(), store.CreateTaskParams{Title: "task", Now: server.now()}); err != nil {
		t.Fatal(err)
	}
	path := "/api/v1/webhooks/" + strconv.FormatInt(created.Webhook.ID, 10)
	response = webhookAPIRequest(server, cookie, "GET", "/api/v1/webhooks", "", false)
	if response.Code != 200 || strings.Contains(response.Body.String(), created.Secret) || strings.Contains(response.Body.String(), `"secret"`) || !strings.Contains(response.Body.String(), `"pending_count":1`) {
		t.Fatalf("unsafe metadata: %d %s", response.Code, response.Body.String())
	}
	response = webhookAPIRequest(server, cookie, "GET", path+"/deliveries", "", false)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"status":"pending"`) {
		t.Fatalf("history: %d %s", response.Code, response.Body.String())
	}
	response = webhookAPIRequest(server, cookie, "PATCH", path, `{"name":"renamed","enabled":false}`, true)
	if response.Code != 200 || strings.Contains(response.Body.String(), created.Secret) || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatalf("patch: %d %s", response.Code, response.Body.String())
	}
	response = webhookAPIRequest(server, cookie, "GET", path+"/deliveries", "", false)
	if !strings.Contains(response.Body.String(), `"status":"cancelled"`) {
		t.Fatalf("disable: %s", response.Body.String())
	}
	response = webhookAPIRequest(server, cookie, "DELETE", path, "", true)
	if response.Code != 204 {
		t.Fatalf("delete: %d %s", response.Code, response.Body.String())
	}
	response = webhookAPIRequest(server, cookie, "GET", path+"/deliveries", "", false)
	if response.Code != 404 {
		t.Fatalf("deleted history = %d", response.Code)
	}
}

func TestWebhookAPIAuthValidationAndNoSecretExport(t *testing.T) {
	server, cookie, sessionID := newEventAPITest(t)
	if err := server.store.SetSessionSudo(t.Context(), sessionID, server.now().Add(5*time.Minute)); err != nil {
		t.Fatal(err)
	}
	valid := `{"name":"hook","url":"http://127.0.0.1","events":["*"]}`
	if response := webhookAPIRequest(server, nil, "GET", "/api/v1/webhooks", "", false); response.Code != 401 {
		t.Fatal("anonymous metadata accepted")
	}
	for _, method := range []string{"POST", "PATCH", "DELETE"} {
		path := "/api/v1/webhooks"
		if method != "POST" {
			path += "/1"
		}
		if response := webhookAPIRequest(server, cookie, method, path, valid, false); response.Code != 403 {
			t.Fatalf("%s without CSRF: %d", method, response.Code)
		}
	}
	for _, invalid := range []string{
		`{"name":"","url":"http://127.0.0.1","events":["*"]}`,
		`{"name":"hook","url":"http://127.0.0.1","events":[]}`,
		`{"name":"hook","url":"http://127.0.0.1","events":["task.created"]}`,
		`{"name":"hook","url":"http://127.0.0.1/#fragment","events":["*"]}`,
	} {
		response := webhookAPIRequest(server, cookie, "POST", "/api/v1/webhooks", invalid, true)
		if response.Code != 422 {
			t.Fatalf("invalid create = %d %s", response.Code, response.Body.String())
		}
	}
	server.cfg.WebhookAllowPrivate = false
	if response := webhookAPIRequest(server, cookie, "POST", "/api/v1/webhooks", valid, true); response.Code != 422 {
		t.Fatalf("private default = %d", response.Code)
	}
	secret := "never-export-this-secret"
	if _, err := server.store.CreateWebhook(t.Context(), "hook", "https://example.com", []string{"*"}, secret, server.now()); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/export", nil)
	request.AddCookie(cookie)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != 200 || strings.Contains(response.Body.String(), secret) {
		t.Fatalf("export exposed secret or failed: %d", response.Code)
	}
	raw, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.CreateAPIToken(t.Context(), "token", prefix, hash, []string{auth.ScopeRead, auth.ScopeWrite}, server.now()); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/webhooks", "/api/v1/webhooks/1/deliveries"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+raw)
		response := httptest.NewRecorder()
		eventTestHandler(server).ServeHTTP(response, request)
		if response.Code != 403 {
			t.Fatalf("token metadata = %d", response.Code)
		}
	}
}
