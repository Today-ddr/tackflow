package store

import (
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestTaskATrashPaginationRetainsStatusAndMetadata(t *testing.T) {
	st, now := newTaskAStore(t)
	project, err := st.CreateProject(t.Context(), "project", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := st.CreateTag(t.Context(), "tag", "sky", now)
	if err != nil {
		t.Fatal(err)
	}
	var ids []int64
	for _, status := range []string{"inbox", "todo", "doing", "review", "done"} {
		task, err := st.CreateTask(t.Context(), CreateTaskParams{Title: status, Status: status, ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: now})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.DeleteTask(t.Context(), task.ID, now); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, task.ID)
	}
	active := taskAReadyTask(t, st, now)
	expired := taskAReadyTask(t, st, now.Add(-31*24*time.Hour))
	if err := st.DeleteTask(t.Context(), expired.ID, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	cursor := ""
	for page := range 3 {
		items, next, err := st.ListTrash(t.Context(), 2, cursor, now)
		if err != nil || len(items) == 0 || len(items) > 2 {
			t.Fatalf("page = %+v, cursor = %q, err = %v", items, next, err)
		}
		if page == 0 && (items[0].ID != ids[4] || items[0].Status != "done" || items[1].ID != ids[3]) {
			t.Fatalf("unstable order or changed status: %+v", items)
		}
		for _, item := range items {
			if seen[item.ID] || item.ID == active.ID || item.ID == expired.ID || item.DeletedAt == nil || item.Project == nil || len(item.Tags) != 1 {
				t.Fatalf("unexpected trash task: %+v", item)
			}
			seen[item.ID] = true
		}
		cursor = next
	}
	if len(seen) != 5 || cursor != "" {
		t.Fatalf("seen = %+v, cursor = %q", seen, cursor)
	}
	for _, invalid := range []string{"!", base64.RawURLEncoding.EncodeToString([]byte("bad:1")), base64.RawURLEncoding.EncodeToString([]byte(`{"deleted_at":"2026-11-16T09:00:00Z","id":0}`))} {
		if _, _, err := st.ListTrash(t.Context(), 2, invalid, now); !errors.Is(err, ErrInvalidCursor) {
			t.Fatalf("invalid cursor %q = %v", invalid, err)
		}
	}
}

func TestTaskATrashRestoreLifecycle(t *testing.T) {
	for _, status := range []string{"inbox", "todo", "doing", "review", "done"} {
		t.Run(status, func(t *testing.T) {
			st, now := newTaskAStore(t)
			project, err := st.CreateProject(t.Context(), "project", "sage", now)
			if err != nil {
				t.Fatal(err)
			}
			tag, err := st.CreateTag(t.Context(), "tag", "sky", now)
			if err != nil {
				t.Fatal(err)
			}
			task, err := st.CreateTask(t.Context(), CreateTaskParams{Title: status, Status: status, AgentReady: true, ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			if err := st.DeleteTask(t.Context(), task.ID, now); err != nil {
				t.Fatal(err)
			}
			if err := st.ArchiveProject(t.Context(), project.ID, now); err != nil {
				t.Fatal(err)
			}
			restored, err := st.RestoreTask(t.Context(), task.ID, now.Add(29*24*time.Hour))
			if err != nil || restored.DeletedAt != nil || restored.Status != status || restored.ProjectID == nil || *restored.ProjectID != project.ID || len(restored.Tags) != 1 || restored.Tags[0].ID != tag.ID {
				t.Fatalf("restore = %+v, err = %v", restored, err)
			}
			if status == "done" && (restored.CompletedAt == nil || !restored.CompletedAt.Equal(now)) {
				t.Fatalf("lost completion time: %+v", restored)
			}
			if status != "done" && restored.CompletedAt != nil {
				t.Fatalf("unexpected completion time: %+v", restored)
			}
			events, err := st.TaskEvents(t.Context(), task.ID)
			if err != nil || events[len(events)-1].Kind != "restored" {
				t.Fatalf("restore event missing: %+v, %v", events, err)
			}
			if _, err := st.RestoreTask(t.Context(), task.ID, now.Add(29*24*time.Hour)); !errors.Is(err, ErrNotFound) {
				t.Fatalf("restore active task = %v", err)
			}
		})
	}
}

func TestTaskATrashRestoreMissingExpiredAndDeletedTag(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	if err := st.DeleteTask(t.Context(), task.ID, now.Add(-30*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{task.ID, 99999} {
		if _, err := st.RestoreTask(t.Context(), id, now); !errors.Is(err, ErrNotFound) {
			t.Fatalf("restore expired/missing %d = %v", id, err)
		}
	}
	tag, err := st.CreateTag(t.Context(), "removed", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	tagged, err := st.CreateTask(t.Context(), CreateTaskParams{Title: "tagged", TagIDs: []int64{tag.ID}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(t.Context(), tagged.ID, now); err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTag(t.Context(), tag.ID); err != nil {
		t.Fatal(err)
	}
	restored, err := st.RestoreTask(t.Context(), tagged.ID, now)
	if err != nil || len(restored.Tags) != 0 {
		t.Fatalf("restore removed tag = %+v, %v", restored, err)
	}
}

func TestTaskATrashAndAgentQueriesUseIndexes(t *testing.T) {
	st, _ := newTaskAStore(t)
	for _, query := range []string{
		"SELECT id FROM task WHERE deleted_at IS NOT NULL AND deleted_at > '2026-01-01T00:00:00Z' ORDER BY deleted_at DESC, id DESC LIMIT 101",
		"SELECT id FROM task WHERE deleted_at IS NULL AND agent_ready = 1 AND status = 'todo' ORDER BY position, id LIMIT 1",
	} {
		rows, err := st.db.QueryContext(t.Context(), "EXPLAIN QUERY PLAN "+query)
		if err != nil {
			t.Fatal(err)
		}
		var descriptions []string
		for rows.Next() {
			var id, parent, unused int
			var description string
			if err := rows.Scan(&id, &parent, &unused, &description); err != nil {
				_ = rows.Close()
				t.Fatal(err)
			}
			descriptions = append(descriptions, description)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		plan := fmt.Sprint(descriptions)
		t.Log(plan)
		if !strings.Contains(plan, "USING") || !strings.Contains(plan, "INDEX") || strings.Contains(plan, "SCAN task") {
			t.Fatalf("query missed index: %s", plan)
		}
	}
}
