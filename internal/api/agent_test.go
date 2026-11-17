package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/store"
)

type taskAClaimResponse struct {
	Task  *taskDTO `json:"task"`
	Claim *struct {
		ID        string `json:"id"`
		ExpiresAt string `json:"expires_at"`
	} `json:"claim"`
}

func taskAAPIReady(t *testing.T, fixture *taskAFixture) store.Task {
	t.Helper()
	task, err := fixture.store.CreateTask(t.Context(), store.CreateTaskParams{Title: "ready", Status: "todo", AgentReady: true, Now: fixture.now})
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func taskAAPIClaim(t *testing.T, fixture *taskAFixture, token string) taskAClaimResponse {
	t.Helper()
	response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"test worker"}`, token, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("claim = %d: %s", response.Code, response.Body.String())
	}
	var claim taskAClaimResponse
	if err := json.Unmarshal(response.Body.Bytes(), &claim); err != nil {
		t.Fatal(err)
	}
	return claim
}

func TestTaskAAgentAPIReadAndClaimScopes(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerAgentRoutes(fixture.mux)
	task := taskAAPIReady(t, fixture)
	readToken := fixture.token(t, auth.ScopeRead)
	for _, path := range []string{"/api/v1/agent/next?claim=1", "/api/v1/agent/next"} {
		response := fixture.request(t, http.MethodGet, path, "", readToken, nil)
		if strings.Contains(path, "claim") {
			if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "/agent/claim") {
				t.Fatalf("legacy claim = %d: %s", response.Code, response.Body.String())
			}
		} else if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"todo"`) {
			t.Fatalf("preview = %d: %s", response.Code, response.Body.String())
		}
	}
	events, err := fixture.store.TaskEvents(t.Context(), task.ID)
	if err != nil || len(events) != 1 {
		t.Fatalf("GET mutated task: %+v, %v", events, err)
	}
	response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"read only"}`, readToken, nil)
	if response.Code != http.StatusForbidden {
		t.Fatalf("read token claim = %d", response.Code)
	}
	for _, headers := range []map[string]string{{"X-Tack-Client": ""}, {"Origin": "https://foreign.example"}} {
		response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"session"}`, "", headers)
		if response.Code != http.StatusForbidden {
			t.Fatalf("CSRF claim = %d", response.Code)
		}
	}
	writer := fixture.token(t, auth.ScopeWrite)
	claimed := taskAAPIClaim(t, fixture, writer)
	if claimed.Task == nil || claimed.Task.ID != task.ID || claimed.Task.Status != "doing" || claimed.Claim == nil || claimed.Claim.ID == "" || claimed.Claim.ExpiresAt != "2026-11-16T09:30:00Z" {
		t.Fatalf("claimed = %+v", claimed)
	}
	empty := taskAAPIClaim(t, fixture, writer)
	if empty.Task != nil || empty.Claim != nil {
		t.Fatalf("empty queue = %+v", empty)
	}
}

func TestTaskAAgentAPIHeartbeatResultAndStaleRetries(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerAgentRoutes(fixture.mux)
	task := taskAAPIReady(t, fixture)
	writer, other := fixture.token(t, auth.ScopeWrite), fixture.token(t, auth.ScopeWrite)
	claimed := taskAAPIClaim(t, fixture, writer)
	heartbeatBody := fmt.Sprintf(`{"claim_id":%q}`, claimed.Claim.ID)
	resultBody := fmt.Sprintf(`{"claim_id":%q,"comment":"review me","links":[{"kind":"codeg_work_task","external_id":"17","url":"codeg://task/17"}]}`, claimed.Claim.ID)
	path := "/api/v1/agent/tasks/" + itoa(task.ID)
	for _, action := range []string{"heartbeat", "result"} {
		body := heartbeatBody
		if action == "result" {
			body = resultBody
		}
		response := fixture.request(t, http.MethodPost, path+"/"+action, body, other, nil)
		if response.Code != http.StatusConflict {
			t.Fatalf("other owner %s = %d", action, response.Code)
		}
	}
	fixture.now = fixture.now.Add(10 * time.Minute)
	response := fixture.request(t, http.MethodPost, path+"/heartbeat", heartbeatBody, writer, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"expires_at":"2026-11-16T09:40:00Z"`) {
		t.Fatalf("heartbeat = %d: %s", response.Code, response.Body.String())
	}
	for attempt := range 2 {
		response = fixture.request(t, http.MethodPost, path+"/result", resultBody, writer, map[string]string{"Idempotency-Key": "same-result"})
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"review"`) || !strings.Contains(response.Body.String(), `"agent_ready":false`) {
			t.Fatalf("result %d = %d: %s", attempt, response.Code, response.Body.String())
		}
	}
	fixture.now = fixture.now.Add(30 * time.Minute)
	for _, action := range []string{"heartbeat", "result"} {
		body := heartbeatBody
		if action == "result" {
			body = resultBody
		}
		response = fixture.request(t, http.MethodPost, path+"/"+action, body, writer, map[string]string{"Idempotency-Key": "same-result"})
		if response.Code != http.StatusConflict {
			t.Fatalf("expired %s = %d: %s", action, response.Code, response.Body.String())
		}
	}
}

func TestTaskAAgentAPIValidationAndSessionOwnership(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerAgentRoutes(fixture.mux)
	task := taskAAPIReady(t, fixture)
	for _, body := range []string{`{}`, `{"worker":""}`, fmt.Sprintf(`{"worker":%q}`, strings.Repeat("a", 101))} {
		response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", body, "", nil)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid worker = %d: %s", response.Code, response.Body.String())
		}
	}
	claimed := taskAAPIClaim(t, fixture, "")
	for _, body := range []string{
		fmt.Sprintf(`{"claim_id":%q,"comment":"ok","status":"doing"}`, claimed.Claim.ID),
		fmt.Sprintf(`{"claim_id":%q,"comment":"ok","links":[{"kind":"link","external_id":"1","url":"javascript:alert(1)"}]}`, claimed.Claim.ID),
		fmt.Sprintf(`{"claim_id":%q,"comment":%q}`, claimed.Claim.ID, strings.Repeat("a", 10001)),
	} {
		response := fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(task.ID)+"/result", body, "", nil)
		if response.Code != http.StatusUnprocessableEntity {
			t.Fatalf("invalid result = %d: %s", response.Code, response.Body.String())
		}
	}
	body := fmt.Sprintf(`{"claim_id":%q,"comment":"done","status":"done"}`, claimed.Claim.ID)
	response := fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(task.ID)+"/result", body, fixture.token(t, auth.ScopeWrite), nil)
	if response.Code != http.StatusConflict {
		t.Fatalf("token used session claim = %d", response.Code)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(task.ID)+"/result", body, "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"done"`) {
		t.Fatalf("session result = %d: %s", response.Code, response.Body.String())
	}
}

func TestTaskAAgentAPIResourceRestrictions(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerAgentRoutes(fixture.mux)
	project, err := fixture.store.CreateProject(t.Context(), "allowed", "sage", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	tag, err := fixture.store.CreateTag(t.Context(), "allowed", "sage", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	hidden := taskAAPIReady(t, fixture)
	visible, err := fixture.store.CreateTask(t.Context(), store.CreateTaskParams{Title: "visible", Status: "todo", AgentReady: true, ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: fixture.now})
	if err != nil {
		t.Fatal(err)
	}
	token, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.store.CreateAPIToken(t.Context(), "restricted", prefix, hash, []string{auth.ScopeWrite}, fixture.now, store.TokenOptions{ProjectIDs: []int64{project.ID}, TagIDs: []int64{tag.ID}}); err != nil {
		t.Fatal(err)
	}
	response := fixture.request(t, http.MethodGet, "/api/v1/agent/next", "", token, nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"title":"visible"`) {
		t.Fatalf("scoped preview = %d: %s", response.Code, response.Body.String())
	}
	claimed := taskAAPIClaim(t, fixture, token)
	if claimed.Task == nil || claimed.Task.ID != visible.ID {
		t.Fatalf("scoped claim = %+v", claimed)
	}
	for _, action := range []string{"heartbeat", "result"} {
		response := fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(hidden.ID)+"/"+action, `{"claim_id":"other","comment":"stolen"}`, token, nil)
		if response.Code != http.StatusNotFound {
			t.Fatalf("out-of-scope %s = %d", action, response.Code)
		}
	}
	if _, err := fixture.store.UpdateTask(t.Context(), visible.ID, store.UpdateTaskParams{ProjectSet: true}, fixture.now); err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(visible.ID)+"/result", fmt.Sprintf(`{"claim_id":%q,"comment":"out of scope"}`, claimed.Claim.ID), token, nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("moved-out-of-scope result = %d", response.Code)
	}
}

func TestTaskAAgentAPIClaimIdempotentRetry(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.server.registerAgentRoutes(fixture.mux)
	first := taskAAPIReady(t, fixture)
	second := taskAAPIReady(t, fixture)
	token := fixture.token(t, auth.ScopeWrite)
	headers := map[string]string{"Idempotency-Key": "bridge-acquire"}
	response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
	if response.Code != http.StatusOK {
		t.Fatalf("first claim = %d: %s", response.Code, response.Body.String())
	}
	initial := response.Body.String()
	var claimed taskAClaimResponse
	if err := json.Unmarshal(response.Body.Bytes(), &claimed); err != nil {
		t.Fatal(err)
	}
	if claimed.Task == nil || claimed.Task.ID != first.ID || claimed.Claim == nil {
		t.Fatalf("first claim = %+v", claimed)
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
	if response.Code != http.StatusOK || response.Body.String() != initial || response.Header().Get("X-Tack-Idempotent-Replay") != "true" {
		t.Fatalf("retry changed claim: status=%d body=%s, first=%s", response.Code, response.Body.String(), initial)
	}
	untouched, err := fixture.store.TaskByID(t.Context(), second.ID)
	if err != nil || untouched.Status != "todo" {
		t.Fatalf("retry consumed next task: %+v, %v", untouched, err)
	}
	events, err := fixture.store.TaskEvents(t.Context(), first.ID)
	if err != nil || len(events) != 2 {
		t.Fatalf("retry duplicated claim events: %+v, %v", events, err)
	}
}

func TestTaskAScopedRebalancePreservesUnrelatedAgentClaim(t *testing.T) {
	for _, principal := range []string{"scoped token", "session"} {
		t.Run(principal, func(t *testing.T) {
			fixture := newTaskAFixture(t)
			fixture.mux = http.NewServeMux()
			fixture.mux.Handle("/", fixture.server.Handler())
			private := taskAAPIReady(t, fixture)
			claimed := taskAAPIClaim(t, fixture, "")
			project, err := fixture.store.CreateProject(t.Context(), "allowed", "sage", fixture.now)
			if err != nil {
				t.Fatal(err)
			}
			movable, err := fixture.store.CreateTask(t.Context(), store.CreateTaskParams{
				Title: "allowed", Status: "doing", ProjectID: &project.ID, Now: fixture.now,
			})
			if err != nil {
				t.Fatal(err)
			}
			token := ""
			if principal == "scoped token" {
				plain, prefix, hash, err := auth.NewAPIToken()
				if err != nil {
					t.Fatal(err)
				}
				if _, err := fixture.store.CreateAPIToken(t.Context(), "restricted", prefix, hash, []string{auth.ScopeWrite}, fixture.now, store.TokenOptions{ProjectIDs: []int64{project.ID}}); err != nil {
					t.Fatal(err)
				}
				token = plain
			}
			path := "/api/v1/agent/tasks/" + itoa(private.ID)
			heartbeat := fmt.Sprintf(`{"claim_id":%q}`, claimed.Claim.ID)
			before := fixture.request(t, http.MethodPost, path+"/heartbeat", heartbeat, "", nil)
			if before.Code != http.StatusOK {
				t.Fatalf("initial heartbeat = %d: %s", before.Code, before.Body.String())
			}
			move := fmt.Sprintf(`{"status":"doing","position":%g}`, claimed.Task.Position)
			response := fixture.request(t, http.MethodPost, "/api/v1/tasks/"+itoa(movable.ID)+"/move", move, token, nil)
			if response.Code != http.StatusOK {
				t.Fatalf("rebalance = %d: %s", response.Code, response.Body.String())
			}
			after := fixture.request(t, http.MethodPost, path+"/heartbeat", heartbeat, "", nil)
			if after.Code != http.StatusOK {
				t.Fatalf("unrelated rebalance revoked claim: %d: %s", after.Code, after.Body.String())
			}
			result := fmt.Sprintf(`{"claim_id":%q,"comment":"finished after rebalance"}`, claimed.Claim.ID)
			response = fixture.request(t, http.MethodPost, path+"/result", result, "", nil)
			if response.Code != http.StatusOK {
				t.Fatalf("result after unrelated rebalance = %d: %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestTaskARegisteredRoutesIntegration(t *testing.T) {
	fixture := newTaskAFixture(t)
	fixture.mux = http.NewServeMux()
	fixture.mux.Handle("/", fixture.server.Handler())
	task := taskAAPIReady(t, fixture)
	claimed := taskAAPIClaim(t, fixture, "")
	if claimed.Task == nil || claimed.Task.ID != task.ID || claimed.Claim == nil {
		t.Fatalf("registered claim = %+v", claimed)
	}
	response := fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(task.ID)+"/heartbeat", fmt.Sprintf(`{"claim_id":%q}`, claimed.Claim.ID), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("registered heartbeat = %d: %s", response.Code, response.Body.String())
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(task.ID)+"/result", fmt.Sprintf(`{"claim_id":%q,"comment":"complete"}`, claimed.Claim.ID), "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("registered result = %d: %s", response.Code, response.Body.String())
	}
	if err := fixture.store.DeleteTask(t.Context(), task.ID, fixture.now); err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodGet, "/api/v1/trash?limit=1", "", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"status":"review"`) {
		t.Fatalf("registered trash = %d: %s", response.Code, response.Body.String())
	}
	response = fixture.request(t, http.MethodPost, "/api/v1/tasks/"+itoa(task.ID)+"/restore", "", "", nil)
	if response.Code != http.StatusOK {
		t.Fatalf("registered restore = %d: %s", response.Code, response.Body.String())
	}
	tag, err := fixture.store.CreateTag(t.Context(), "tag", "sage", fixture.now)
	if err != nil {
		t.Fatal(err)
	}
	response = fixture.request(t, http.MethodPatch, "/api/v1/tags/"+itoa(tag.ID), `{"name":"new","color":"plum"}`, "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"name":"new"`) {
		t.Fatalf("registered tag patch = %d: %s", response.Code, response.Body.String())
	}
}

func taskAAgentDatabase(t *testing.T, fixture *taskAFixture) *sql.DB {
	t.Helper()
	database, err := sql.Open("sqlite", filepath.Join(fixture.dataDir, "tack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	return database
}

func TestTaskAAgentClaimIdempotencySurvivesPersistenceFailure(t *testing.T) {
	for _, table := range []string{"idempotency", "agent_claim_replay"} {
		t.Run(table, func(t *testing.T) {
			fixture := newTaskAFixture(t)
			fixture.server.registerAgentRoutes(fixture.mux)
			first := taskAAPIReady(t, fixture)
			second := taskAAPIReady(t, fixture)
			token := fixture.token(t, auth.ScopeWrite)
			database := taskAAgentDatabase(t, fixture)
			if _, err := database.ExecContext(t.Context(), "CREATE TRIGGER fail_claim_cache BEFORE INSERT ON "+table+" BEGIN SELECT RAISE(FAIL, 'injected cache failure'); END"); err != nil {
				t.Fatal(err)
			}
			headers := map[string]string{"Idempotency-Key": "durable-acquire"}
			initial := ""
			for range 2 {
				response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
				if table == "agent_claim_replay" {
					if response.Code != http.StatusInternalServerError {
						t.Fatalf("failed persistence returned %d, want 500", response.Code)
					}
					unchanged, err := fixture.store.TaskByID(t.Context(), first.ID)
					if err != nil || unchanged.Status != "todo" {
						t.Fatalf("failed persistence consumed task: %+v, %v", unchanged, err)
					}
				} else {
					if response.Code != http.StatusOK {
						t.Fatalf("claim = %d: %s", response.Code, response.Body.String())
					}
					if initial != "" && response.Body.String() != initial {
						t.Fatal("generic cache failure caused a second allocation")
					}
					initial = response.Body.String()
				}
			}
			if _, err := database.ExecContext(t.Context(), "DROP TRIGGER fail_claim_cache"); err != nil {
				t.Fatal(err)
			}
			if err := fixture.store.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err := store.Open(t.Context(), fixture.dataDir)
			if err != nil {
				t.Fatal(err)
			}
			fixture.store = reopened
			fixture.server = NewServer(fixture.server.cfg, reopened, nil, BuildInfo{}, fixture.server.logger, func() time.Time { return fixture.now })
			fixture.mux = http.NewServeMux()
			fixture.mux.Handle("/", fixture.server.Handler())
			for attempt := range 2 {
				response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
				if response.Code != http.StatusOK || initial != "" && response.Body.String() != initial {
					t.Fatalf("recovered retry = %d: %s", response.Code, response.Body.String())
				}
				if attempt == 1 && response.Header().Get("X-Tack-Idempotent-Replay") != "true" {
					t.Fatal("missing replay header")
				}
				initial = response.Body.String()
			}
			var result taskAClaimResponse
			if err := json.Unmarshal([]byte(initial), &result); err != nil || result.Task == nil || result.Task.ID != first.ID || result.Claim == nil {
				t.Fatalf("recovery did not claim first task: %+v, %v", result, err)
			}
			untouched, err := fixture.store.TaskByID(t.Context(), second.ID)
			if err != nil || untouched.Status != "todo" {
				t.Fatalf("retry consumed second task: %+v, %v", untouched, err)
			}
			events, err := fixture.store.TaskEvents(t.Context(), first.ID)
			if err != nil || len(events) != 2 {
				t.Fatalf("retry duplicated events: %+v, %v", events, err)
			}
			var sealed []byte
			var key string
			if err := database.QueryRowContext(t.Context(), "SELECT key, body FROM agent_claim_replay").Scan(&key, &sealed); err != nil {
				t.Fatal(err)
			}
			if strings.Contains(key, "durable-acquire") || bytes.Contains(sealed, []byte(result.Claim.ID)) {
				t.Fatal("claim/key persisted in plaintext")
			}
			opened, err := fixture.server.openIdempotentBody(key, sealed)
			if err != nil || !bytes.Contains(opened, []byte(result.Claim.ID)) {
				t.Fatalf("replay not encrypted with the session secret: %v", err)
			}
		})
	}
}

func TestTaskAAgentClaimAPIReplayRechecksLeaseAndAccess(t *testing.T) {
	for _, change := range []string{"expired", "edited", "completed", "project", "tag", "secret", "corrupt"} {
		t.Run(change, func(t *testing.T) {
			fixture := newTaskAFixture(t)
			fixture.server.registerAgentRoutes(fixture.mux)
			project, err := fixture.store.CreateProject(t.Context(), "allowed", "sage", fixture.now)
			if err != nil {
				t.Fatal(err)
			}
			tag, err := fixture.store.CreateTag(t.Context(), "allowed", "sage", fixture.now)
			if err != nil {
				t.Fatal(err)
			}
			first, err := fixture.store.CreateTask(t.Context(), store.CreateTaskParams{
				Title: "claimed", Status: "todo", AgentReady: true, ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: fixture.now,
			})
			if err != nil {
				t.Fatal(err)
			}
			second, err := fixture.store.CreateTask(t.Context(), store.CreateTaskParams{
				Title: "next", Status: "todo", AgentReady: true, ProjectID: &project.ID, TagIDs: []int64{tag.ID}, Now: fixture.now,
			})
			if err != nil {
				t.Fatal(err)
			}
			token, prefix, hash, err := auth.NewAPIToken()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := fixture.store.CreateAPIToken(t.Context(), "scoped", prefix, hash, []string{auth.ScopeWrite}, fixture.now, store.TokenOptions{ProjectIDs: []int64{project.ID}, TagIDs: []int64{tag.ID}}); err != nil {
				t.Fatal(err)
			}
			headers := map[string]string{"Idempotency-Key": "stale-claim"}
			initial := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
			if initial.Code != http.StatusOK {
				t.Fatalf("initial claim = %d: %s", initial.Code, initial.Body.String())
			}
			var result taskAClaimResponse
			if err := json.Unmarshal(initial.Body.Bytes(), &result); err != nil || result.Claim == nil {
				t.Fatalf("initial claim = %+v, %v", result, err)
			}
			want := http.StatusConflict
			switch change {
			case "expired":
				fixture.now = fixture.now.Add(30 * time.Minute)
			case "edited":
				title := "changed instructions"
				_, err = fixture.store.UpdateTask(t.Context(), first.ID, store.UpdateTaskParams{Title: &title}, fixture.now)
			case "completed":
				body := fmt.Sprintf(`{"claim_id":%q,"comment":"finished"}`, result.Claim.ID)
				response := fixture.request(t, http.MethodPost, "/api/v1/agent/tasks/"+itoa(first.ID)+"/result", body, token, nil)
				if response.Code != http.StatusOK {
					t.Fatalf("complete = %d: %s", response.Code, response.Body.String())
				}
			case "project":
				_, err = fixture.store.UpdateTask(t.Context(), first.ID, store.UpdateTaskParams{ProjectSet: true}, fixture.now)
				want = http.StatusNotFound
			case "tag":
				_, err = fixture.store.UpdateTask(t.Context(), first.ID, store.UpdateTaskParams{TagsSet: true}, fixture.now)
				want = http.StatusNotFound
			case "secret":
				fixture.server.cfg.SessionSecret = bytes.Repeat([]byte{4}, 32)
				want = http.StatusInternalServerError
			case "corrupt":
				_, err = taskAAgentDatabase(t, fixture).ExecContext(t.Context(), "UPDATE agent_claim_replay SET body = 'corrupt'")
				want = http.StatusInternalServerError
			}
			if err != nil {
				t.Fatal(err)
			}
			for range 2 {
				response := fixture.request(t, http.MethodPost, "/api/v1/agent/claim", `{"worker":"bridge"}`, token, headers)
				if response.Code != want || strings.Contains(response.Body.String(), result.Claim.ID) {
					t.Fatalf("unsafe replay = %d: %s, want %d", response.Code, response.Body.String(), want)
				}
			}
			untouched, err := fixture.store.TaskByID(t.Context(), second.ID)
			if err != nil || untouched.Status != "todo" {
				t.Fatalf("unsafe replay consumed next task: %+v, %v", untouched, err)
			}
		})
	}
}

func TestTaskAAgentClaimAPIConcurrentReplayAcrossServers(t *testing.T) {
	fixture := newTaskAFixture(t)
	first := taskAAPIReady(t, fixture)
	second := taskAAPIReady(t, fixture)
	third := taskAAPIReady(t, fixture)
	tokens := []string{fixture.token(t, auth.ScopeWrite), fixture.token(t, auth.ScopeWrite)}
	peer, err := store.Open(t.Context(), fixture.dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = peer.Close() })
	peerServer := NewServer(fixture.server.cfg, peer, nil, BuildInfo{}, fixture.server.logger, func() time.Time { return fixture.now })
	primary := httptest.NewServer(fixture.server.Handler())
	t.Cleanup(primary.Close)
	secondary := httptest.NewServer(peerServer.Handler())
	t.Cleanup(secondary.Close)
	type outcome struct {
		principal int
		status    int
		body      []byte
		replayed  bool
		err       error
	}
	results := make(chan outcome, 12)
	start := make(chan struct{})
	for attempt := range 12 {
		go func() {
			<-start
			principal := attempt % 2
			server := primary
			if attempt%3 == 0 {
				server = secondary
			}
			request, err := http.NewRequestWithContext(t.Context(), http.MethodPost, server.URL+"/api/v1/agent/claim", strings.NewReader(`{"worker":"bridge"}`))
			if err != nil {
				results <- outcome{err: err}
				return
			}
			request.Header.Set("Authorization", "Bearer "+tokens[principal])
			request.Header.Set("Idempotency-Key", "same-key")
			request.Header.Set("Content-Type", "application/json")
			response, err := server.Client().Do(request)
			if err != nil {
				results <- outcome{err: err}
				return
			}
			body, err := io.ReadAll(response.Body)
			_ = response.Body.Close()
			results <- outcome{principal, response.StatusCode, body, response.Header.Get("X-Tack-Idempotent-Replay") == "true", err}
		}()
	}
	close(start)
	var originals [2][]byte
	var taskIDs [2]int64
	replayed := 0
	for range 12 {
		result := <-results
		if result.err != nil || result.status != http.StatusOK {
			t.Errorf("concurrent response = %d: %s, %v", result.status, result.body, result.err)
			continue
		}
		if originals[result.principal] == nil {
			originals[result.principal] = result.body
		}
		if !bytes.Equal(originals[result.principal], result.body) {
			t.Errorf("same principal received different claims")
		}
		var claim taskAClaimResponse
		if err := json.Unmarshal(result.body, &claim); err != nil || claim.Task == nil || claim.Claim == nil {
			t.Errorf("concurrent claim = %+v, %v", claim, err)
			continue
		}
		taskIDs[result.principal] = claim.Task.ID
		if result.replayed {
			replayed++
		}
	}
	if replayed != 10 || taskIDs[0] == taskIDs[1] {
		t.Fatalf("replays=%d tasks=%v; want 10 replays and isolated principals", replayed, taskIDs)
	}
	for _, task := range []store.Task{first, second} {
		events, err := fixture.store.TaskEvents(t.Context(), task.ID)
		if err != nil || len(events) != 2 {
			t.Fatalf("duplicate claim events: %+v, %v", events, err)
		}
	}
	untouched, err := fixture.store.TaskByID(t.Context(), third.ID)
	if err != nil || untouched.Status != "todo" {
		t.Fatalf("retry consumed third task: %+v, %v", untouched, err)
	}
}
