package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
)

func newEventAPITest(t *testing.T) (*Server, *http.Cookie, string) {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BootstrapAdmin(t.Context(), "admin", hash, now); err != nil {
		t.Fatal(err)
	}
	secret := bytes.Repeat([]byte{9}, 32)
	raw, sessionID, err := auth.NewSessionToken(secret)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(t.Context(), sessionID, 1, now, now.Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	server := NewServer(config.Config{
		PublicURL: "http://localhost:8080", SessionSecret: secret, SessionTTL: 24 * time.Hour,
		WebhookAllowPrivate: true,
	}, st, fstest.MapFS{}, BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return now })
	return server, &http.Cookie{Name: auth.CookieName(false), Value: raw}, sessionID
}

type eventFlushWriter struct {
	*httptest.ResponseRecorder
	afterFlush func()
}

func (writer *eventFlushWriter) Flush() {
	writer.ResponseRecorder.Flush()
	writer.afterFlush()
}

func eventTestHandler(server *Server) http.Handler {
	mux := http.NewServeMux()
	server.registerEventRoutes(mux)
	server.registerWebhookRoutes(mux)
	return server.withRequestID(server.withSecurityHeaders(mux))
}

func TestSSEInitialCursorReplayAndCancel(t *testing.T) {
	server, cookie, _ := newEventAPITest(t)
	for range 205 {
		if _, err := server.store.CreateTask(t.Context(), store.CreateTaskParams{Title: "task", Now: server.now()}); err != nil {
			t.Fatal(err)
		}
	}
	for _, test := range []struct {
		name    string
		query   string
		header  string
		cursor  int64
		changes int
	}{
		{name: "initial", cursor: 205},
		{name: "resume", header: "2", cursor: 2, changes: 203},
		{name: "query", query: "?cursor=204", cursor: 204, changes: 1},
		{name: "header wins", query: "?cursor=200", header: "204", cursor: 204, changes: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events"+test.query, nil)
			request.AddCookie(cookie)
			request.Header.Set("Last-Event-ID", test.header)
			recorder := &eventFlushWriter{ResponseRecorder: httptest.NewRecorder()}
			recorder.afterFlush = func() {
				if strings.Count(recorder.Body.String(), "event: change\n") == test.changes {
					cancel()
				}
			}
			eventTestHandler(server).ServeHTTP(recorder, request)
			if recorder.Code != http.StatusOK || recorder.Header().Get("Content-Type") != "text/event-stream" || !recorder.Flushed {
				t.Fatalf("stream not flushed: status=%d headers=%v body=%s", recorder.Code, recorder.Header(), recorder.Body.String())
			}
			body := recorder.Body.String()
			if !strings.Contains(body, `data: {"cursor":`+strconv.FormatInt(test.cursor, 10)+"}\n") || strings.Count(body, "event: change\n") != test.changes {
				t.Fatalf("unexpected replay: %s", body)
			}
			previous := test.cursor
			for _, frame := range strings.Split(body, "\n\n") {
				if !strings.Contains(frame, "event: change\n") {
					continue
				}
				_, data, ok := strings.Cut(frame, "data: ")
				var event store.ChangeEvent
				if !ok || json.Unmarshal([]byte(data), &event) != nil || event.ID != previous+1 || event.Resource != "task" || event.Kind != "created" {
					t.Fatalf("invalid replay frame: %s", frame)
				}
				previous = event.ID
			}
			if err := server.store.Ping(t.Context()); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestSSERejectsUnauthorizedOriginAndCursorBeforeStreaming(t *testing.T) {
	server, cookie, sessionID := newEventAPITest(t)
	raw, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.store.CreateAPIToken(t.Context(), "token", prefix, hash, []string{auth.ScopeRead}, server.now()); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		path    string
		cookie  bool
		headers map[string]string
		status  int
	}{
		{path: "/api/v1/events", status: 401},
		{path: "/api/v1/events?token=" + raw, status: 401},
		{path: "/api/v1/events", headers: map[string]string{"Authorization": "Bearer " + raw}, status: 403},
		{path: "/api/v1/events", cookie: true, headers: map[string]string{"Origin": "https://evil.example"}, status: 403},
		{path: "/api/v1/events", cookie: true, headers: map[string]string{"Sec-Fetch-Site": "same-site"}, status: 403},
		{path: "/api/v1/events?cursor=-1", cookie: true, status: 400},
		{path: "/api/v1/events?cursor=1", cookie: true, status: 400},
		{path: "/api/v1/events?cursor=bad", cookie: true, status: 400},
		{path: "/api/v1/events?cursor=", cookie: true, status: 400},
		{path: "/api/v1/events", cookie: true, headers: map[string]string{"Last-Event-ID": "9223372036854775808"}, status: 400},
	} {
		request := httptest.NewRequest(http.MethodGet, test.path, nil)
		if test.cookie {
			request.AddCookie(cookie)
		}
		for name, value := range test.headers {
			request.Header.Set(name, value)
		}
		recorder := httptest.NewRecorder()
		eventTestHandler(server).ServeHTTP(recorder, request)
		if recorder.Code != test.status || !strings.HasPrefix(recorder.Header().Get("Content-Type"), "application/json") || recorder.Flushed {
			t.Fatalf("%s headers=%v: status=%d body=%s", test.path, test.headers, recorder.Code, recorder.Body.String())
		}
	}
	if err := server.store.DeleteSession(t.Context(), sessionID); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/events", nil)
	request.AddCookie(cookie)
	recorder := httptest.NewRecorder()
	eventTestHandler(server).ServeHTTP(recorder, request)
	if recorder.Code != 401 {
		t.Fatalf("revoked session reconnect: %d", recorder.Code)
	}
}

func TestSSEHeartbeatRechecksSession(t *testing.T) {
	server, cookie, sessionID := newEventAPITest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	heartbeat := make(chan time.Time, 2)
	poll := make(chan time.Time)
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events", nil)
	request.AddCookie(cookie)
	recorder := &eventFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	flushes := 0
	recorder.afterFlush = func() {
		flushes++
		if flushes == 2 {
			if err := server.store.DeleteSession(t.Context(), sessionID); err != nil {
				t.Fatal(err)
			}
		}
		heartbeat <- server.now()
	}
	handler := server.requireAuth(server.requireWeb(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		server.streamEvents(writer, request, poll, heartbeat)
	})))
	server.withRequestID(handler).ServeHTTP(recorder, request)
	if ctx.Err() != nil || strings.Count(recorder.Body.String(), ": heartbeat\n\n") != 1 {
		t.Fatalf("revoked session kept streaming: ctx=%v body=%s", ctx.Err(), recorder.Body.String())
	}
}

func TestSSELivePollingReleasesSQLiteAndStopsAfterRevocation(t *testing.T) {
	server, cookie, sessionID := newEventAPITest(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	poll := make(chan time.Time, 2)
	heartbeat := make(chan time.Time)
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events", nil)
	request.AddCookie(cookie)
	recorder := &eventFlushWriter{ResponseRecorder: httptest.NewRecorder()}
	flushes := 0
	recorder.afterFlush = func() {
		flushes++
		if flushes == 1 {
			if _, err := server.store.CreateTask(ctx, store.CreateTaskParams{Title: "live", Now: server.now()}); err != nil {
				t.Fatal(err)
			}
		} else {
			if err := server.store.DeleteSession(ctx, sessionID); err != nil {
				t.Fatal(err)
			}
			if _, err := server.store.CreateTask(ctx, store.CreateTaskParams{Title: "after revoke", Now: server.now()}); err != nil {
				t.Fatal(err)
			}
		}
		poll <- server.now()
	}
	handler := server.requireAuth(server.requireWeb(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		server.streamEvents(writer, request, poll, heartbeat)
	})))
	server.withRequestID(handler).ServeHTTP(recorder, request)
	if ctx.Err() != nil || strings.Count(recorder.Body.String(), "event: change\n") != 1 {
		t.Fatalf("poll leaked events or held DB: ctx=%v body=%s", ctx.Err(), recorder.Body.String())
	}
}

type blockedEventWriter struct {
	header     http.Header
	mutex      sync.Mutex
	deadline   time.Time
	started    chan struct{}
	changed    chan struct{}
	blockFlush bool
}

func (writer *blockedEventWriter) Header() http.Header { return writer.header }
func (writer *blockedEventWriter) WriteHeader(int)     {}
func (writer *blockedEventWriter) SetWriteDeadline(deadline time.Time) error {
	writer.mutex.Lock()
	writer.deadline = deadline
	writer.mutex.Unlock()
	select {
	case writer.changed <- struct{}{}:
	default:
	}
	return nil
}
func (writer *blockedEventWriter) Write(body []byte) (int, error) {
	if writer.blockFlush {
		return len(body), nil
	}
	return 0, writer.block()
}
func (writer *blockedEventWriter) FlushError() error { return writer.block() }
func (writer *blockedEventWriter) block() error {
	close(writer.started)
	for {
		writer.mutex.Lock()
		deadline := writer.deadline
		writer.mutex.Unlock()
		if deadline.IsZero() {
			return fmt.Errorf("write has no deadline")
		}
		if time.Until(deadline) <= 0 {
			return context.DeadlineExceeded
		}
		<-writer.changed
	}
}

func TestSSECancellationInterruptsBlockedWriteAndFlush(t *testing.T) {
	server, cookie, _ := newEventAPITest(t)
	for _, blockFlush := range []bool{false, true} {
		t.Run(strconv.FormatBool(blockFlush), func(t *testing.T) {
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v1/events", nil)
			request.AddCookie(cookie)
			writer := &blockedEventWriter{header: make(http.Header), started: make(chan struct{}), changed: make(chan struct{}, 1), blockFlush: blockFlush}
			done := make(chan struct{})
			go func() { defer close(done); eventTestHandler(server).ServeHTTP(writer, request) }()
			select {
			case <-writer.started:
			case <-time.After(3 * time.Second):
				t.Fatal("stream did not start")
			}
			writer.mutex.Lock()
			deadline := writer.deadline
			writer.mutex.Unlock()
			if remaining := time.Until(deadline); remaining <= 0 || remaining > 10*time.Second {
				t.Fatalf("unsafe write deadline: %v", remaining)
			}
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Fatal("cancel did not interrupt blocked response")
			}
		})
	}
}
