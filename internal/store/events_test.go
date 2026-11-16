package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func eventTestStore(t *testing.T) (*Store, string) {
	t.Helper()
	directory := t.TempDir()
	st, err := Open(context.Background(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	return st, directory
}

func TestChangeEventsTransactionalLifecycle(t *testing.T) {
	st, directory := eventTestStore(t)
	ctx := context.Background()
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	project, err := st.CreateProject(ctx, "project", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateTag(ctx, "tag", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(ctx, CreateTaskParams{Title: "task", ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	title := "changed"
	if _, err := st.UpdateTask(ctx, task.ID, UpdateTaskParams{Title: &title}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.MoveTask(ctx, task.ID, "doing", nil, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.AddComment(ctx, task.ID, "not exported", now); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(ctx, task.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE task SET deleted_at = NULL WHERE id = ?", task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "INSERT INTO task_event(task_id, kind, created_at) VALUES (?, 'restored', ?)", task.ID, now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	if err := st.ArchiveProject(ctx, project.ID, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "UPDATE tag SET name = 'renamed' WHERE id = ?", tag.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTag(ctx, tag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "rollback", TagIDs: []int64{99999}, Now: now}); err == nil {
		t.Fatal("invalid tag must roll back task creation")
	}
	transaction, err := st.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := transaction.ExecContext(ctx, "UPDATE project SET name = 'rollback'"); err != nil {
		t.Fatal(err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	if _, err := st.BootstrapAdmin(ctx, "admin", "secret hash", now); err != nil {
		t.Fatal(err)
	}
	if err := st.CreateSession(ctx, "session", 1, now, now.Add(time.Hour), "", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "INSERT INTO setting(key, value) VALUES ('secret', 'not exported')"); err != nil {
		t.Fatal(err)
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, filepath.Clean(directory))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	rows, err := reopened.db.QueryContext(ctx, "SELECT id, resource || '.' || kind, created_at FROM change_event ORDER BY id")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	want := []string{"project.created", "tag.created", "task.created", "task.updated", "task.moved", "task.commented", "task.deleted", "task.restored", "project.archived", "tag.updated", "tag.deleted"}
	index := 0
	var previousID int64
	for rows.Next() {
		var id int64
		var event, createdAt string
		if err := rows.Scan(&id, &event, &createdAt); err != nil {
			t.Fatal(err)
		}
		if index >= len(want) || event != want[index] || id <= previousID {
			t.Fatalf("event[%d] = %d %s, want ordered %v", index, id, event, want)
		}
		if _, err := time.Parse(time.RFC3339, createdAt); err != nil {
			t.Fatal(err)
		}
		previousID = id
		index++
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if index != len(want) {
		t.Fatalf("got %d events, want %d", index, len(want))
	}
}

func TestChangeEventsReplayBoundedAndReleasesConnection(t *testing.T) {
	st, _ := eventTestStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for index := range 205 {
		if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "event", Now: time.Now().UTC().Add(time.Duration(index) * time.Second)}); err != nil {
			t.Fatal(err)
		}
	}
	latest, err := st.LatestChangeID(ctx)
	if err != nil || latest != 205 {
		t.Fatalf("latest = %d, err = %v", latest, err)
	}
	batch, err := st.ChangesAfter(ctx, 2, 100)
	if err != nil || len(batch) != 100 || batch[0].ID != 3 || batch[99].ID != 102 {
		t.Fatalf("replay = %v, err = %v", batch, err)
	}
	if err := st.Ping(ctx); err != nil {
		t.Fatalf("event reader retained connection: %v", err)
	}
	if _, err := st.ChangesAfter(ctx, -1, 100); err == nil {
		t.Fatal("negative cursor accepted")
	}
}

func TestChangeEventsPersistAfterResourcePurgeAndNeverReuseIDs(t *testing.T) {
	st, _ := eventTestStore(t)
	ctx := t.Context()
	now := time.Now().UTC()
	task, err := st.CreateTask(ctx, CreateTaskParams{Title: "task", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(ctx, task.ID, now); err != nil {
		t.Fatal(err)
	}
	if count, err := st.PurgeTrash(ctx, now.Add(time.Second)); err != nil || count != 1 {
		t.Fatalf("purge=%d err=%v", count, err)
	}
	project, err := st.CreateProject(ctx, "project", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.db.ExecContext(ctx, "DELETE FROM project WHERE id = ?", project.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTask(ctx, CreateTaskParams{Title: "new task", Now: now}); err != nil {
		t.Fatal(err)
	}
	batch, err := st.ChangesAfter(ctx, 0, 100)
	want := []string{"task.created", "task.deleted", "task.purged", "project.created", "project.deleted", "task.created"}
	if err != nil || len(batch) != len(want) {
		t.Fatalf("purged event log=%+v err=%v", batch, err)
	}
	for index, event := range batch {
		if event.ID != int64(index+1) || event.Resource+"."+event.Kind != want[index] {
			t.Fatalf("event=%+v want=%s", event, want[index])
		}
	}
}
