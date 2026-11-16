package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

type codegTask struct {
	ID               int64           `json:"id"`
	FolderID         int64           `json:"folder_id"`
	Config           json.RawMessage `json:"config"`
	Status           string          `json:"status"`
	Verdict          string          `json:"verdict"`
	FailureReason    string          `json:"failure_reason"`
	LastError        string          `json:"last_error"`
	ResultSummary    string          `json:"result_summary"`
	ConversationID   *int64          `json:"conversation_id"`
	WorktreeFolderID *int64          `json:"worktree_folder_id"`
	AgentType        string          `json:"agent_type"`
}

type Bridge struct {
	config      Config
	tack, codeg apiClient
	file        *stateFile
	state       diskState
	failedSave  bool
}

func Open(config Config) (*Bridge, error) {
	if err := config.validate(); err != nil {
		return nil, err
	}
	file, state, err := openState(config)
	if err != nil {
		return nil, err
	}
	tack, codeg := newClients(config)
	return &Bridge{config: config, tack: tack, codeg: codeg, file: file, state: state}, nil
}

func (bridge *Bridge) Close() error { return bridge.file.close() }

func (bridge *Bridge) save() error {
	if err := bridge.file.save(bridge.state); err != nil {
		bridge.failedSave = true
		return err
	}
	return nil
}

func (bridge *Bridge) Run(ctx context.Context, once bool) error {
	for {
		if err := bridge.Step(ctx); err != nil {
			return err
		}
		if once {
			return nil
		}
		timer := time.NewTimer(bridge.config.PollInterval)
		select {
		case <-ctx.Done():
			timer.Stop()
			return ctx.Err()
		case <-timer.C:
		}
	}
}

func (bridge *Bridge) Step(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if bridge.file.lock == nil || bridge.failedSave {
		return errors.New("bridge is closed or persistence failed; restart with the preserved state")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*bridge.config.RequestTimeout)
	defer cancel()
	if bridge.state.Active == nil {
		if err := bridge.acquire(ctx); err != nil {
			return err
		}
		if bridge.state.Active == nil {
			return nil
		}
	}
	active := bridge.state.Active
	if active.Phase == "result_pending" {
		return bridge.submitResult(ctx)
	}
	if err := bridge.heartbeat(ctx); err != nil {
		return err
	}
	if active.CodegID == 0 {
		if err := bridge.ensureCreated(ctx); err != nil {
			return err
		}
	}
	return bridge.monitor(ctx)
}

func (bridge *Bridge) acquire(ctx context.Context) error {
	if bridge.state.ClaimKey == "" {
		key, err := randomKey()
		if err != nil {
			return err
		}
		bridge.state.ClaimKey, bridge.state.ClaimStartedAt = key, time.Now().UTC()
		if err := bridge.save(); err != nil {
			return err
		}
	}
	if time.Since(bridge.state.ClaimStartedAt) >= 30*time.Minute {
		return errors.New("claim outcome is too old to replay safely; reconcile Tack claim before proceeding")
	}
	var wire struct {
		Task  json.RawMessage `json:"task"`
		Claim json.RawMessage `json:"claim"`
	}
	if err := bridge.tack.call(ctx, http.MethodPost, "/api/v1/agent/claim", bridge.state.ClaimKey, map[string]string{"worker": bridge.config.Worker}, &wire); err != nil {
		return err
	}
	var response struct {
		Task  *tackTask
		Claim *claim
	}
	if len(wire.Task) == 0 || len(wire.Claim) == 0 || json.Unmarshal(wire.Task, &response.Task) != nil || json.Unmarshal(wire.Claim, &response.Claim) != nil {
		return errors.New("tack claim response is incomplete or invalid; preserving request identity")
	}
	if response.Task == nil && response.Claim == nil {
		bridge.state.ClaimKey = ""
		bridge.state.ClaimStartedAt = time.Time{}
		return bridge.save()
	}
	if response.Task == nil || response.Claim == nil || response.Task.ID <= 0 || !response.Task.AgentReady || response.Task.Status != "doing" || response.Claim.ID == "" || !response.Claim.ExpiresAt.After(time.Now()) {
		return errors.New("tack claim returned an invalid, unmarked or expired task; refusing dispatch")
	}
	if bridge.state.Completed[strconv.FormatInt(response.Task.ID, 10)] > 0 {
		return fmt.Errorf("tack task %d already has a completed mapping; requeue or rollback requires manual reconciliation, no old result will be reused", response.Task.ID)
	}
	marker := markerFor(bridge.config.TackURL, response.Task.ID)
	bridge.state.Active = &mapping{Task: *response.Task, Claim: *response.Claim, Marker: marker, Phase: "claimed"}
	bridge.state.ClaimKey = ""
	bridge.state.ClaimStartedAt = time.Time{}
	return bridge.save()
}

func (bridge *Bridge) heartbeat(ctx context.Context) error {
	active := bridge.state.Active
	if !active.Claim.ExpiresAt.After(time.Now()) {
		return errors.New("tack claim expired; stopped without further Codeg dispatch or result")
	}
	var response struct {
		Claim claim `json:"claim"`
	}
	if err := bridge.tack.call(ctx, http.MethodPost, bridge.taskRoute("heartbeat"), "", map[string]string{"claim_id": active.Claim.ID}, &response); err != nil {
		return fmt.Errorf("heartbeat failed; stopped without stale result: %w", err)
	}
	if response.Claim.ID != active.Claim.ID || !response.Claim.ExpiresAt.After(time.Now()) {
		return errors.New("heartbeat returned invalid or expired claim; stopped without stale result")
	}
	active.Claim = response.Claim
	return bridge.save()
}

func (bridge *Bridge) taskRoute(action string) string {
	return "/api/v1/agent/tasks/" + strconv.FormatInt(bridge.state.Active.Task.ID, 10) + "/" + action
}

func (bridge *Bridge) ensureCreated(ctx context.Context) error {
	active := bridge.state.Active
	var tasks []codegTask
	if err := bridge.codeg.call(ctx, http.MethodPost, "/api/work_task_list", "", map[string]int64{"folderId": bridge.config.FolderID}, &tasks); err != nil {
		return err
	}
	matches := []codegTask{}
	for _, task := range tasks {
		if bridge.matches(task) {
			matches = append(matches, task)
		}
	}
	if len(matches) > 1 {
		return errors.New("ambiguous Codeg marker: multiple matches; reconcile manually, no dispatch")
	}
	if len(matches) == 1 {
		if active.Phase != "creating" {
			return errors.New("codeg marker exists without a persisted create intent; reconcile rollback or prior execution manually, no old result will be reused")
		}
		active.CodegID, active.Phase = matches[0].ID, "created"
		return bridge.save()
	}
	if active.Phase == "creating" {
		return errors.New("ambiguous Codeg create: no marker match yet; never automatically resend; preserve state and reconcile")
	}
	if err := bridge.heartbeat(ctx); err != nil {
		return err
	}
	active.Phase = "creating"
	if err := bridge.save(); err != nil {
		return err
	}
	prompt := active.Task.Title + "\n\n" + active.Task.Notes
	draft := map[string]any{
		"folder_id": bridge.config.FolderID, "title": active.Task.Title,
		"config": map[string]any{
			"display_text": prompt, "prompt_blocks": []any{map[string]string{"type": "text", "text": prompt}},
			"tack_bridge_marker": active.Marker,
		},
	}
	var created codegTask
	if err := bridge.codeg.call(ctx, http.MethodPost, "/api/work_task_create", "", map[string]any{"draft": draft}, &created); err != nil {
		return fmt.Errorf("ambiguous Codeg create; next run reconciles marker without resending: %w", err)
	}
	if !bridge.matches(created) {
		return errors.New("ambiguous Codeg create: response does not match persisted intent")
	}
	active.CodegID, active.Phase = created.ID, "created"
	return bridge.save()
}

func (bridge *Bridge) matches(task codegTask) bool {
	var config struct {
		Marker string `json:"tack_bridge_marker"`
	}
	return task.ID > 0 && task.FolderID == bridge.config.FolderID && json.Unmarshal(task.Config, &config) == nil && config.Marker == bridge.state.Active.Marker
}

func (bridge *Bridge) getTask(ctx context.Context) (codegTask, error) {
	var task codegTask
	err := bridge.codeg.call(ctx, http.MethodPost, "/api/work_task_get", "", map[string]int64{"id": bridge.state.Active.CodegID}, &task)
	if err != nil {
		return task, err
	}
	if task.ID != bridge.state.Active.CodegID || !bridge.matches(task) {
		return task, errors.New("codeg task no longer matches persisted mapping; reconcile manually")
	}
	return task, nil
}

func (bridge *Bridge) monitor(ctx context.Context) error {
	active := bridge.state.Active
	task, err := bridge.getTask(ctx)
	if err != nil {
		return err
	}
	if task.Status == "todo" {
		if active.Phase == "starting" || active.Phase == "monitoring" {
			return errors.New("codeg start outcome is unresolved or task was requeued; refusing automatic restart")
		}
		if err := bridge.heartbeat(ctx); err != nil {
			return err
		}
		active.Phase = "starting"
		if err := bridge.save(); err != nil {
			return err
		}
		if err := bridge.codeg.call(ctx, http.MethodPost, "/api/work_task_start", "", map[string]int64{"id": active.CodegID}, nil); err != nil {
			return fmt.Errorf("codeg start outcome unknown; next run reads task status: %w", err)
		}
		active.Phase = "monitoring"
		return bridge.save()
	}
	switch task.Status {
	case "queued", "preparing", "running", "awaiting_input", "merging":
		active.Phase = "monitoring"
		return bridge.save()
	case "review", "done", "failed", "canceled":
		if err := bridge.heartbeat(ctx); err != nil {
			return err
		}
		status := "review"
		if bridge.config.ResultStatus == "done" && (task.Status == "done" || task.Status == "review") && task.Verdict != "blocked" {
			status = "done"
		}
		comment := fmt.Sprintf("Codeg work_task %d: %s", task.ID, task.Status)
		for _, detail := range []string{task.Verdict, task.FailureReason, task.LastError, task.ResultSummary} {
			if strings.TrimSpace(detail) != "" {
				comment += "\n" + detail
			}
		}
		comment = redact(comment, bridge.config)
		if len([]rune(comment)) > 4000 {
			comment = string([]rune(comment)[:4000])
		}
		active.Result = &taskResult{ClaimID: active.Claim.ID, Status: status, Comment: comment, Links: []resultLink{{Kind: "codeg_work_task", ExternalID: strconv.FormatInt(task.ID, 10), URL: bridge.taskURL(task)}}}
		key, err := randomKey()
		if err != nil {
			return err
		}
		active.ResultKey, active.Phase = key, "result_pending"
		if err := bridge.save(); err != nil {
			return err
		}
		return bridge.submitResult(ctx)
	default:
		return errors.New("codeg returned an unsupported task status; stopped for contract review")
	}
}

func (bridge *Bridge) taskURL(task codegTask) string {
	if task.ConversationID != nil && *task.ConversationID > 0 && task.AgentType != "" {
		folderID := task.FolderID
		if task.WorktreeFolderID != nil && *task.WorktreeFolderID > 0 {
			folderID = *task.WorktreeFolderID
		}
		query := url.Values{"folderId": {strconv.FormatInt(folderID, 10)}, "conversationId": {strconv.FormatInt(*task.ConversationID, 10)}, "agent": {task.AgentType}}
		return bridge.config.CodegURL + "/workspace?" + query.Encode()
	}
	return bridge.config.CodegURL + "/workspace"
}

func (bridge *Bridge) submitResult(ctx context.Context) error {
	active := bridge.state.Active
	if err := bridge.tack.call(ctx, http.MethodPost, bridge.taskRoute("result"), active.ResultKey, active.Result, nil); err != nil {
		return fmt.Errorf("result not acknowledged; retry replays only the saved result: %w", err)
	}
	bridge.state.Completed[strconv.FormatInt(active.Task.ID, 10)] = active.CodegID
	bridge.state.Active = nil
	return bridge.save()
}
