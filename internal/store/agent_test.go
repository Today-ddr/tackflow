package store

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/today-ddr/tackflow/migrations"
)

func taskAReadyTask(t *testing.T, st *Store, now time.Time) Task {
	t.Helper()
	task, err := st.CreateTask(t.Context(), CreateTaskParams{Title: "ready", Status: "todo", AgentReady: true, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func TestTaskAAgentClaimConcurrentConnections(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	var databasePath string
	var sequence int
	var name string
	if err := st.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &databasePath); err != nil {
		t.Fatal(err)
	}
	peer, err := Open(t.Context(), strings.TrimSuffix(databasePath, "/tack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	type outcome struct {
		task  *Task
		claim *AgentClaim
		err   error
	}
	results := make(chan outcome, 12)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for attempt := range 12 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			connection := st
			if attempt%2 == 1 {
				connection = peer
			}
			claimed, claim, err := connection.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
			results <- outcome{claimed, claim, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	winners := 0
	for result := range results {
		if result.err != nil {
			t.Fatal(result.err)
		}
		if result.task == nil {
			if result.claim != nil {
				t.Fatal("empty task returned a lease")
			}
			continue
		}
		winners++
		if result.task.ID != task.ID || result.task.Status != "doing" || result.claim == nil || !result.claim.ExpiresAt.Equal(now.Add(30*time.Minute)) {
			t.Fatalf("bad claim: %+v, %+v", result.task, result.claim)
		}
		var storedHash string
		if err := st.db.QueryRowContext(t.Context(), "SELECT claim_hash FROM agent_claim WHERE task_id = ?", task.ID).Scan(&storedHash); err != nil {
			t.Fatal(err)
		}
		digest := sha256.Sum256([]byte(result.claim.ID))
		if storedHash != hex.EncodeToString(digest[:]) || strings.Contains(storedHash, result.claim.ID) {
			t.Fatal("claim must be stored only as its hash")
		}
	}
	if winners != 1 {
		t.Fatalf("winners = %d, want 1", winners)
	}
	events, err := st.TaskEvents(t.Context(), task.ID)
	if err != nil || len(events) != 2 || events[1].Kind != "moved" {
		t.Fatalf("claim events = %+v, err = %v", events, err)
	}
}

func TestTaskAAgentSelectionHonorsEligibilityAndScope(t *testing.T) {
	st, now := newTaskAStore(t)
	allowedProject, err := st.CreateProject(t.Context(), "allowed", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	otherProject, err := st.CreateProject(t.Context(), "other", "sky", now)
	if err != nil {
		t.Fatal(err)
	}
	allowedTag, err := st.CreateTag(t.Context(), "allowed", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []CreateTaskParams{
		{Title: "not ready", Status: "todo", ProjectID: &allowedProject.ID, TagIDs: []int64{allowedTag.ID}},
		{Title: "done", Status: "done", AgentReady: true, ProjectID: &allowedProject.ID, TagIDs: []int64{allowedTag.ID}},
		{Title: "wrong project", Status: "todo", AgentReady: true, ProjectID: &otherProject.ID, TagIDs: []int64{allowedTag.ID}},
		{Title: "missing tag", Status: "todo", AgentReady: true, ProjectID: &allowedProject.ID},
		{Title: "missing project", Status: "todo", AgentReady: true, TagIDs: []int64{allowedTag.ID}},
	} {
		params.Now = now
		if _, err := st.CreateTask(t.Context(), params); err != nil {
			t.Fatal(err)
		}
	}
	deleted, err := st.CreateTask(t.Context(), CreateTaskParams{Title: "deleted", Status: "todo", AgentReady: true, ProjectID: &allowedProject.ID, TagIDs: []int64{allowedTag.ID}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.DeleteTask(t.Context(), deleted.ID, now); err != nil {
		t.Fatal(err)
	}
	wanted, err := st.CreateTask(t.Context(), CreateTaskParams{Title: "wanted", Status: "todo", AgentReady: true, ProjectID: &allowedProject.ID, TagIDs: []int64{allowedTag.ID}, Now: now})
	if err != nil {
		t.Fatal(err)
	}
	filter := TaskFilter{AllowedProjectIDs: []int64{allowedProject.ID, 999}, AllowedTagIDs: []int64{allowedTag.ID, 999}}
	preview, err := st.NextAgentTask(t.Context(), filter)
	if err != nil || preview == nil || preview.ID != wanted.ID || preview.Status != "todo" {
		t.Fatalf("preview = %+v, err = %v", preview, err)
	}
	claimed, _, err := st.ClaimAgentTask(t.Context(), filter, "token:1", "worker", now)
	if err != nil || claimed == nil || claimed.ID != wanted.ID {
		t.Fatalf("claimed = %+v, err = %v", claimed, err)
	}
	if next, err := st.NextAgentTask(t.Context(), filter); err != nil || next != nil {
		t.Fatalf("next = %+v, err = %v", next, err)
	}
}

func TestTaskAAgentHeartbeatOwnerExpiryAndReclaim(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.HeartbeatAgentTask(t.Context(), task.ID, "token:2", claim.ID, now); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("other owner heartbeat = %v", err)
	}
	if _, err := st.HeartbeatAgentTask(t.Context(), task.ID, "token:1", "wrong", now); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("wrong claim heartbeat = %v", err)
	}
	heartbeat, err := st.HeartbeatAgentTask(t.Context(), task.ID, "token:1", claim.ID, now.Add(10*time.Minute))
	if err != nil || !heartbeat.ExpiresAt.Equal(now.Add(40*time.Minute)) {
		t.Fatalf("heartbeat = %+v, err = %v", heartbeat, err)
	}
	if _, err := st.HeartbeatAgentTask(t.Context(), task.ID, "token:1", claim.ID, now.Add(40*time.Minute)); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("expired heartbeat = %v", err)
	}
	if preview, err := st.NextAgentTask(t.Context(), TaskFilter{}); err != nil || preview != nil {
		t.Fatalf("preview requeued expired task: %+v, %v", preview, err)
	}
	reclaimed, replacement, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:2", "replacement", now.Add(40*time.Minute))
	if err != nil || reclaimed == nil || reclaimed.ID != task.ID || replacement.ID == claim.ID {
		t.Fatalf("reclaim = %+v, %+v, %v", reclaimed, replacement, err)
	}
	if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", AgentResultParams{ClaimID: claim.ID, Comment: "stale"}, now.Add(40*time.Minute)); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("stale result = %v", err)
	}
}

func TestTaskAAgentManualChangesInvalidateClaims(t *testing.T) {
	for _, change := range []string{"edit", "notes", "priority", "due", "project", "move", "done", "delete", "disable", "tags", "direct"} {
		t.Run(change, func(t *testing.T) {
			st, now := newTaskAStore(t)
			task := taskAReadyTask(t, st, now)
			_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "edit":
				title := "human edited in same second"
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{Title: &title}, now)
			case "notes":
				notes := "new instructions"
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{Notes: &notes}, now)
			case "priority":
				priority := 3
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{Priority: &priority}, now)
			case "due":
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{DueAtSet: true, DueAt: &now}, now)
			case "project":
				project, projectErr := st.CreateProject(t.Context(), "manual", "sage", now)
				if projectErr != nil {
					t.Fatal(projectErr)
				}
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{ProjectSet: true, ProjectID: &project.ID}, now)
			case "move", "done":
				status := "review"
				if change == "done" {
					status = "done"
				}
				_, err = st.MoveTask(t.Context(), task.ID, status, nil, now)
			case "delete":
				err = st.DeleteTask(t.Context(), task.ID, now)
			case "disable":
				ready := false
				_, err = st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{AgentReady: &ready}, now)
			case "tags":
				tag, tagErr := st.CreateTag(t.Context(), "manual", "sage", now)
				if tagErr != nil {
					t.Fatal(tagErr)
				}
				_, err = st.db.ExecContext(t.Context(), "INSERT INTO task_tag(task_id, tag_id) VALUES (?, ?)", task.ID, tag.ID)
			case "direct":
				_, err = st.db.ExecContext(t.Context(), "UPDATE task SET title = 'manual' WHERE id = ?", task.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := st.HeartbeatAgentTask(t.Context(), task.ID, "token:1", claim.ID, now); !errors.Is(err, ErrAgentClaimLost) {
				t.Fatalf("manual change retained heartbeat: %v", err)
			}
			if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", AgentResultParams{ClaimID: claim.ID, Comment: "late"}, now); !errors.Is(err, ErrAgentClaimLost) {
				t.Fatalf("manual change retained result: %v", err)
			}
			claimed, lease, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:2", "replacement", now.Add(time.Hour))
			if err != nil || claimed != nil || lease != nil {
				t.Fatalf("manual task resurrected: %+v, %+v, %v", claimed, lease, err)
			}
		})
	}
}

func TestTaskAAgentResultAtomicAndIdempotent(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	params := AgentResultParams{ClaimID: claim.ID, Comment: "ready for review", Links: []AgentResultLink{
		{Kind: "codeg_work_task", ExternalID: "42", URL: "codeg://task/42"},
		{Kind: "codeg_work_task", ExternalID: "42", URL: "codeg://task/42"},
	}}
	completed, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now.Add(time.Minute))
	if err != nil || completed.Status != "review" || completed.AgentReady || completed.CompletedAt != nil {
		t.Fatalf("result = %+v, err = %v", completed, err)
	}
	events, err := st.TaskEvents(t.Context(), task.ID)
	if err != nil {
		t.Fatal(err)
	}
	links, err := st.TaskLinks(t.Context(), task.ID)
	if err != nil || len(links) != 1 || links[0].ExternalID != "42" {
		t.Fatalf("links = %+v, err = %v", links, err)
	}
	counts := map[string]int{}
	for _, event := range events {
		counts[event.Kind]++
	}
	if counts["commented"] != 1 || counts["linked"] != 1 || counts["moved"] != 2 {
		t.Fatalf("events = %+v", counts)
	}
	replayed, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now.Add(2*time.Minute))
	if err != nil || replayed.Status != "review" || !replayed.UpdatedAt.Equal(completed.UpdatedAt) {
		t.Fatalf("replay = %+v, err = %v", replayed, err)
	}
	after, err := st.TaskEvents(t.Context(), task.ID)
	if err != nil || len(after) != len(events) {
		t.Fatalf("replay duplicated events: %+v, %v", after, err)
	}
	params.Comment = "different result"
	if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now.Add(2*time.Minute)); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("different result replay = %v", err)
	}
}

func TestTaskAAgentResultConflictRollsBack(t *testing.T) {
	st, now := newTaskAStore(t)
	first := taskAReadyTask(t, st, now)
	second := taskAReadyTask(t, st, now)
	_, firstClaim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "first", now)
	if err != nil {
		t.Fatal(err)
	}
	_, secondClaim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:2", "second", now)
	if err != nil {
		t.Fatal(err)
	}
	params := AgentResultParams{ClaimID: firstClaim.ID, Status: "done", Comment: "finished", Links: []AgentResultLink{{Kind: "issue", ExternalID: "1", URL: "https://example.com/1"}}}
	finished, err := st.CompleteAgentTask(t.Context(), first.ID, "token:1", params, now)
	if err != nil || finished.CompletedAt == nil || finished.AgentReady || finished.Status != "done" {
		t.Fatalf("done = %+v, %v", finished, err)
	}
	params.ClaimID = secondClaim.ID
	params.Links = append([]AgentResultLink{{Kind: "issue", ExternalID: "0"}}, params.Links...)
	if _, err := st.CompleteAgentTask(t.Context(), second.ID, "token:2", params, now); !errors.Is(err, ErrAgentLinkConflict) {
		t.Fatalf("link conflict = %v", err)
	}
	unchanged, err := st.TaskByID(t.Context(), second.ID)
	if err != nil || unchanged.Status != "doing" || !unchanged.AgentReady {
		t.Fatalf("conflict changed task = %+v, %v", unchanged, err)
	}
	links, err := st.TaskLinks(t.Context(), second.ID)
	if err != nil || len(links) != 0 {
		t.Fatalf("conflict leaked links = %+v, %v", links, err)
	}
	events, err := st.TaskEvents(t.Context(), second.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("conflict leaked events = %+v, %v", events, err)
	}
	params.Status, params.Links = "todo", nil
	returned, err := st.CompleteAgentTask(t.Context(), second.ID, "token:2", params, now)
	if err != nil || returned.Status != "todo" || !returned.AgentReady || returned.CompletedAt != nil {
		t.Fatalf("return to queue = %+v, %v", returned, err)
	}
}

func TestTaskAAgentRejectsInvalidResultAndWorker(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	for _, worker := range []string{"", " ", strings.Repeat("界", 101)} {
		if _, _, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", worker, now); !errors.Is(err, ErrInvalidAgentInput) {
			t.Errorf("worker validation = %v", err)
		}
	}
	_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, params := range []AgentResultParams{
		{Comment: ""}, {Comment: strings.Repeat("x", 10001)}, {Comment: "ok", Status: "doing"},
		{Comment: "ok", Links: make([]AgentResultLink, 21)},
		{Comment: "ok", Links: []AgentResultLink{{Kind: "link", ExternalID: "1", URL: "javascript:alert(1)"}}},
		{Comment: "ok", Links: []AgentResultLink{{Kind: "link", ExternalID: "1", URL: "file:///tmp/private"}}},
		{Comment: "ok", Links: []AgentResultLink{{Kind: "link", ExternalID: "1", URL: "//example.com"}}},
		{Comment: "ok", Links: []AgentResultLink{{Kind: "link", ExternalID: "1", URL: "https://"}}},
	} {
		params.ClaimID = claim.ID
		if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now); !errors.Is(err, ErrInvalidAgentInput) {
			t.Errorf("result validation = %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := st.CompleteAgentTask(ctx, task.ID, "token:1", AgentResultParams{ClaimID: claim.ID, Comment: "cancelled"}, now); err == nil {
		t.Fatal("cancelled result succeeded")
	}
}

func TestTaskAAgentMigrationUpgradesV2(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.ExecContext(t.Context(), "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{"0001_init.sql", "0002_m1_core.sql"} {
		migration, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(t.Context(), index+1, string(migration)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := st.db.ExecContext(t.Context(), `
		INSERT INTO task(id, title, status, agent_ready, position, created_at, updated_at)
		VALUES (42, 'legacy ready', 'todo', 1, 1024, ?, ?)
	`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := st.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	claimed, lease, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "upgraded", now)
	if err != nil || claimed == nil || claimed.ID != 42 || lease == nil {
		t.Fatalf("upgraded claim = %+v, %+v, %v", claimed, lease, err)
	}
}

func TestTaskAAgentResultRetryCannotOverwriteManualChanges(t *testing.T) {
	st, now := newTaskAStore(t)
	task := taskAReadyTask(t, st, now)
	_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now)
	if err != nil {
		t.Fatal(err)
	}
	params := AgentResultParams{ClaimID: claim.ID, Comment: "result"}
	if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now); err != nil {
		t.Fatal(err)
	}
	title := "human follow-up"
	if _, err := st.UpdateTask(t.Context(), task.ID, UpdateTaskParams{Title: &title}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.CompleteAgentTask(t.Context(), task.ID, "token:1", params, now); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("manual follow-up accepted retry: %v", err)
	}
}

func taskAClaimReplay(t *testing.T, key string) *AgentClaimReplay {
	t.Helper()
	block, err := aes.NewCipher(bytes.Repeat([]byte{7}, 32))
	if err != nil {
		t.Fatal(err)
	}
	encryption, err := cipher.NewGCMWithRandomNonce(block)
	if err != nil {
		t.Fatal(err)
	}
	return &AgentClaimReplay{
		Key: key,
		Seal: func(key string, body []byte) ([]byte, error) {
			return encryption.Seal(nil, nil, body, []byte(key)), nil
		},
		Open: func(key string, body []byte) ([]byte, error) {
			return encryption.Open(nil, nil, body, []byte(key))
		},
	}
}

func taskAStoreDirectory(t *testing.T, st *Store) string {
	t.Helper()
	var sequence int
	var name, path string
	if err := st.db.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&sequence, &name, &path); err != nil {
		t.Fatal(err)
	}
	return filepath.Dir(path)
}

func taskAAssertClaimCounts(t *testing.T, st *Store, taskID int64, status string, events int) {
	t.Helper()
	task, err := st.TaskByID(t.Context(), taskID)
	if err != nil || task.Status != status {
		t.Fatalf("task = %+v, err = %v; want status %s", task, err, status)
	}
	items, err := st.TaskEvents(t.Context(), taskID)
	if err != nil || len(items) != events {
		t.Fatalf("events = %+v, err = %v; want count %d", items, err, events)
	}
}

func TestTaskAAgentClaimReplaySurvivesReopen(t *testing.T) {
	st, now := newTaskAStore(t)
	directory := taskAStoreDirectory(t, st)
	first := taskAReadyTask(t, st, now)
	second := taskAReadyTask(t, st, now)
	replay := taskAClaimReplay(t, "bound-key")
	claimed, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
	if err != nil || claimed == nil || claimed.ID != first.ID || claim == nil || replay.Replayed {
		t.Fatalf("claim = %+v, %+v, %v, replay=%v", claimed, claim, err, replay.Replayed)
	}
	var body []byte
	if err := st.db.QueryRowContext(t.Context(), "SELECT body FROM agent_claim_replay WHERE key = ?", replay.Key).Scan(&body); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(body, []byte(claim.ID)) || bytes.Contains(body, []byte(`"Title"`)) {
		t.Fatal("claim replay persisted plaintext")
	}
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	replay = taskAClaimReplay(t, "bound-key")
	retried, retryClaim, err := reopened.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now.Add(time.Minute), replay)
	if err != nil || retried == nil || retried.ID != first.ID || retryClaim == nil || *retryClaim != *claim || !replay.Replayed {
		t.Fatalf("reopened retry = %+v, %+v, %v, replay=%v", retried, retryClaim, err, replay.Replayed)
	}
	taskAAssertClaimCounts(t, reopened, first.ID, "doing", 2)
	taskAAssertClaimCounts(t, reopened, second.ID, "todo", 1)
}

func TestTaskAAgentClaimReplayPersistenceFailureRollsBack(t *testing.T) {
	for _, failure := range []string{"seal", "insert", "after insert", "cancel"} {
		t.Run(failure, func(t *testing.T) {
			st, now := newTaskAStore(t)
			first := taskAReadyTask(t, st, now)
			second := taskAReadyTask(t, st, now)
			for range 2 {
				replay := taskAClaimReplay(t, "retry-key")
				ctx, cancel := context.WithCancel(t.Context())
				switch failure {
				case "seal":
					replay.Seal = func(string, []byte) ([]byte, error) { return nil, errors.New("injected seal failure") }
				case "insert", "after insert":
					timing := "BEFORE"
					if failure == "after insert" {
						timing = "AFTER"
					}
					if _, err := st.db.ExecContext(t.Context(), "CREATE TRIGGER IF NOT EXISTS fail_replay "+timing+" INSERT ON agent_claim_replay BEGIN SELECT RAISE(FAIL, 'injected replay persistence failure'); END"); err != nil {
						t.Fatal(err)
					}
				case "cancel":
					seal := replay.Seal
					replay.Seal = func(key string, body []byte) ([]byte, error) {
						cancel()
						return seal(key, body)
					}
				}
				claimed, claim, err := st.ClaimAgentTask(ctx, TaskFilter{}, "token:1", "worker", now, replay)
				cancel()
				if err == nil || claimed != nil || claim != nil || replay.Replayed {
					t.Fatalf("failure returned success: %+v, %+v, %v", claimed, claim, err)
				}
				taskAAssertClaimCounts(t, st, first.ID, "todo", 1)
				taskAAssertClaimCounts(t, st, second.ID, "todo", 1)
				var claims, replays, moves int
				if err := st.db.QueryRowContext(t.Context(), `SELECT
					(SELECT COUNT(*) FROM agent_claim), (SELECT COUNT(*) FROM agent_claim_replay),
					(SELECT COUNT(*) FROM change_event WHERE kind = 'moved')`).Scan(&claims, &replays, &moves); err != nil {
					t.Fatal(err)
				}
				if claims != 0 || replays != 0 || moves != 0 {
					t.Fatalf("failed claim retained writes: claims=%d replays=%d moves=%d", claims, replays, moves)
				}
			}
			if _, err := st.db.ExecContext(t.Context(), "DROP TRIGGER IF EXISTS fail_replay"); err != nil {
				t.Fatal(err)
			}
			replay := taskAClaimReplay(t, "retry-key")
			claimed, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
			if err != nil || claimed == nil || claimed.ID != first.ID || claim == nil {
				t.Fatalf("recovered claim = %+v, %+v, %v", claimed, claim, err)
			}
			taskAAssertClaimCounts(t, st, second.ID, "todo", 1)
		})
	}
}

func TestTaskAAgentClaimReplayConcurrentConnections(t *testing.T) {
	st, now := newTaskAStore(t)
	first := taskAReadyTask(t, st, now)
	second := taskAReadyTask(t, st, now)
	peer, err := Open(t.Context(), taskAStoreDirectory(t, st))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	type outcome struct {
		task     *Task
		claim    *AgentClaim
		err      error
		replayed bool
	}
	results := make(chan outcome, 12)
	start := make(chan struct{})
	for attempt := range 12 {
		replay := taskAClaimReplay(t, "concurrent-key")
		go func() {
			<-start
			connection := st
			if attempt%2 == 1 {
				connection = peer
			}
			task, claim, err := connection.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
			results <- outcome{task, claim, err, replay.Replayed}
		}()
	}
	close(start)
	claimID := ""
	replayed := 0
	for range 12 {
		result := <-results
		if result.err != nil || result.task == nil || result.task.ID != first.ID || result.claim == nil {
			t.Fatalf("concurrent retry = %+v", result)
		}
		if claimID == "" {
			claimID = result.claim.ID
		}
		if result.claim.ID != claimID {
			t.Fatal("concurrent retries got different leases")
		}
		if result.replayed {
			replayed++
		}
	}
	if replayed != 11 {
		t.Fatalf("replays = %d, want 11", replayed)
	}
	taskAAssertClaimCounts(t, st, first.ID, "doing", 2)
	taskAAssertClaimCounts(t, st, second.ID, "todo", 1)
}

func TestTaskAAgentClaimReplayFailsClosed(t *testing.T) {
	for _, change := range []string{"expired", "invalidated", "completed", "project ACL", "tag ACL", "deleted", "purged", "replaced", "tampered", "open failure", "owner"} {
		t.Run(change, func(t *testing.T) {
			st, now := newTaskAStore(t)
			first := taskAReadyTask(t, st, now)
			second := taskAReadyTask(t, st, now)
			replay := taskAClaimReplay(t, "one-allocation")
			_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
			if err != nil {
				t.Fatal(err)
			}
			filter := TaskFilter{}
			owner := "token:1"
			want := ErrAgentClaimLost
			switch change {
			case "expired":
				now = now.Add(30 * time.Minute)
			case "invalidated":
				title := "manual edit"
				_, err = st.UpdateTask(t.Context(), first.ID, UpdateTaskParams{Title: &title}, now)
			case "completed":
				_, err = st.CompleteAgentTask(t.Context(), first.ID, "token:1", AgentResultParams{ClaimID: claim.ID, Comment: "done"}, now)
			case "project ACL":
				filter.AllowedProjectIDs = []int64{999}
				want = ErrNotFound
			case "tag ACL":
				filter.AllowedTagIDs = []int64{999}
				want = ErrNotFound
			case "deleted":
				err = st.DeleteTask(t.Context(), first.ID, now)
				want = ErrNotFound
			case "purged":
				_, err = st.db.ExecContext(t.Context(), "DELETE FROM task WHERE id = ?", first.ID)
				want = ErrNotFound
			case "replaced":
				_, err = st.db.ExecContext(t.Context(), "UPDATE agent_claim SET claim_hash = 'replacement' WHERE task_id = ?", first.ID)
			case "tampered":
				_, err = st.db.ExecContext(t.Context(), "UPDATE agent_claim_replay SET body = 'corrupt' WHERE key = ?", replay.Key)
				want = nil
			case "open failure":
				replay.Open = func(string, []byte) ([]byte, error) { return nil, errors.New("injected decrypt failure") }
				want = nil
			case "owner":
				owner = "token:2"
			}
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				task, lease, err := st.ClaimAgentTask(t.Context(), filter, owner, "worker", now, replay)
				if err == nil || want != nil && !errors.Is(err, want) || task != nil || lease != nil || replay.Replayed {
					t.Fatalf("unsafe replay = %+v, %+v, %v, want %v", task, lease, err, want)
				}
			}
			taskAAssertClaimCounts(t, st, second.ID, "todo", 1)
		})
	}
}

func TestTaskAAgentClaimReplayRemembersEmptyQueue(t *testing.T) {
	st, now := newTaskAStore(t)
	replay := taskAClaimReplay(t, "empty-queue")
	task, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
	if err != nil || task != nil || claim != nil || replay.Replayed {
		t.Fatalf("empty claim = %+v, %+v, %v", task, claim, err)
	}
	created := taskAReadyTask(t, st, now)
	task, claim, err = st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
	if err != nil || task != nil || claim != nil || !replay.Replayed {
		t.Fatalf("empty replay = %+v, %+v, %v", task, claim, err)
	}
	taskAAssertClaimCounts(t, st, created.ID, "todo", 1)
	task, claim, err = st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now.Add(24*time.Hour), replay)
	if !errors.Is(err, ErrAgentClaimLost) || task != nil || claim != nil || replay.Replayed {
		t.Fatalf("expired key allocated again: %+v, %+v, %v", task, claim, err)
	}
	taskAAssertClaimCounts(t, st, created.ID, "todo", 1)
}

func TestTaskAAgentReplayMigrationPreservesExistingClaims(t *testing.T) {
	st, err := Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if _, err := st.db.ExecContext(t.Context(), "CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)"); err != nil {
		t.Fatal(err)
	}
	for index, name := range []string{
		"0001_init.sql", "0002_m1_core.sql", "0003_token_resources.sql", "0004_agent.sql",
		"0005_passkeys.sql", "0006_events.sql", "0007_resource_identity.sql",
	} {
		body, err := migrations.FS.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := st.applyMigration(t.Context(), index+1, string(body)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	if _, err := st.db.ExecContext(t.Context(), `
		INSERT INTO task(id, title, status, agent_ready, position, created_at, updated_at)
		VALUES (42, 'existing ready', 'todo', 1, 1024, ?, ?)
	`, now.Format(time.RFC3339), now.Format(time.RFC3339)); err != nil {
		t.Fatal(err)
	}
	_, claim, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "existing worker", now)
	if err != nil || claim == nil {
		t.Fatalf("pre-migration claim = %+v, %v", claim, err)
	}
	for range 2 {
		if err := st.Migrate(t.Context()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.db.ExecContext(t.Context(), "UPDATE task SET position = 2048, updated_at = updated_at WHERE id = 42"); err != nil {
		t.Fatal(err)
	}
	if _, err := st.HeartbeatAgentTask(t.Context(), 42, "token:1", claim.ID, now); err != nil {
		t.Fatalf("migration or position maintenance invalidated existing claim: %v", err)
	}
	replay := taskAClaimReplay(t, "migrated-replay")
	task, lease, err := st.ClaimAgentTask(t.Context(), TaskFilter{}, "token:1", "worker", now, replay)
	if err != nil || task != nil || lease != nil {
		t.Fatalf("new replay table not usable: %+v, %+v, %v", task, lease, err)
	}
	title := "manual change"
	if _, err := st.UpdateTask(t.Context(), 42, UpdateTaskParams{Title: &title}, now); err != nil {
		t.Fatal(err)
	}
	if _, err := st.HeartbeatAgentTask(t.Context(), 42, "token:1", claim.ID, now); !errors.Is(err, ErrAgentClaimLost) {
		t.Fatalf("upgrade disabled meaningful invalidation: %v", err)
	}
}
