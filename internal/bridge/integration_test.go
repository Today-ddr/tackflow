package bridge

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/today-ddr/tackflow/internal/api"
	"github.com/today-ddr/tackflow/internal/auth"
	appconfig "github.com/today-ddr/tackflow/internal/config"
	"github.com/today-ddr/tackflow/internal/store"
)

func TestBridgeWithRealTackAPIAndTemporarySQLite(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	database := newRealTackStore(t, fixture)
	task, err := database.CreateTask(ctx, store.CreateTaskParams{Title: "Bridge integration", Notes: "Temporary database only", Status: "todo", AgentReady: true, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	server := api.NewServer(appconfig.Config{PublicURL: "http://localhost", SessionSecret: bytes.Repeat([]byte{1}, 32)}, database, fstest.MapFS{"index.html": {Data: []byte("test")}}, api.BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now)
	realTack := httptest.NewServer(server.Handler())
	t.Cleanup(realTack.Close)
	fixture.config.TackURL = realTack.URL
	if err := Check(ctx, fixture.config); err != nil {
		t.Fatal(err)
	}
	worker := openFixture(t, fixture)
	step(t, worker)
	step(t, worker)
	finished, err := database.TaskByID(ctx, task.ID)
	if err != nil || finished.Status != "review" || finished.AgentReady {
		t.Fatalf("task after real API completion: %+v, %v", finished, err)
	}
	links, err := database.TaskLinks(ctx, task.ID)
	if err != nil || len(links) != 1 || links[0].Kind != "codeg_work_task" || links[0].ExternalID != "42" {
		t.Fatalf("real persisted links: %+v, %v", links, err)
	}
	events, err := database.TaskEvents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	comments := 0
	for _, event := range events {
		if event.Kind == "commented" {
			comments++
		}
	}
	if comments != 1 {
		t.Fatalf("result comments = %d", comments)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, realTack.URL+"/api/v1/agent/claim", strings.NewReader(`{"worker":"bridge-test"}`))
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+fixture.config.TackToken)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	data, err := io.ReadAll(response.Body)
	if err != nil || response.StatusCode != 200 || !strings.Contains(string(data), `"task":null`) {
		t.Fatalf("completed task was eligible again: %s, %v", data, err)
	}
	newTitle, ready := "Updated requirements need a new execution", true
	if _, err := database.UpdateTask(ctx, task.ID, store.UpdateTaskParams{Title: &newTitle, AgentReady: &ready}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.MoveTask(ctx, task.ID, "todo", nil, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if err := worker.Step(ctx); err == nil || !strings.Contains(err.Error(), "completed mapping") {
		t.Fatalf("manual requeue reused old completion: %v", err)
	}
	requeued, err := database.TaskByID(ctx, task.ID)
	if err != nil || requeued.Status != "doing" || requeued.Title != newTitle || !requeued.AgentReady {
		t.Fatalf("updated task was overwritten: %+v, %v", requeued, err)
	}
	currentEvents, err := database.TaskEvents(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	comments = 0
	for _, event := range currentEvents {
		if event.Kind == "commented" {
			comments++
		}
	}
	if comments != 1 || fixture.creates != 1 || fixture.starts != 1 {
		t.Fatal("requeue added an old result or dispatched again")
	}
}

func TestBridgeRejectsCompletedIDAfterRealDatabaseRollback(t *testing.T) {
	fixture := newFixture(t)
	ctx := context.Background()
	database := newRealTackStore(t, fixture)
	rollbackDirectory := t.TempDir()
	if err := database.Backup(ctx, filepath.Join(rollbackDirectory, "tack.db")); err != nil {
		t.Fatal(err)
	}
	original, err := database.CreateTask(ctx, store.CreateTaskParams{Title: "Original task", Status: "todo", AgentReady: true, Now: time.Now().UTC()})
	if err != nil {
		t.Fatal(err)
	}
	handlerFor := func(database *store.Store) http.Handler {
		return api.NewServer(appconfig.Config{PublicURL: "http://localhost", SessionSecret: bytes.Repeat([]byte{1}, 32)}, database, fstest.MapFS{}, api.BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)), time.Now).Handler()
	}
	var handlerMutex sync.RWMutex
	handler := handlerFor(database)
	realTack := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		handlerMutex.RLock()
		defer handlerMutex.RUnlock()
		handler.ServeHTTP(writer, request)
	}))
	t.Cleanup(realTack.Close)
	fixture.config.TackURL = realTack.URL
	worker := openFixture(t, fixture)
	step(t, worker)
	step(t, worker)
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := store.Open(ctx, rollbackDirectory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restored.Close() })
	replacement, err := restored.CreateTask(ctx, store.CreateTaskParams{Title: "Different task after restoring backup", Status: "todo", AgentReady: true, Now: time.Now().UTC().Add(time.Second)})
	if err != nil || replacement.ID != original.ID {
		t.Fatalf("rollback fixture did not restore the ID sequence: original=%d replacement=%d error=%v", original.ID, replacement.ID, err)
	}
	handlerMutex.Lock()
	handler = handlerFor(restored)
	handlerMutex.Unlock()
	worker = openFixture(t, fixture)
	if err := worker.Step(ctx); err == nil || !strings.Contains(err.Error(), "completed mapping") {
		t.Fatalf("rollback reused old completion: %v", err)
	}
	replacement, err = restored.TaskByID(ctx, replacement.ID)
	if err != nil || replacement.Status != "doing" || !replacement.AgentReady {
		t.Fatalf("rollback task received old result: %+v, %v", replacement, err)
	}
	links, err := restored.TaskLinks(ctx, replacement.ID)
	if err != nil || len(links) != 0 || fixture.creates != 1 || fixture.starts != 1 {
		t.Fatalf("rollback changed external associations: %+v, %v", links, err)
	}
}

func newRealTackStore(t *testing.T, fixture *fixture) *store.Store {
	t.Helper()
	ctx := context.Background()
	database, err := store.Open(ctx, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := database.BootstrapAdmin(ctx, "bridge-test", "unused-in-token-auth", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	if _, err := database.CreateAPIToken(ctx, "bridge", "tk_test", auth.HashAPIToken(fixture.config.TackToken), []string{auth.ScopeRead, auth.ScopeWrite}, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	return database
}
