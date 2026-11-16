package bridge

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

type fixture struct {
	testing                                             *testing.T
	mu                                                  sync.Mutex
	config                                              Config
	tack, codeg                                         *httptest.Server
	empty, unmarked, loseCreate, omitCreate, loseResult bool
	duplicateMarker, loseStart, omitStart               bool
	heartbeatStatus                                     int
	status, afterStart, verdict, reason                 string
	taskTitle                                           string
	draft                                               map[string]json.RawMessage
	codegConfig                                         json.RawMessage
	creates, starts, claims, heartbeats, results        int
	resultBodies                                        [][]byte
	resultKeys                                          []string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	fixture := &fixture{testing: t, status: "todo", afterStart: "review", verdict: "success", taskTitle: "Fix $(touch forbidden); `echo unsafe`"}
	fixture.tack = httptest.NewServer(http.HandlerFunc(fixture.serveTack))
	fixture.codeg = httptest.NewServer(http.HandlerFunc(fixture.serveCodeg))
	t.Cleanup(fixture.tack.Close)
	t.Cleanup(fixture.codeg.Close)
	values := testEnvironment(t)
	values["TACK_URL"], values["CODEG_URL"] = fixture.tack.URL, fixture.codeg.URL
	var err error
	fixture.config, err = LoadConfig(func(key string) string { return values[key] })
	if err != nil {
		t.Fatal(err)
	}
	return fixture
}

func (fixture *fixture) reply(writer http.ResponseWriter, value any) {
	fixture.testing.Helper()
	writer.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(writer).Encode(value); err != nil {
		fixture.testing.Error(err)
	}
}

func (fixture *fixture) serveTack(writer http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if request.Header.Get("Authorization") != "Bearer tk_test_secret" {
		fixture.testing.Error("missing Tack Bearer")
	}
	if request.Header.Get("Cookie") != "" {
		fixture.testing.Error("bridge used cookies")
	}
	switch request.Method + " " + request.URL.Path {
	case "GET /api/v1/agent/next":
		fixture.reply(writer, map[string]any{"task": nil})
	case "POST /api/v1/agent/claim":
		fixture.claims++
		var body struct {
			Worker string `json:"worker"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.Worker != "bridge-test" {
			fixture.testing.Errorf("claim payload: %+v, %v", body, err)
		}
		if request.Header.Get("Idempotency-Key") == "" {
			fixture.testing.Error("claim lacks persistent replay key")
		}
		if fixture.empty {
			fixture.reply(writer, map[string]any{"task": nil, "claim": nil})
			return
		}
		fixture.reply(writer, map[string]any{
			"task":  map[string]any{"id": 19, "title": fixture.taskTitle, "notes": "Acceptance: tests pass.", "status": "doing", "agent_ready": !fixture.unmarked, "priority": 2, "position": 100, "project_id": nil, "project": nil, "tags": []any{}, "due_at": nil, "completed_at": nil, "created_at": "2026-11-16T00:00:00Z", "updated_at": "2026-11-16T00:00:00Z"},
			"claim": map[string]any{"id": "lease-19", "expires_at": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)},
		})
	case "POST /api/v1/agent/tasks/19/heartbeat":
		fixture.heartbeats++
		var body map[string]string
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body["claim_id"] != "lease-19" {
			fixture.testing.Errorf("heartbeat payload: %v, %v", body, err)
		}
		if fixture.heartbeatStatus != 0 {
			http.Error(writer, "secret tk_test_secret codeg_test_secret", fixture.heartbeatStatus)
			return
		}
		fixture.reply(writer, map[string]any{"claim": map[string]any{"id": "lease-19", "expires_at": time.Now().UTC().Add(30 * time.Minute).Format(time.RFC3339)}})
	case "POST /api/v1/agent/tasks/19/result":
		fixture.results++
		var body json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			fixture.testing.Error(err)
		}
		fixture.resultBodies = append(fixture.resultBodies, body)
		fixture.resultKeys = append(fixture.resultKeys, request.Header.Get("Idempotency-Key"))
		if fixture.loseResult {
			fixture.loseResult = false
			dropConnection(writer)
			return
		}
		fixture.reply(writer, map[string]any{"task": map[string]any{"id": 19, "status": "review"}})
	default:
		fixture.testing.Errorf("unexpected Tack route: %s %s", request.Method, request.URL.Path)
		http.NotFound(writer, request)
	}
}

func (fixture *fixture) workTask() map[string]any {
	return map[string]any{
		"id": 42, "folder_id": 7, "title": "Fix $(touch forbidden); `echo unsafe`", "config": fixture.codegConfig,
		"status": fixture.status, "failure_reason": fixture.reason, "last_error": nil, "run_seq": 1, "sort_order": 0,
		"worktree_folder_id": 8, "worktree_missing": false, "agent_type": "codex", "conversation_id": 51, "connection_id": nil,
		"base_branch": "main", "base_sha": "abc", "work_branch": "task/42", "cleanup_state": nil,
		"verdict": fixture.verdict, "result_summary": "Tests passed", "files_changed": 1, "additions": 2, "deletions": 0,
		"merge_commit": nil, "preflight": nil, "merge_queued": nil, "archived_at": nil, "scheduled_at": nil, "deferred_at": nil,
		"compacting": false, "created_at": "2026-11-16T00:00:00Z", "updated_at": "2026-11-16T00:00:00Z",
		"started_at": nil, "settled_at": nil, "finished_at": nil,
	}
}

func (fixture *fixture) serveCodeg(writer http.ResponseWriter, request *http.Request) {
	fixture.mu.Lock()
	defer fixture.mu.Unlock()
	if request.Method != "POST" || request.Header.Get("Authorization") != "Bearer codeg_test_secret" {
		fixture.testing.Error("Codeg requires POST and Bearer")
	}
	var body map[string]json.RawMessage
	if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
		fixture.testing.Error(err)
	}
	switch request.URL.Path {
	case "/api/health":
		fixture.reply(writer, map[string]any{"status": "ok", "version": "0.42.3"})
	case "/api/list_folders":
		fixture.reply(writer, []any{map[string]any{"name": "project", "path": "/mock/project", "agent_types": []string{"codex"}, "conversation_count": 1}})
	case "/api/get_folder":
		if string(body["folderId"]) != "7" {
			fixture.testing.Errorf("get_folder requires folderId: %s", body)
		}
		fixture.reply(writer, map[string]any{"id": 7, "name": "project", "path": "/mock/project", "parent_id": nil, "git_branch": "main", "default_agent_type": "codex", "last_opened_at": "2026-11-16T00:00:00Z", "sort_order": 0, "color": "inherit", "kind": "regular", "alias": nil, "group_id": nil})
	case "/api/work_task_list":
		if string(body["folderId"]) != "7" {
			fixture.testing.Errorf("list requires camelCase folderId: %s", body)
		}
		items := []any{}
		if len(fixture.codegConfig) > 0 {
			items = append(items, fixture.workTask())
			if fixture.duplicateMarker {
				items = append(items, fixture.workTask())
			}
		}
		fixture.reply(writer, items)
	case "/api/work_task_create":
		fixture.creates++
		if request.Header.Get("Idempotency-Key") != "" {
			fixture.testing.Error("Codeg create has no idempotency contract")
		}
		persisted, err := os.ReadFile(fixture.config.StatePath)
		var saved struct {
			Active *struct {
				Phase string `json:"phase"`
				Claim struct {
					ID string `json:"id"`
				} `json:"claim"`
			} `json:"active"`
		}
		if err != nil || json.Unmarshal(persisted, &saved) != nil || saved.Active == nil || saved.Active.Phase != "creating" || saved.Active.Claim.ID == "" {
			fixture.testing.Errorf("create intent not durable before request: %s, %v", persisted, err)
		}
		if err := json.Unmarshal(body["draft"], &fixture.draft); err != nil {
			fixture.testing.Error(err)
		}
		if string(fixture.draft["folder_id"]) != "7" {
			fixture.testing.Errorf("draft requires snake_case folder_id: %s", body)
		}
		if !fixture.omitCreate {
			fixture.codegConfig = fixture.draft["config"]
		}
		if fixture.loseCreate {
			fixture.loseCreate = false
			dropConnection(writer)
			return
		}
		fixture.reply(writer, fixture.workTask())
	case "/api/work_task_get", "/api/work_task_start":
		if string(body["id"]) != "42" {
			fixture.testing.Errorf("requires numeric id: %s", body)
		}
		if request.URL.Path == "/api/work_task_start" {
			fixture.starts++
			if !fixture.omitStart {
				fixture.status = fixture.afterStart
			}
			if fixture.loseStart {
				fixture.loseStart = false
				dropConnection(writer)
				return
			}
			fixture.reply(writer, nil)
		} else {
			fixture.reply(writer, fixture.workTask())
		}
	default:
		fixture.testing.Errorf("unexpected Codeg route: %s", request.URL.Path)
		http.NotFound(writer, request)
	}
}

func dropConnection(writer http.ResponseWriter) {
	connection, _, err := writer.(http.Hijacker).Hijack()
	if err == nil {
		_ = connection.Close()
	}
}

func openFixture(t *testing.T, fixture *fixture) *Bridge {
	t.Helper()
	worker, err := Open(fixture.config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Close() })
	return worker
}

func step(t *testing.T, worker *Bridge) {
	t.Helper()
	if err := worker.Step(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestEmptyClaimDoesNotCreate(t *testing.T) {
	fixture := newFixture(t)
	fixture.empty = true
	step(t, openFixture(t, fixture))
	if fixture.creates != 0 || fixture.results != 0 {
		t.Fatal("empty queue dispatched work")
	}
}

func TestCheckUsesReadOnlyRealContracts(t *testing.T) {
	fixture := newFixture(t)
	if err := Check(context.Background(), fixture.config); err != nil {
		t.Fatal(err)
	}
	if fixture.claims != 0 || fixture.creates != 0 {
		t.Fatal("check dispatched work")
	}
	if _, err := os.Stat(fixture.config.StatePath); !os.IsNotExist(err) {
		t.Fatalf("check mutated state: %v", err)
	}
}

func TestHappyCompletionPersistsMappingAndReportsReview(t *testing.T) {
	fixture := newFixture(t)
	worker := openFixture(t, fixture)
	step(t, worker)
	if fixture.results == 0 {
		step(t, worker)
	}
	if fixture.creates != 1 || fixture.starts != 1 || fixture.results != 1 {
		t.Fatalf("counts: create %d start %d result %d", fixture.creates, fixture.starts, fixture.results)
	}
	var result struct {
		ClaimID, Status, Comment string
		Links                    []struct{ Kind, ExternalID, URL string }
	}
	var wire map[string]json.RawMessage
	if err := json.Unmarshal(fixture.resultBodies[0], &wire); err != nil {
		t.Fatal(err)
	}
	if string(wire["claim_id"]) != `"lease-19"` || string(wire["status"]) != `"review"` {
		t.Fatalf("result: %s", fixture.resultBodies[0])
	}
	if err := json.Unmarshal(fixture.resultBodies[0], &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Links) != 1 || result.Links[0].Kind != "codeg_work_task" || !strings.Contains(result.Links[0].URL, "/workspace?") {
		t.Fatalf("missing real Codeg session link: %+v", result)
	}
	if !strings.Contains(string(wire["links"]), `"external_id":"42"`) {
		t.Fatal("missing external ID")
	}
	data, err := os.ReadFile(fixture.config.StatePath)
	if err != nil || !strings.Contains(string(data), "42") {
		t.Fatalf("mapping not saved: %s, %v", data, err)
	}
	info, err := os.Stat(fixture.config.StatePath)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatalf("state not private: %v", err)
	}
	if strings.Contains(string(data), "test_secret") {
		t.Fatal("credentials persisted")
	}
	if !strings.Contains(string(fixture.draft["title"]), "$(touch forbidden)") || !strings.Contains(string(fixture.codegConfig), "Acceptance: tests pass.") {
		t.Fatal("task text lost or interpreted")
	}
}

func TestRestartResumesWithoutDuplicateCreateOrStart(t *testing.T) {
	fixture := newFixture(t)
	fixture.afterStart = "running"
	worker := openFixture(t, fixture)
	step(t, worker)
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.status = "review"
	step(t, openFixture(t, fixture))
	if fixture.creates != 1 || fixture.starts != 1 || fixture.claims != 1 || fixture.results != 1 {
		t.Fatalf("restart duplicated side effects: %+v", fixture.resultKeys)
	}
}

func TestCompletedTaskRequeueNeverReusesOldResult(t *testing.T) {
	fixture := newFixture(t)
	worker := openFixture(t, fixture)
	step(t, worker)
	step(t, worker)
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	fixture.taskTitle = "New requirements after manual requeue"
	worker = openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "completed mapping") {
		t.Fatalf("completed ID was reused: %v", err)
	}
	if fixture.creates != 1 || fixture.starts != 1 || fixture.results != 1 {
		t.Fatalf("requeued task dispatched or received old result: creates=%d starts=%d results=%d", fixture.creates, fixture.starts, fixture.results)
	}
	if worker.state.Active != nil || worker.state.ClaimKey == "" || worker.state.Completed["19"] != 42 {
		t.Fatal("ambiguous requeue discarded mapping or request identity")
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	worker = openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil || fixture.results != 1 {
		t.Fatalf("restart bypassed completed mapping protection: %v", err)
	}
}

func TestRolledBackClaimIntentDoesNotAdoptUntrackedCodegResult(t *testing.T) {
	fixture := newFixture(t)
	worker := openFixture(t, fixture)
	if err := worker.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	snapshot, err := os.ReadFile(fixture.config.StatePath)
	if err != nil {
		t.Fatal(err)
	}
	step(t, worker)
	step(t, worker)
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(fixture.config.StatePath, snapshot, 0600); err != nil {
		t.Fatal(err)
	}
	worker = openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "without a persisted create intent") {
		t.Fatalf("rolled-back state adopted old result: %v", err)
	}
	if fixture.creates != 1 || fixture.starts != 1 || fixture.results != 1 {
		t.Fatalf("rollback repeated side effects: creates=%d starts=%d results=%d", fixture.creates, fixture.starts, fixture.results)
	}
}

func TestLegacyActiveMappingCannotReuseCompletedResult(t *testing.T) {
	fixture := newFixture(t)
	worker := openFixture(t, fixture)
	step(t, worker)
	active := *worker.state.Active
	step(t, worker)
	worker.state.Active = &active
	if err := worker.save(); err != nil {
		t.Fatal(err)
	}
	if err := worker.Close(); err != nil {
		t.Fatal(err)
	}
	if reopened, err := Open(fixture.config); err == nil {
		_ = reopened.Close()
		t.Fatal("legacy state reused completed task as active mapping")
	}
}

func TestAmbiguousCreateReconcilesByMarkerAfterRestart(t *testing.T) {
	fixture := newFixture(t)
	fixture.loseCreate = true
	worker := openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil {
		t.Fatal("lost create response must be reported")
	}
	_ = worker.Close()
	worker = openFixture(t, fixture)
	step(t, worker)
	if fixture.results == 0 {
		step(t, worker)
	}
	if fixture.creates != 1 || fixture.results != 1 {
		t.Fatalf("reconciliation duplicated/lost work: %d, %d", fixture.creates, fixture.results)
	}
}

func TestAmbiguousCreateWithoutMatchNeverResends(t *testing.T) {
	fixture := newFixture(t)
	fixture.loseCreate, fixture.omitCreate = true, true
	worker := openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil {
		t.Fatal("expected uncertain create")
	}
	_ = worker.Close()
	worker = openFixture(t, fixture)
	for attempt := 0; attempt < 2; attempt++ {
		if err := worker.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "ambiguous") {
			t.Fatalf("missing ambiguity diagnostic: %v", err)
		}
	}
	if fixture.creates != 1 || fixture.results != 0 {
		t.Fatal("ambiguous create was resent or finalized")
	}
}

func TestHeartbeatFailureStopsWithoutStaleResult(t *testing.T) {
	for _, status := range []int{409, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			fixture := newFixture(t)
			fixture.afterStart = "running"
			worker := openFixture(t, fixture)
			step(t, worker)
			fixture.status, fixture.heartbeatStatus = "review", status
			err := worker.Step(context.Background())
			if err == nil || strings.Contains(err.Error(), "test_secret") || fixture.results != 0 {
				t.Fatalf("stale result or leaked secret: %v", err)
			}
		})
	}
}

func TestTerminalOutcomes(t *testing.T) {
	for _, test := range []struct{ status, verdict, setting, expected string }{
		{"failed", "blocked", "done", "review"}, {"canceled", "", "done", "review"},
		{"review", "needs_review", "review", "review"}, {"done", "success", "review", "review"},
		{"done", "success", "done", "done"}, {"review", "success", "done", "done"},
		{"review", "blocked", "done", "review"},
	} {
		t.Run(test.status+test.verdict+test.setting, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.afterStart, fixture.verdict, fixture.reason = test.status, test.verdict, "agent_error"
			fixture.config.ResultStatus = test.setting
			worker := openFixture(t, fixture)
			step(t, worker)
			if fixture.results == 0 {
				step(t, worker)
			}
			var result map[string]json.RawMessage
			if len(fixture.resultBodies) != 1 {
				t.Fatalf("missing terminal result: %d", fixture.results)
			}
			if err := json.Unmarshal(fixture.resultBodies[0], &result); err != nil {
				t.Fatal(err)
			}
			if string(result["status"]) != `"`+test.expected+`"` || !strings.Contains(string(result["comment"]), test.status) {
				t.Fatalf("terminal result: %s", fixture.resultBodies[0])
			}
			if test.status == "failed" && !strings.Contains(string(result["comment"]), "agent_error") {
				t.Fatal("failure reason missing")
			}
		})
	}
}

func TestLostResultResponseReplaysIdenticalPersistedResult(t *testing.T) {
	fixture := newFixture(t)
	fixture.loseResult = true
	worker := openFixture(t, fixture)
	err := worker.Step(context.Background())
	if err == nil {
		err = worker.Step(context.Background())
	}
	if err == nil {
		t.Fatal("lost result response was hidden")
	}
	_ = worker.Close()
	fixture.heartbeatStatus = 409
	step(t, openFixture(t, fixture))
	if fixture.results != 2 || fixture.resultKeys[0] == "" || fixture.resultKeys[0] != fixture.resultKeys[1] || !reflect.DeepEqual(fixture.resultBodies[0], fixture.resultBodies[1]) {
		t.Fatal("result replay was not identical/idempotent")
	}
	if fixture.creates != 1 {
		t.Fatal("result retry dispatched again")
	}
}

func TestRejectsUnmarkedTask(t *testing.T) {
	fixture := newFixture(t)
	fixture.unmarked = true
	if err := openFixture(t, fixture).Step(context.Background()); err == nil || fixture.creates != 0 {
		t.Fatalf("unmarked task dispatched: %v", err)
	}
}

func TestCanceledLoopExits(t *testing.T) {
	fixture := newFixture(t)
	fixture.empty = true
	worker := openFixture(t, fixture)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if err := worker.Run(ctx, false); err != nil && err != context.DeadlineExceeded {
		t.Fatal(err)
	}
	if time.Since(started) > time.Second || fixture.creates != 0 {
		t.Fatal("loop ignored cancellation")
	}
}

func TestStateLockAndDestinationBinding(t *testing.T) {
	fixture := newFixture(t)
	fixture.empty = true
	worker := openFixture(t, fixture)
	step(t, worker)
	if duplicate, err := Open(fixture.config); err == nil {
		_ = duplicate.Close()
		t.Fatal("same state allowed concurrent workers")
	}
	_ = worker.Close()
	fixture.config.FolderID = 9
	if duplicate, err := Open(fixture.config); err == nil {
		_ = duplicate.Close()
		t.Fatal("state silently rebound to different Codeg folder")
	}
}

func TestCorruptStateFailsClosed(t *testing.T) {
	fixture := newFixture(t)
	if err := os.WriteFile(fixture.config.StatePath, []byte(`{"version":1`), 0600); err != nil {
		t.Fatal(err)
	}
	if worker, err := Open(fixture.config); err == nil {
		_ = worker.Close()
		t.Fatal("corrupt mapping ignored")
	}
	if fixture.claims != 0 {
		t.Fatal("corrupt state claimed work")
	}
}

func TestStateSymlinkRejected(t *testing.T) {
	fixture := newFixture(t)
	target := filepath.Join(t.TempDir(), "unrelated")
	if err := os.WriteFile(target, []byte("private"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, fixture.config.StatePath); err != nil {
		t.Fatal(err)
	}
	if worker, err := Open(fixture.config); err == nil {
		_ = worker.Close()
		t.Fatal("state symlink accepted")
	}
}

func TestAmbiguousStartIsObservedWithoutResending(t *testing.T) {
	for _, omitted := range []bool{false, true} {
		t.Run(fmt.Sprint(omitted), func(t *testing.T) {
			fixture := newFixture(t)
			fixture.loseStart, fixture.omitStart = true, omitted
			worker := openFixture(t, fixture)
			if err := worker.Step(context.Background()); err == nil {
				t.Fatal("missing start uncertainty")
			}
			_ = worker.Close()
			worker = openFixture(t, fixture)
			err := worker.Step(context.Background())
			if omitted && err == nil {
				t.Fatal("unresolved start should require reconciliation")
			}
			if !omitted && err != nil {
				t.Fatal(err)
			}
			if fixture.starts != 1 || fixture.creates != 1 {
				t.Fatal("ambiguous start retriggered work")
			}
		})
	}
}

func TestAmbiguousDuplicateMarkerFailsClosed(t *testing.T) {
	fixture := newFixture(t)
	fixture.loseCreate = true
	worker := openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil {
		t.Fatal("expected ambiguous create")
	}
	fixture.duplicateMarker = true
	if err := worker.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "multiple") {
		t.Fatalf("duplicate mapping not rejected: %v", err)
	}
	if fixture.creates != 1 || fixture.starts != 0 || fixture.results != 0 {
		t.Fatal("duplicate marker dispatched work")
	}
}

func TestLiveStatusesKeepRenewingWithoutResult(t *testing.T) {
	for _, status := range []string{"queued", "preparing", "running", "awaiting_input", "merging"} {
		t.Run(status, func(t *testing.T) {
			fixture := newFixture(t)
			fixture.afterStart = status
			worker := openFixture(t, fixture)
			step(t, worker)
			renewals := fixture.heartbeats
			step(t, worker)
			if fixture.heartbeats <= renewals || fixture.results != 0 || fixture.starts != 1 {
				t.Fatal("live status stopped renewal or submitted a result")
			}
		})
	}
}

func TestExpiredClaimNeverDispatchesOrReports(t *testing.T) {
	fixture := newFixture(t)
	fixture.afterStart = "running"
	worker := openFixture(t, fixture)
	step(t, worker)
	worker.state.Active.Claim.ExpiresAt = time.Now().Add(-time.Minute)
	if err := worker.save(); err != nil {
		t.Fatal(err)
	}
	_ = worker.Close()
	fixture.status = "review"
	worker = openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil || !strings.Contains(err.Error(), "expired") {
		t.Fatalf("expired claim accepted: %v", err)
	}
	if fixture.results != 0 || fixture.creates != 1 {
		t.Fatal("stale worker caused side effects")
	}
}

func TestTerminalResultRedactsConfiguredCredentials(t *testing.T) {
	fixture := newFixture(t)
	fixture.afterStart, fixture.reason = "failed", "agent_error tk_test_secret codeg_test_secret"
	worker := openFixture(t, fixture)
	step(t, worker)
	step(t, worker)
	if strings.Contains(string(fixture.resultBodies[0]), "test_secret") {
		t.Fatal("failure details leaked configured credentials")
	}
}
