package store

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"
	"unicode"
)

var (
	ErrAgentClaimLost    = errors.New("agent claim lost or expired")
	ErrInvalidAgentInput = errors.New("invalid agent input")
	ErrAgentLinkConflict = errors.New("external link belongs to another task")
)

const agentClaimLifetime = 30 * time.Minute

type AgentClaim struct {
	ID        string
	ExpiresAt time.Time
}

type AgentClaimReplay struct {
	Key      string
	Seal     func(string, []byte) ([]byte, error)
	Open     func(string, []byte) ([]byte, error)
	Replayed bool
}

type agentClaimReplayResult struct {
	Task  *Task       `json:"task"`
	Claim *AgentClaim `json:"claim"`
}

type AgentResultLink struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	URL        string `json:"url,omitempty"`
}

type AgentResultParams struct {
	ClaimID string            `json:"claim_id"`
	Status  string            `json:"status,omitempty"`
	Comment string            `json:"comment"`
	Links   []AgentResultLink `json:"links,omitempty"`
}

func (s *Store) NextAgentTask(ctx context.Context, filter TaskFilter) (*Task, error) {
	ready := true
	filter.Statuses = []string{"todo"}
	filter.AgentReady = &ready
	filter.Limit = 1
	filter.Cursor = ""
	items, _, err := s.ListTasks(ctx, filter)
	if err != nil || len(items) == 0 {
		return nil, err
	}
	return &items[0], nil
}

func (s *Store) ClaimAgentTask(ctx context.Context, filter TaskFilter, owner, worker string, now time.Time, options ...*AgentClaimReplay) (*Task, *AgentClaim, error) {
	var replay *AgentClaimReplay
	if len(options) > 1 {
		return nil, nil, ErrInvalidAgentInput
	}
	if len(options) == 1 {
		replay = options[0]
	}
	if replay != nil {
		replay.Replayed = false
		if replay.Key == "" || len(replay.Key) > 200 || replay.Seal == nil || replay.Open == nil {
			return nil, nil, ErrInvalidAgentInput
		}
	}
	worker = strings.TrimSpace(worker)
	if owner == "" || worker == "" || len([]rune(worker)) > 100 {
		return nil, nil, ErrInvalidAgentInput
	}
	now = now.UTC()
	tx, err := s.beginTaskWrite(ctx)
	if err != nil {
		return nil, nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if replay != nil {
		previous, err := readAgentClaimReplay(ctx, tx, filter, owner, replay, now)
		if err != nil {
			return nil, nil, err
		}
		if previous != nil {
			if err := tx.Commit(); err != nil {
				return nil, nil, err
			}
			replay.Replayed = true
			return previous.Task, previous.Claim, nil
		}
	}
	if err := requeueExpiredAgentTasks(ctx, tx, filter, now); err != nil {
		return nil, nil, err
	}
	where, args := agentTaskConditions(filter, "todo")
	var taskID int64
	err = tx.QueryRowContext(ctx, "SELECT t.id FROM task t WHERE "+where+" ORDER BY t.position, t.id LIMIT 1", args...).Scan(&taskID)
	if errors.Is(err, sql.ErrNoRows) {
		if err := persistAgentClaimReplay(ctx, tx, owner, replay, nil, nil, now); err != nil {
			return nil, nil, err
		}
		return nil, nil, tx.Commit()
	}
	if err != nil {
		return nil, nil, err
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return nil, nil, fmt.Errorf("generate claim: %w", err)
	}
	claim := &AgentClaim{ID: base64.RawURLEncoding.EncodeToString(random), ExpiresAt: now.Add(agentClaimLifetime)}
	if err := moveAgentTask(ctx, tx, taskID, "todo", "doing", true, "agent_claimed", worker, now); err != nil {
		return nil, nil, err
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_claim(task_id, claim_hash, owner, worker, expires_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(task_id) DO UPDATE SET claim_hash = excluded.claim_hash,
		owner = excluded.owner, worker = excluded.worker, expires_at = excluded.expires_at,
		invalidated = 0, result_hash = NULL, result_json = NULL
	`, taskID, agentSecretHash(claim.ID), owner, worker, claim.ExpiresAt.Format(time.RFC3339)); err != nil {
		return nil, nil, err
	}
	task, err := taskInTransaction(ctx, tx, taskID)
	if err != nil {
		return nil, nil, err
	}
	if err := persistAgentClaimReplay(ctx, tx, owner, replay, &task, claim, now); err != nil {
		return nil, nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, nil, err
	}
	return &task, claim, nil
}

func persistAgentClaimReplay(ctx context.Context, tx *sql.Tx, owner string, replay *AgentClaimReplay, task *Task, claim *AgentClaim, now time.Time) error {
	if replay == nil {
		return nil
	}
	body, err := json.Marshal(agentClaimReplayResult{Task: task, Claim: claim})
	if err != nil {
		return err
	}
	sealed, err := replay.Seal(replay.Key, body)
	if err != nil {
		return fmt.Errorf("seal agent claim replay: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO agent_claim_replay(key, owner, body, expires_at) VALUES (?, ?, ?, ?)
	`, replay.Key, owner, sealed, now.Add(24*time.Hour).Format(time.RFC3339))
	return err
}

func readAgentClaimReplay(ctx context.Context, tx *sql.Tx, filter TaskFilter, owner string, replay *AgentClaimReplay, now time.Time) (*agentClaimReplayResult, error) {
	var body []byte
	var storedOwner, expiresAt string
	err := tx.QueryRowContext(ctx, "SELECT owner, body, expires_at FROM agent_claim_replay WHERE key = ?", replay.Key).Scan(&storedOwner, &body, &expiresAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	expires, err := time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return nil, err
	}
	if storedOwner != owner || !expires.After(now) {
		return nil, ErrAgentClaimLost
	}
	opened, err := replay.Open(replay.Key, body)
	if err != nil {
		return nil, fmt.Errorf("open agent claim replay: %w", err)
	}
	var previous agentClaimReplayResult
	if err := json.Unmarshal(opened, &previous); err != nil {
		return nil, err
	}
	if (previous.Task == nil) != (previous.Claim == nil) {
		return nil, errors.New("incomplete agent claim replay")
	}
	if previous.Task == nil {
		return &previous, nil
	}
	current, err := taskInTransaction(ctx, tx, previous.Task.ID)
	if err != nil {
		return nil, err
	}
	if current.DeletedAt != nil || !agentReplayTaskAllowed(filter, current) {
		return nil, ErrNotFound
	}
	if current.Status != "doing" || !current.AgentReady {
		return nil, ErrAgentClaimLost
	}
	var active bool
	err = tx.QueryRowContext(ctx, `
		SELECT EXISTS (SELECT 1 FROM agent_claim WHERE task_id = ? AND owner = ? AND claim_hash = ?
		AND invalidated = 0 AND result_hash IS NULL AND expires_at > ?)
	`, current.ID, owner, agentSecretHash(previous.Claim.ID), now.Format(time.RFC3339)).Scan(&active)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, ErrAgentClaimLost
	}
	return &previous, nil
}

func agentReplayTaskAllowed(filter TaskFilter, task Task) bool {
	if len(filter.AllowedProjectIDs) > 0 && (task.ProjectID == nil || !slices.Contains(filter.AllowedProjectIDs, *task.ProjectID)) {
		return false
	}
	if len(filter.AllowedTagIDs) > 0 {
		for _, tag := range task.Tags {
			if slices.Contains(filter.AllowedTagIDs, tag.ID) {
				return true
			}
		}
		return false
	}
	return true
}

func (s *Store) HeartbeatAgentTask(ctx context.Context, taskID int64, owner, claimID string, now time.Time) (AgentClaim, error) {
	expiresAt := now.UTC().Add(agentClaimLifetime)
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_claim SET expires_at = ?
		WHERE task_id = ? AND owner = ? AND claim_hash = ? AND invalidated = 0
		AND result_hash IS NULL AND expires_at > ? AND EXISTS (
			SELECT 1 FROM task WHERE id = agent_claim.task_id AND deleted_at IS NULL
			AND status = 'doing' AND agent_ready = 1
		)
	`, expiresAt.Format(time.RFC3339), taskID, owner, agentSecretHash(claimID), now.UTC().Format(time.RFC3339))
	if err != nil {
		return AgentClaim{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return AgentClaim{}, err
	}
	if affected == 0 {
		return AgentClaim{}, ErrAgentClaimLost
	}
	return AgentClaim{ID: claimID, ExpiresAt: expiresAt}, nil
}

func (s *Store) CompleteAgentTask(ctx context.Context, taskID int64, owner string, params AgentResultParams, now time.Time) (Task, error) {
	params, err := normalizeAgentResult(params)
	if err != nil {
		return Task{}, err
	}
	claimHash := agentSecretHash(params.ClaimID)
	params.ClaimID = ""
	encoded, err := json.Marshal(params)
	if err != nil {
		return Task{}, err
	}
	resultHash := agentSecretHash(string(encoded))
	now = now.UTC()
	tx, err := s.beginTaskWrite(ctx)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	var previousHash, previousJSON, worker string
	err = tx.QueryRowContext(ctx, `
		SELECT COALESCE(result_hash, ''), COALESCE(result_json, ''), worker
		FROM agent_claim WHERE task_id = ? AND owner = ? AND claim_hash = ?
		AND invalidated = 0 AND expires_at > ?
	`, taskID, owner, claimHash, now.Format(time.RFC3339)).Scan(&previousHash, &previousJSON, &worker)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrAgentClaimLost
	}
	if err != nil {
		return Task{}, err
	}
	if previousHash != "" {
		if previousHash != resultHash {
			return Task{}, ErrAgentClaimLost
		}
		var previous Task
		if err := json.Unmarshal([]byte(previousJSON), &previous); err != nil {
			return Task{}, err
		}
		return previous, tx.Commit()
	}
	task, err := taskInTransaction(ctx, tx, taskID)
	if errors.Is(err, ErrNotFound) || err == nil && (task.Status != "doing" || !task.AgentReady || task.DeletedAt != nil) {
		return Task{}, ErrAgentClaimLost
	}
	if err != nil {
		return Task{}, err
	}
	if err := insertTaskEvent(ctx, tx, taskID, "commented", map[string]any{"text": params.Comment, "worker": worker}, now); err != nil {
		return Task{}, err
	}
	for _, link := range params.Links {
		if err := insertAgentLink(ctx, tx, taskID, link, now); err != nil {
			return Task{}, err
		}
	}
	if err := moveAgentTask(ctx, tx, taskID, "doing", params.Status, params.Status == "todo", "agent_result", worker, now); err != nil {
		return Task{}, err
	}
	task, err = taskInTransaction(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	resultJSON, err := json.Marshal(task)
	if err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE agent_claim SET invalidated = 0, result_hash = ?, result_json = ? WHERE task_id = ?", resultHash, string(resultJSON), taskID); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func (s *Store) beginTaskWrite(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, "UPDATE task SET updated_at = updated_at WHERE 0"); err != nil {
		_ = tx.Rollback()
		return nil, err
	}
	return tx, nil
}

func agentTaskConditions(filter TaskFilter, status string) (string, []any) {
	where := []string{"t.deleted_at IS NULL", "t.agent_ready = 1", "t.status = ?"}
	args := []any{status}
	for _, restriction := range []struct {
		ids       []int64
		condition string
	}{
		{filter.AllowedProjectIDs, "t.project_id IN (%s)"},
		{filter.AllowedTagIDs, "EXISTS (SELECT 1 FROM task_tag scoped WHERE scoped.task_id = t.id AND scoped.tag_id IN (%s))"},
	} {
		if len(restriction.ids) == 0 {
			continue
		}
		placeholders := make([]string, len(restriction.ids))
		for index, id := range restriction.ids {
			placeholders[index] = "?"
			args = append(args, id)
		}
		where = append(where, fmt.Sprintf(restriction.condition, strings.Join(placeholders, ",")))
	}
	if filter.ProjectID != nil {
		where = append(where, "t.project_id = ?")
		args = append(args, *filter.ProjectID)
	}
	if filter.TagID != nil {
		where = append(where, "EXISTS (SELECT 1 FROM task_tag selected WHERE selected.task_id = t.id AND selected.tag_id = ?)")
		args = append(args, *filter.TagID)
	}
	if filter.Query != "" {
		where = append(where, "(LOWER(t.title) LIKE LOWER(?) OR LOWER(t.notes) LIKE LOWER(?))")
		needle := "%" + strings.TrimSpace(filter.Query) + "%"
		args = append(args, needle, needle)
	}
	if filter.UpdatedSince != nil {
		where = append(where, "t.updated_at >= ?")
		args = append(args, filter.UpdatedSince.UTC().Format(time.RFC3339))
	}
	return strings.Join(where, " AND "), args
}

func requeueExpiredAgentTasks(ctx context.Context, tx *sql.Tx, filter TaskFilter, now time.Time) error {
	where, args := agentTaskConditions(filter, "doing")
	args = append(args, now.Format(time.RFC3339))
	rows, err := tx.QueryContext(ctx, `
		SELECT t.id FROM task t JOIN agent_claim claim ON claim.task_id = t.id
		WHERE `+where+` AND claim.invalidated = 0 AND claim.result_hash IS NULL AND claim.expires_at <= ?
		ORDER BY t.position, t.id
	`, args...)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for _, id := range ids {
		if err := moveAgentTask(ctx, tx, id, "doing", "todo", true, "agent_claim_expired", "", now); err != nil {
			return err
		}
	}
	return nil
}

func moveAgentTask(ctx context.Context, tx *sql.Tx, taskID int64, from, status string, ready bool, reason, worker string, now time.Time) error {
	var position float64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0) + 1024 FROM task WHERE status = ? AND deleted_at IS NULL", status).Scan(&position); err != nil {
		return err
	}
	var completedAt any
	if status == "done" {
		completedAt = now.Format(time.RFC3339)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task SET status = ?, position = ?, agent_ready = ?, completed_at = ?, updated_at = ? WHERE id = ?
	`, status, position, boolInt(ready), completedAt, now.Format(time.RFC3339), taskID); err != nil {
		return err
	}
	return insertTaskEvent(ctx, tx, taskID, "moved", map[string]any{
		"from": from, "to": status, "position": position, "reason": reason, "worker": worker,
	}, now)
}

func taskInTransaction(ctx context.Context, tx *sql.Tx, taskID int64) (Task, error) {
	task, err := scanTask(tx.QueryRowContext(ctx, taskSelect+" WHERE t.id = ?", taskID))
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT tag.id, tag.name, tag.color, tag.created_at, 0
		FROM tag JOIN task_tag ON task_tag.tag_id = tag.id
		WHERE task_tag.task_id = ? ORDER BY tag.name COLLATE NOCASE, tag.id
	`, taskID)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		tag, err := scanTag(rows)
		if err != nil {
			return Task{}, err
		}
		task.Tags = append(task.Tags, tag)
	}
	return task, rows.Err()
}

func normalizeAgentResult(params AgentResultParams) (AgentResultParams, error) {
	if params.ClaimID == "" || len(params.ClaimID) > 200 {
		return AgentResultParams{}, ErrInvalidAgentInput
	}
	if params.Status == "" {
		params.Status = "review"
	}
	params.Comment = strings.TrimSpace(params.Comment)
	if !slices.Contains([]string{"review", "done", "todo"}, params.Status) || params.Comment == "" || len([]rune(params.Comment)) > 10_000 || len(params.Links) > 20 {
		return AgentResultParams{}, ErrInvalidAgentInput
	}
	links := make([]AgentResultLink, 0, len(params.Links))
	seen := make(map[[2]string]string)
	for _, link := range params.Links {
		link.Kind = strings.TrimSpace(link.Kind)
		link.ExternalID = strings.TrimSpace(link.ExternalID)
		link.URL = strings.TrimSpace(link.URL)
		if link.Kind == "" || len([]rune(link.Kind)) > 80 || link.ExternalID == "" || len([]rune(link.ExternalID)) > 200 || len(link.URL) > 2048 || strings.ContainsFunc(link.Kind+link.ExternalID+link.URL, unicode.IsControl) {
			return AgentResultParams{}, ErrInvalidAgentInput
		}
		if link.URL != "" {
			parsed, err := url.Parse(link.URL)
			if err != nil || !slices.Contains([]string{"https", "http", "codeg"}, parsed.Scheme) || parsed.Host == "" || parsed.User != nil {
				return AgentResultParams{}, ErrInvalidAgentInput
			}
		}
		key := [2]string{link.Kind, link.ExternalID}
		if previous, exists := seen[key]; exists {
			if previous != link.URL {
				return AgentResultParams{}, ErrInvalidAgentInput
			}
			continue
		}
		seen[key] = link.URL
		links = append(links, link)
	}
	slices.SortFunc(links, func(left, right AgentResultLink) int {
		if order := strings.Compare(left.Kind, right.Kind); order != 0 {
			return order
		}
		return strings.Compare(left.ExternalID, right.ExternalID)
	})
	params.Links = links
	return params, nil
}

func insertAgentLink(ctx context.Context, tx *sql.Tx, taskID int64, link AgentResultLink, now time.Time) error {
	var linkURL any
	if link.URL != "" {
		linkURL = link.URL
	}
	result, err := tx.ExecContext(ctx, `
		INSERT INTO task_link(task_id, kind, external_id, url, created_at) VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(kind, external_id) DO NOTHING
	`, taskID, link.Kind, link.ExternalID, linkURL, now.Format(time.RFC3339))
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		var existingTaskID int64
		if err := tx.QueryRowContext(ctx, "SELECT task_id FROM task_link WHERE kind = ? AND external_id = ?", link.Kind, link.ExternalID).Scan(&existingTaskID); err != nil {
			return err
		}
		if existingTaskID != taskID {
			return ErrAgentLinkConflict
		}
		return nil
	}
	return insertTaskEvent(ctx, tx, taskID, "linked", map[string]any{"kind": link.Kind, "external_id": link.ExternalID, "url": link.URL}, now)
}

func agentSecretHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
