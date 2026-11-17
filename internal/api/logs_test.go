package api

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/diagnostics"
)

func TestDiagnosticLogsRequireSessionAndCorrelateSafeErrors(t *testing.T) {
	fixture := newTaskAFixture(t)
	logs, err := diagnostics.Open(t.TempDir(), io.Discard, diagnostics.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = logs.Close() })
	fixture.server.SetDiagnostics(logs)
	fixture.server.logger = slog.New(logs.Handler())
	fixture.mux.Handle("/", fixture.server.Handler())
	response := fixture.request(t, http.MethodPost, "/api/v1/tokens", `{"name":"private task title","scopes":["invalid"],"password":"never-record-this"}`, "", nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("test error = %d", response.Code)
	}
	requestID := response.Header().Get("X-Request-Id")
	response = fixture.request(t, http.MethodGet, "/api/v1/logs?level=WARN&request_id="+requestID, "", "", nil)
	if response.Code != http.StatusOK || response.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("logs = %d, %v", response.Code, response.Header())
	}
	if !strings.Contains(response.Body.String(), `"error_code":"invalid_scopes"`) || !strings.Contains(response.Body.String(), requestID) || !strings.Contains(response.Body.String(), "POST /api/v1/tokens") {
		t.Fatalf("missing correlated error: %s", response.Body.String())
	}
	if strings.Contains(response.Body.String(), "never-record") || strings.Contains(response.Body.String(), "private task title") {
		t.Fatal("logs leaked request content")
	}
	token := fixture.token(t, auth.ScopeWrite)
	response = fixture.request(t, http.MethodGet, "/api/v1/logs", "", token, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("token can read logs: %d", response.Code)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/logs", nil)
	response = httptest.NewRecorder()
	fixture.server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous can read logs: %d", response.Code)
	}
	for _, query := range []string{"level=INVALID", "limit=0", "limit=501", "request_id=arbitrary"} {
		if response := fixture.request(t, http.MethodGet, "/api/v1/logs?"+query, "", "", nil); response.Code != http.StatusBadRequest {
			t.Fatalf("invalid filter %s = %d", query, response.Code)
		}
	}
}
