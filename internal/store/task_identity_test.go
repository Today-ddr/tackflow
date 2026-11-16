package store

import (
	"errors"
	"fmt"
	"io/fs"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/today/tack/migrations"
)

func openTaskIdentityStore(test *testing.T, dataDir string) *Store {
	test.Helper()
	database, err := Open(test.Context(), dataDir)
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close() })
	if err := database.Migrate(test.Context()); err != nil {
		test.Fatal(err)
	}
	return database
}

func taskIdentityLegacyStore(test *testing.T, schemaVersion int) *Store {
	test.Helper()
	database, err := Open(test.Context(), test.TempDir())
	if err != nil {
		test.Fatal(err)
	}
	test.Cleanup(func() { _ = database.Close() })
	if _, err := database.db.ExecContext(test.Context(), "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		test.Fatal(err)
	}
	entries, err := fs.ReadDir(migrations.FS, ".")
	if err != nil {
		test.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, err := migrationVersion(entry.Name())
		if err != nil {
			test.Fatal(err)
		}
		if version > schemaVersion {
			continue
		}
		body, err := migrations.FS.ReadFile(entry.Name())
		if err != nil {
			test.Fatal(err)
		}
		if err := database.applyMigration(test.Context(), version, string(body)); err != nil {
			test.Fatal(err)
		}
	}
	return database
}

func TestTaskIdentityNeverReusedAfterPurge(test *testing.T) {
	for _, testCase := range []struct {
		name   string
		all    bool
		reopen bool
	}{
		{name: "highest"},
		{name: "all", all: true},
		{name: "highest after reopen", reopen: true},
		{name: "all after reopen", all: true, reopen: true},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			dataDir := test.TempDir()
			database := openTaskIdentityStore(test, dataDir)
			now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
			var originals []Task
			for index := range 3 {
				task, err := database.CreateTask(test.Context(), CreateTaskParams{Title: fmt.Sprintf("original %d", index), Now: now})
				if err != nil {
					test.Fatal(err)
				}
				originals = append(originals, task)
			}
			purged := originals[2:]
			if testCase.all {
				purged = originals
			}
			for _, task := range purged {
				if err := database.DeleteTask(test.Context(), task.ID, now); err != nil {
					test.Fatal(err)
				}
			}
			if count, err := database.PurgeTrash(test.Context(), now); err != nil || count != int64(len(purged)) {
				test.Fatalf("purged = %d, error = %v", count, err)
			}
			if testCase.reopen {
				if err := database.Close(); err != nil {
					test.Fatal(err)
				}
				database = openTaskIdentityStore(test, dataDir)
			}
			for _, wantID := range []int64{4, 5} {
				replacement, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "replacement", Now: now.Add(time.Hour)})
				if err != nil {
					test.Fatal(err)
				}
				if replacement.ID != wantID {
					test.Fatalf("replacement ID = %d, want %d; purged IDs must never be reused", replacement.ID, wantID)
				}
			}
			for _, task := range purged {
				if _, err := database.TaskByID(test.Context(), task.ID); !errors.Is(err, ErrNotFound) {
					test.Fatalf("purged ID %d resolves to a task: %v", task.ID, err)
				}
			}
		})
	}
}

func TestTaskIdentityUpgradePreservesExistingAndHistoricalIDs(test *testing.T) {
	for _, testCase := range []struct {
		name          string
		schemaVersion int
		purge         string
	}{
		{name: "v2 existing tasks", schemaVersion: 2, purge: "none"},
		{name: "v8 existing tasks", schemaVersion: 8, purge: "none"},
		{name: "v8 highest purged", schemaVersion: 8, purge: "highest"},
		{name: "v8 all purged", schemaVersion: 8, purge: "all"},
	} {
		test.Run(testCase.name, func(test *testing.T) {
			database := taskIdentityLegacyStore(test, testCase.schemaVersion)
			now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
			if _, err := database.db.ExecContext(test.Context(), `
				INSERT INTO task(id, title, status, position, created_at, updated_at, deleted_at) VALUES
				(7, 'retained', 'todo', 1024, ?, ?, NULL),
				(42, 'trashed', 'todo', 2048, ?, ?, ?);
				INSERT INTO task_event(task_id, kind, payload, created_at) VALUES (7, 'created', '{}', ?);
				INSERT INTO task_link(task_id, kind, external_id, created_at) VALUES (7, 'codeg', 'legacy', ?);
			`, now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
				test.Fatal(err)
			}
			if testCase.purge != "none" {
				if count, err := database.PurgeTrash(test.Context(), now); err != nil || count != 1 {
					test.Fatalf("legacy purge = %d, %v", count, err)
				}
			}
			if testCase.purge == "all" {
				if err := database.DeleteTask(test.Context(), 7, now); err != nil {
					test.Fatal(err)
				}
				if count, err := database.PurgeTrash(test.Context(), now); err != nil || count != 1 {
					test.Fatalf("legacy final purge = %d, %v", count, err)
				}
			}
			for range 2 {
				if err := database.Migrate(test.Context()); err != nil {
					test.Fatal(err)
				}
			}
			created, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "after upgrade", Now: now})
			if err != nil || created.ID != 43 {
				test.Fatalf("new task = %+v, error = %v; want ID 43", created, err)
			}
			if testCase.purge != "all" {
				retained, err := database.TaskByID(test.Context(), 7)
				if err != nil || retained.ID != 7 || retained.Title != "retained" {
					test.Fatalf("existing task changed during upgrade: %+v, %v", retained, err)
				}
				links, err := database.TaskLinks(test.Context(), 7)
				if err != nil || len(links) != 1 || links[0].ExternalID != "legacy" || links[0].TaskID != 7 {
					test.Fatalf("existing link changed during upgrade: %+v, %v", links, err)
				}
				events, err := database.TaskEvents(test.Context(), 7)
				if err != nil || len(events) != 1 || events[0].TaskID != 7 || events[0].Kind != "created" {
					test.Fatalf("existing history changed during upgrade: %+v, %v", events, err)
				}
			}
			if testCase.purge == "none" {
				var title, deletedAt string
				if err := database.db.QueryRowContext(test.Context(), "SELECT title, deleted_at FROM task WHERE id = 42").Scan(&title, &deletedAt); err != nil || title != "trashed" || deletedAt != now.Format(time.RFC3339) {
					test.Fatalf("trashed task changed during upgrade: %q, %q, %v", title, deletedAt, err)
				}
			}
		})
	}
}

func TestTaskIdentityCreateFailureRollsBackAllocationAndEvents(test *testing.T) {
	database := openTaskIdentityStore(test, test.TempDir())
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	first, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "first", Now: now})
	if err != nil || first.ID != 1 {
		test.Fatalf("first task = %+v, %v", first, err)
	}
	if _, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "must roll back", TagIDs: []int64{999}, Now: now}); err == nil {
		test.Fatal("creation with a missing tag must fail")
	}
	for _, query := range []string{
		"SELECT COUNT(*) FROM task",
		"SELECT COUNT(*) FROM task_event",
		"SELECT COUNT(*) FROM change_event WHERE resource = 'task'",
	} {
		var count int
		if err := database.db.QueryRowContext(test.Context(), query).Scan(&count); err != nil || count != 1 {
			test.Fatalf("%s = %d, %v; failed creation must leave no task or events", query, count, err)
		}
	}
	next, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "next", Now: now})
	if err != nil || next.ID != 2 {
		test.Fatalf("next task = %+v, %v; allocation must roll back with creation", next, err)
	}
}

func TestTaskIdentityConcurrentConnectionsAllocateUniqueIDs(test *testing.T) {
	dataDir := test.TempDir()
	connections := []*Store{openTaskIdentityStore(test, dataDir), openTaskIdentityStore(test, dataDir)}
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	type outcome struct {
		task Task
		err  error
	}
	results := make(chan outcome, 12)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for index := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			task, err := connections[index%2].CreateTask(test.Context(), CreateTaskParams{Title: fmt.Sprintf("concurrent %d", index), Now: now})
			results <- outcome{task: task, err: err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	seen := make(map[int64]bool)
	for result := range results {
		if result.err != nil {
			test.Fatalf("concurrent creation failed: %v", result.err)
		}
		if seen[result.task.ID] || result.task.ID < 1 || result.task.ID > 12 {
			test.Fatalf("duplicate or invalid task ID %d", result.task.ID)
		}
		seen[result.task.ID] = true
	}
	if len(seen) != 12 {
		test.Fatalf("created %d unique tasks, want 12", len(seen))
	}
}

func TestTaskIdentityExhaustionFailsWithoutReusingPurgedIDs(test *testing.T) {
	database := taskIdentityLegacyStore(test, 8)
	now := time.Date(2026, 11, 17, 1, 0, 0, 0, time.UTC)
	if _, err := database.db.ExecContext(test.Context(), `
		INSERT INTO task(id, title, status, position, created_at, updated_at)
		VALUES (?, 'last identity', 'todo', 1024, ?, ?)
	`, int64(math.MaxInt64), now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		test.Fatal(err)
	}
	if err := database.Migrate(test.Context()); err != nil {
		test.Fatal(err)
	}
	if err := database.DeleteTask(test.Context(), math.MaxInt64, now); err != nil {
		test.Fatal(err)
	}
	if count, err := database.PurgeTrash(test.Context(), now); err != nil || count != 1 {
		test.Fatalf("purged = %d, %v", count, err)
	}
	if created, err := database.CreateTask(test.Context(), CreateTaskParams{Title: "must not reuse", Now: now}); err == nil {
		test.Fatalf("exhausted identity sequence created task %+v", created)
	}
	var count int
	if err := database.db.QueryRowContext(test.Context(), "SELECT COUNT(*) FROM task").Scan(&count); err != nil || count != 0 {
		test.Fatalf("exhausted sequence left %d tasks, %v", count, err)
	}
}
