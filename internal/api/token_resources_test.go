package api

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
	"github.com/today/tack/migrations"
)

func resourceTestToken(t *testing.T, ts *httptest.Server, client *http.Client, projects, tags []int64) string {
	t.Helper()
	resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "resource test", "scopes": []string{"read", "write", "capture"},
		"project_ids": projects, "tag_ids": tags, "password": testPassword,
	})
	if resp.StatusCode != http.StatusCreated {
		defer closeBody(t, resp)
		t.Fatalf("create restricted token status = %d", resp.StatusCode)
	}
	var result struct {
		Token string `json:"token"`
	}
	decodeResponse(t, resp, &result)
	return result.Token
}

func TestTokenResourceIsolation(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	closeBody(t, login(t, client, ts.URL, testPassword, ""))
	var projects [2]projectDTO
	var tags [2]tagDTO
	for index, name := range []string{"allowed", "private"} {
		decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/projects", map[string]any{"name": name}), &projects[index])
		decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tags", map[string]any{"name": name}), &tags[index])
	}
	var tasks [3]taskDTO
	for index, membership := range [][2]int{{0, 0}, {1, 0}, {0, 1}} {
		decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks", map[string]any{
			"title": "task", "project_id": projects[membership[0]].ID, "tag_ids": []int64{tags[membership[1]].ID},
		}), &tasks[index])
	}
	token := resourceTestToken(t, ts, client, []int64{projects[0].ID}, []int64{tags[0].ID})
	var listed struct {
		Tasks []taskDTO `json:"tasks"`
	}
	decodeResponse(t, bearerJSON(t, ts, token, http.MethodGet, "/api/v1/tasks?limit=10", nil), &listed)
	if len(listed.Tasks) != 1 || listed.Tasks[0].ID != tasks[0].ID {
		t.Fatalf("scoped tasks = %+v", listed.Tasks)
	}
	for _, suffix := range []string{"", "/events"} {
		resp := bearerJSON(t, ts, token, http.MethodGet, "/api/v1/tasks/"+itoa(tasks[1].ID)+suffix, nil)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("hidden detail %s status = %d", suffix, resp.StatusCode)
		}
		closeBody(t, resp)
	}
	for _, mutation := range []struct {
		method string
		suffix string
		body   any
	}{
		{http.MethodPatch, "", map[string]any{"title": "leak"}},
		{http.MethodPost, "/move", map[string]any{"status": "done"}},
		{http.MethodPost, "/comments", map[string]any{"text": "leak"}},
		{http.MethodDelete, "", nil},
	} {
		resp := bearerJSON(t, ts, token, mutation.method, "/api/v1/tasks/"+itoa(tasks[1].ID)+mutation.suffix, mutation.body)
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("hidden mutation %s %s = %d", mutation.method, mutation.suffix, resp.StatusCode)
		}
		closeBody(t, resp)
	}
	for _, body := range []map[string]any{
		{"project_id": projects[1].ID}, {"project_id": nil}, {"tag_ids": []int64{tags[1].ID}},
		{"tags": []string{"new-forbidden-tag"}},
	} {
		resp := bearerJSON(t, ts, token, http.MethodPatch, "/api/v1/tasks/"+itoa(tasks[0].ID), body)
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("out-of-scope patch %+v = %d", body, resp.StatusCode)
		}
		closeBody(t, resp)
	}
	for _, text := range []string{"missing memberships", "bad @private #allowed", "bad @allowed #new-forbidden-tag"} {
		resp := bearerJSON(t, ts, token, http.MethodPost, "/api/v1/capture", map[string]any{"text": text})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("forbidden capture %q = %d", text, resp.StatusCode)
		}
		closeBody(t, resp)
	}
	resp := bearerJSON(t, ts, token, http.MethodPost, "/api/v1/capture", map[string]any{"text": "allowed capture @allowed #allowed"})
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("allowed capture = %d", resp.StatusCode)
	}
	closeBody(t, resp)
	var exported exportDocument
	decodeResponse(t, bearerJSON(t, ts, token, http.MethodGet, "/api/v1/export", nil), &exported)
	if len(exported.Tasks) != 2 || len(exported.Projects) != 1 || len(exported.Tags) != 1 || exported.Projects[0].TaskCount != 2 || exported.Tags[0].TaskCount != 2 {
		t.Fatalf("export leaks hidden resources/counts: %+v", exported)
	}
	var metadata struct {
		Projects []projectDTO `json:"projects"`
		Tags     []tagDTO     `json:"tags"`
	}
	decodeResponse(t, bearerJSON(t, ts, token, http.MethodGet, "/api/v1/projects", nil), &metadata)
	decodeResponse(t, bearerJSON(t, ts, token, http.MethodGet, "/api/v1/tags", nil), &metadata)
	if len(metadata.Projects) != 1 || len(metadata.Tags) != 1 || metadata.Projects[0].TaskCount != 2 || metadata.Tags[0].TaskCount != 2 {
		t.Fatalf("metadata scope/counts = %+v", metadata)
	}
	var allTags struct {
		Tags []tagDTO `json:"tags"`
	}
	decodeResponse(t, webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tags", nil), &allTags)
	if len(allTags.Tags) != 2 {
		t.Fatalf("rejected mutations created tags: %+v", allTags.Tags)
	}
	for _, resource := range []string{"projects", "tags"} {
		resp := bearerJSON(t, ts, token, http.MethodPost, "/api/v1/"+resource, map[string]any{"name": "forbidden"})
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("resource-limited metadata write %s = %d", resource, resp.StatusCode)
		}
		closeBody(t, resp)
	}
}

func TestIdempotencyIsBoundToPrincipalAndRoute(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	closeBody(t, login(t, client, ts.URL, testPassword, ""))
	firstToken := resourceTestToken(t, ts, client, nil, nil)
	secondToken := resourceTestToken(t, ts, client, nil, nil)
	var first, second taskDTO
	for index, token := range []string{firstToken, secondToken} {
		resp := webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks", map[string]any{"title": "unique"}, map[string]string{
			"Authorization": "Bearer " + token, "Idempotency-Key": "shared-key",
		})
		if index == 0 {
			decodeResponse(t, resp, &first)
		} else {
			decodeResponse(t, resp, &second)
		}
	}
	if first.ID == second.ID {
		t.Fatal("idempotency cache crossed token principals")
	}
	resp := webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks/"+itoa(first.ID)+"/comments", map[string]any{"text": "comment"}, map[string]string{
		"Authorization": "Bearer " + firstToken, "Idempotency-Key": "shared-key",
	})
	var event eventDTO
	decodeResponse(t, resp, &event)
	if event.Kind != "commented" {
		t.Fatalf("idempotency cache crossed routes: %+v", event)
	}
}

func TestIdempotentReplayRechecksTaskResourceAccess(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	closeBody(t, login(t, client, ts.URL, testPassword, ""))
	var project projectDTO
	decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/projects", map[string]string{"name": "allowed"}), &project)
	token := resourceTestToken(t, ts, client, []int64{project.ID}, nil)
	headers := map[string]string{"Authorization": "Bearer " + token, "Idempotency-Key": "create-key"}
	body := map[string]any{"title": "initially allowed", "project_id": project.ID}
	var task taskDTO
	decodeResponse(t, webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks", body, headers), &task)
	resp := webJSON(t, client, http.MethodPatch, ts.URL+"/api/v1/tasks/"+itoa(task.ID), map[string]any{"project_id": nil})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("move task out of token scope = %d", resp.StatusCode)
	}
	closeBody(t, resp)
	resp = webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks", body, headers)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("replay exposes task moved out of scope: %d", resp.StatusCode)
	}
	closeBody(t, resp)
}

func TestTokenResourcesValidateIDs(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	closeBody(t, login(t, client, ts.URL, testPassword, ""))
	for _, field := range []string{"project_ids", "tag_ids"} {
		resp := webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
			"name": "invalid", "scopes": []string{"read"}, "password": testPassword, field: []int64{999},
		})
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Errorf("invalid %s status = %d", field, resp.StatusCode)
		}
		closeBody(t, resp)
	}
}

func TestTokenResourceRejectsCaseFoldedTagEscape(t *testing.T) {
	for _, operation := range []string{"create", "patch", "capture"} {
		for _, order := range []string{"private-first", "allowed-first", "missing-tag"} {
			t.Run(operation+"/"+order, func(t *testing.T) {
				ts := newTestServer(t)
				defer ts.Close()
				client := newClient(t)
				closeBody(t, login(t, client, ts.URL, testPassword, ""))
				var allowed, private tagDTO
				decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tags", map[string]string{"name": "review-tag"}), &allowed)
				decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tags", map[string]string{"name": "REVIEW-TAG"}), &private)
				token := resourceTestToken(t, ts, client, nil, []int64{allowed.ID})
				var original taskDTO
				decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tasks", map[string]any{"title": "original", "tag_ids": []int64{allowed.ID}}), &original)
				names := []string{"REVIEW-TAG", "review-tag"}
				wantStatus := http.StatusForbidden
				switch order {
				case "allowed-first":
					names = []string{"review-tag", "REVIEW-TAG", "review-tag"}
					wantStatus = http.StatusCreated
					if operation == "patch" {
						wantStatus = http.StatusOK
					}
				case "missing-tag":
					names = []string{"review-tag", "not-yet-created"}
				}
				method, path := http.MethodPost, "/api/v1/tasks"
				body := map[string]any{"title": "changed", "tags": names}
				switch operation {
				case "patch":
					method, path = http.MethodPatch, "/api/v1/tasks/"+itoa(original.ID)
				case "capture":
					path = "/api/v1/capture"
					body = map[string]any{"text": "changed #" + strings.Join(names, " #")}
				}
				response := bearerJSON(t, ts, token, method, path, body)
				if response.StatusCode != wantStatus {
					t.Errorf("%s status = %d, want %d", operation, response.StatusCode, wantStatus)
				}
				if wantStatus < 300 && response.StatusCode < 300 {
					var task taskDTO
					decodeResponse(t, response, &task)
					if len(task.Tags) != 1 || task.Tags[0].ID != allowed.ID {
						t.Fatalf("persisted memberships differ from authorization: %+v", task.Tags)
					}
					read := bearerJSON(t, ts, token, http.MethodGet, "/api/v1/tasks/"+itoa(task.ID), nil)
					if read.StatusCode != http.StatusOK {
						t.Errorf("token cannot read its accepted mutation: %d", read.StatusCode)
					}
					closeBody(t, read)
				} else {
					closeBody(t, response)
				}
				var tags struct {
					Tags []tagDTO `json:"tags"`
				}
				decodeResponse(t, webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tags", nil), &tags)
				if len(tags.Tags) != 2 {
					t.Errorf("restricted mutation created a global tag: %+v", tags.Tags)
				}
				if wantStatus == http.StatusForbidden {
					var tasks struct {
						Tasks []taskDTO `json:"tasks"`
					}
					decodeResponse(t, webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks", nil), &tasks)
					if len(tasks.Tasks) != 1 || tasks.Tasks[0].Title != "original" || len(tasks.Tasks[0].Tags) != 1 || tasks.Tasks[0].Tags[0].ID != allowed.ID {
						t.Errorf("rejected mutation changed task state: %+v", tasks.Tasks)
					}
				}
			})
		}
	}
}

func TestDeletedTagNeverTransfersTokenAccess(t *testing.T) {
	for _, membership := range []string{"single", "first-of-many", "last-of-many"} {
		t.Run(membership, func(t *testing.T) {
			st, err := store.Open(t.Context(), t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = st.Close() })
			if err := st.Migrate(t.Context()); err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC().Truncate(time.Second)
			if _, err := st.BootstrapAdmin(t.Context(), "admin", "unused", now); err != nil {
				t.Fatal(err)
			}
			other, err := st.CreateTag(t.Context(), "unrelated", "sage", now)
			if err != nil {
				t.Fatal(err)
			}
			deleted, err := st.CreateTag(t.Context(), "old-resource", "sage", now)
			if err != nil {
				t.Fatal(err)
			}
			allowedIDs := []int64{deleted.ID}
			switch membership {
			case "first-of-many":
				allowedIDs = []int64{deleted.ID, other.ID}
			case "last-of-many":
				allowedIDs = []int64{other.ID, deleted.ID}
			}
			createToken := func(ids []int64) (string, store.APIToken) {
				t.Helper()
				plain, prefix, hash, err := auth.NewAPIToken()
				if err != nil {
					t.Fatal(err)
				}
				token, err := st.CreateAPIToken(t.Context(), "review", prefix, hash, []string{auth.ScopeRead, auth.ScopeWrite, auth.ScopeCapture}, now, store.TokenResources{TagIDs: ids})
				if err != nil {
					t.Fatal(err)
				}
				return plain, token
			}
			plain, restricted := createToken(allowedIDs)
			_, alreadyRevoked := createToken(allowedIDs)
			_, unrelated := createToken([]int64{other.ID})
			_, unrestricted := createToken(nil)
			priorRevocation := now.Add(-time.Hour)
			if err := st.RevokeAPIToken(t.Context(), alreadyRevoked.ID, priorRevocation); err != nil {
				t.Fatal(err)
			}
			server := NewServer(config.Config{SessionSecret: bytes.Repeat([]byte{8}, 32)}, st, nil, BuildInfo{}, nil, func() time.Time { return now })
			ts := httptest.NewServer(server.Handler())
			defer ts.Close()
			body := map[string]any{"title": "before delete", "tag_ids": []int64{deleted.ID}}
			headers := map[string]string{"Authorization": "Bearer " + plain, "Idempotency-Key": "cached-create"}
			response := webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks", body, headers)
			if response.StatusCode != http.StatusCreated {
				t.Fatalf("initial token request = %d", response.StatusCode)
			}
			closeBody(t, response)
			if err := st.DeleteTag(t.Context(), deleted.ID); err != nil {
				t.Fatal(err)
			}
			updated, err := st.APITokenByID(t.Context(), restricted.ID)
			if err != nil {
				t.Fatal(err)
			}
			if updated.RevokedAt == nil {
				t.Error("deleting a referenced tag did not revoke the token")
			}
			if !slices.Equal(updated.TagIDs, allowedIDs) {
				t.Errorf("revocation changed resource restrictions: %v, want %v", updated.TagIDs, allowedIDs)
			}
			for _, unaffected := range []store.APIToken{alreadyRevoked, unrelated, unrestricted} {
				stored, err := st.APITokenByID(t.Context(), unaffected.ID)
				if err != nil {
					t.Fatal(err)
				}
				if stored.ID == alreadyRevoked.ID {
					if stored.RevokedAt == nil || !stored.RevokedAt.Equal(priorRevocation) {
						t.Error("existing revocation time changed")
					}
				} else if stored.RevokedAt != nil {
					t.Errorf("unrelated token revoked: %d", stored.ID)
				}
			}
			replacement, err := st.CreateTag(t.Context(), "new-private-resource", "sage", now)
			if err != nil {
				t.Fatal(err)
			}
			if replacement.ID != deleted.ID {
				t.Fatalf("test must exercise ID reuse: replacement=%d deleted=%d", replacement.ID, deleted.ID)
			}
			private, err := st.CreateTask(t.Context(), store.CreateTaskParams{Title: "private", Status: "todo", AgentReady: true, TagIDs: []int64{replacement.ID}, Now: now})
			if err != nil {
				t.Fatal(err)
			}
			for _, endpoint := range []struct {
				method, path string
				body         any
			}{
				{http.MethodGet, "/api/v1/tasks", nil},
				{http.MethodGet, "/api/v1/tasks/" + itoa(private.ID), nil},
				{http.MethodGet, "/api/v1/export", nil},
				{http.MethodGet, "/api/v1/tags", nil},
				{http.MethodPatch, "/api/v1/tasks/" + itoa(private.ID), map[string]string{"title": "escaped"}},
				{http.MethodPost, "/api/v1/capture", map[string]string{"text": "escaped #new-private-resource"}},
				{http.MethodPost, "/api/v1/agent/claim", map[string]string{"worker": "old-token"}},
			} {
				response := bearerJSON(t, ts, plain, endpoint.method, endpoint.path, endpoint.body)
				if response.StatusCode != http.StatusUnauthorized {
					t.Errorf("deleted tag transferred privilege to %s %s: %d", endpoint.method, endpoint.path, response.StatusCode)
				}
				closeBody(t, response)
			}
			response = webJSON(t, ts.Client(), http.MethodPost, ts.URL+"/api/v1/tasks", body, headers)
			if response.StatusCode != http.StatusUnauthorized {
				t.Errorf("revoked token replayed cached result: %d", response.StatusCode)
			}
			closeBody(t, response)
		})
	}
}

func TestResourceIdentityMigrationRevokesAtomicallyAndPreservesExistingData(t *testing.T) {
	directory := t.TempDir()
	st, err := store.Open(t.Context(), directory)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	database, err := sql.Open("sqlite", filepath.Join(directory, "tack.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	if _, err := database.ExecContext(t.Context(), `CREATE TABLE schema_migrations(version INTEGER PRIMARY KEY, applied_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	for index, filename := range []string{"0001_init.sql", "0002_m1_core.sql", "0003_token_resources.sql", "0004_agent.sql", "0005_passkeys.sql", "0006_events.sql"} {
		body, err := migrations.FS.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(t.Context(), string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := database.ExecContext(t.Context(), `INSERT INTO schema_migrations(version, applied_at) VALUES (?, ?)`, index+1, time.Now().UTC().Format(time.RFC3339)); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().Truncate(time.Second)
	tag, err := st.CreateTag(t.Context(), "existing", "sage", now)
	if err != nil {
		t.Fatal(err)
	}
	token, err := st.CreateAPIToken(t.Context(), "existing", "prefix", "hash", []string{auth.ScopeRead}, now, store.TokenResources{TagIDs: []int64{tag.ID}})
	if err != nil {
		t.Fatal(err)
	}
	var taskID int64
	if err := database.QueryRowContext(t.Context(), `
		INSERT INTO task(title, status, position, created_at, updated_at)
		VALUES ('existing task', 'inbox', 1024, ?, ?) RETURNING id
	`, now.Format(time.RFC3339), now.Format(time.RFC3339)).Scan(&taskID); err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(t.Context(), "INSERT INTO task_tag(task_id, tag_id) VALUES (?, ?)", taskID, tag.ID); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	stored, err := st.APITokenByID(t.Context(), token.ID)
	if err != nil || stored.RevokedAt != nil || !slices.Equal(stored.TagIDs, []int64{tag.ID}) {
		t.Fatalf("migration changed existing token: %+v, %v", stored, err)
	}
	existing, err := st.TaskByID(t.Context(), taskID)
	if err != nil || existing.Title != "existing task" || len(existing.Tags) != 1 || existing.Tags[0].ID != tag.ID {
		t.Fatalf("migration changed task: %+v, %v", existing, err)
	}
	transaction, err := database.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = transaction.Rollback() }()
	if _, err := transaction.ExecContext(t.Context(), "DELETE FROM tag WHERE id = ?", tag.ID); err != nil {
		t.Fatal(err)
	}
	var revokedAt sql.NullString
	var tagIDs string
	if err := transaction.QueryRowContext(t.Context(), "SELECT revoked_at, tag_ids FROM api_token WHERE id = ?", token.ID).Scan(&revokedAt, &tagIDs); err != nil {
		t.Fatal(err)
	}
	if !revokedAt.Valid {
		t.Error("tag deletion did not revoke token in the same transaction")
	}
	var ids []int64
	if err := json.Unmarshal([]byte(tagIDs), &ids); err != nil || !slices.Equal(ids, []int64{tag.ID}) {
		t.Errorf("trigger changed tag restrictions: %q, %v", tagIDs, err)
	}
	if err := transaction.Rollback(); err != nil {
		t.Fatal(err)
	}
	stored, err = st.APITokenByID(t.Context(), token.ID)
	if err != nil || stored.RevokedAt != nil {
		t.Fatalf("delete rollback retained revocation: %+v, %v", stored, err)
	}
	if _, err := st.TagByID(t.Context(), tag.ID); err != nil {
		t.Fatalf("delete rollback lost tag: %v", err)
	}
	if err := st.DeleteTag(t.Context(), tag.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.APITokenByHash(t.Context(), token.Hash); !errors.Is(err, store.ErrNotFound) {
		t.Errorf("committed deletion retained token authentication: %v", err)
	}
}
