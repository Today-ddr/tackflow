package store

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/tasks"
)

var ErrInvalidCursor = errors.New("invalid cursor")

type Task struct {
	ID          int64
	ProjectID   *int64
	Title       string
	Notes       string
	Status      string
	Priority    int
	Position    float64
	DueAt       *time.Time
	AgentReady  bool
	CreatedAt   time.Time
	UpdatedAt   time.Time
	CompletedAt *time.Time
	DeletedAt   *time.Time
	Project     *Project
	Tags        []Tag
}

type TaskEvent struct {
	ID        int64
	TaskID    int64
	Kind      string
	Payload   json.RawMessage
	CreatedAt time.Time
}

type TaskLink struct {
	ID         int64
	TaskID     int64
	Kind       string
	ExternalID string
	URL        *string
	CreatedAt  time.Time
}

type TaskFilter struct {
	AllowedProjectIDs []int64
	AllowedTagIDs     []int64
	Statuses          []string
	ProjectID         *int64
	TagID             *int64
	Query             string
	AgentReady        *bool
	UpdatedSince      *time.Time
	Limit             int
	Cursor            string
}

type CreateTaskParams struct {
	ProjectID  *int64
	Title      string
	Notes      string
	Status     string
	Priority   int
	Position   *float64
	DueAt      *time.Time
	AgentReady bool
	TagIDs     []int64
	Now        time.Time
}

type UpdateTaskParams struct {
	Title      *string
	Notes      *string
	ProjectID  *int64
	ProjectSet bool
	Priority   *int
	DueAt      *time.Time
	DueAtSet   bool
	AgentReady *bool
	TagIDs     []int64
	TagsSet    bool
}

func (s *Store) ListTasks(ctx context.Context, filter TaskFilter) ([]Task, string, error) {
	limit := filter.Limit
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}

	where := []string{"t.deleted_at IS NULL"}
	args := make([]any, 0, 8)
	if len(filter.AllowedProjectIDs) > 0 {
		placeholders := make([]string, 0, len(filter.AllowedProjectIDs))
		for _, id := range filter.AllowedProjectIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		where = append(where, "t.project_id IN ("+strings.Join(placeholders, ",")+")")
	}
	if len(filter.AllowedTagIDs) > 0 {
		placeholders := make([]string, 0, len(filter.AllowedTagIDs))
		for _, id := range filter.AllowedTagIDs {
			placeholders = append(placeholders, "?")
			args = append(args, id)
		}
		where = append(where, "EXISTS (SELECT 1 FROM task_tag acl WHERE acl.task_id = t.id AND acl.tag_id IN ("+strings.Join(placeholders, ",")+"))")
	}
	if len(filter.Statuses) > 0 {
		placeholders := make([]string, 0, len(filter.Statuses))
		for _, status := range filter.Statuses {
			placeholders = append(placeholders, "?")
			args = append(args, status)
		}
		where = append(where, "t.status IN ("+strings.Join(placeholders, ",")+")")
	}
	if filter.ProjectID != nil {
		where = append(where, "t.project_id = ?")
		args = append(args, *filter.ProjectID)
	}
	if filter.TagID != nil {
		where = append(where, "EXISTS (SELECT 1 FROM task_tag fx WHERE fx.task_id = t.id AND fx.tag_id = ?)")
		args = append(args, *filter.TagID)
	}
	if filter.Query != "" {
		where = append(where, "(LOWER(t.title) LIKE LOWER(?) OR LOWER(t.notes) LIKE LOWER(?))")
		needle := "%" + strings.TrimSpace(filter.Query) + "%"
		args = append(args, needle, needle)
	}
	if filter.AgentReady != nil {
		where = append(where, "t.agent_ready = ?")
		if *filter.AgentReady {
			args = append(args, 1)
		} else {
			args = append(args, 0)
		}
	}
	if filter.UpdatedSince != nil {
		where = append(where, "t.updated_at >= ?")
		args = append(args, filter.UpdatedSince.UTC().Format(time.RFC3339))
	}
	if filter.Cursor != "" {
		position, id, err := decodeTaskCursor(filter.Cursor)
		if err != nil {
			return nil, "", err
		}
		where = append(where, "(t.position > ? OR (t.position = ? AND t.id > ?))")
		args = append(args, position, position, id)
	}

	query := taskSelect + " WHERE " + strings.Join(where, " AND ") +
		" ORDER BY t.position, t.id LIMIT ?"
	args = append(args, limit+1)

	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", fmt.Errorf("list tasks: %w", err)
	}
	defer func() { _ = rows.Close() }()

	result := make([]Task, 0, limit)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			return nil, "", err
		}
		result = append(result, task)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("iterate tasks: %w", err)
	}

	nextCursor := ""
	if len(result) > limit {
		result = result[:limit]
		last := result[len(result)-1]
		nextCursor = encodeTaskCursor(last.Position, last.ID)
	}
	if err := s.attachTags(ctx, result); err != nil {
		return nil, "", err
	}
	return result, nextCursor, nil
}

func (s *Store) TaskByID(ctx context.Context, id int64) (Task, error) {
	row := s.db.QueryRowContext(ctx, taskSelect+" WHERE t.id = ? AND t.deleted_at IS NULL", id)
	task, err := scanTask(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	items := []Task{task}
	if err := s.attachTags(ctx, items); err != nil {
		return Task{}, err
	}
	return items[0], nil
}

func (s *Store) CreateTask(ctx context.Context, params CreateTaskParams) (Task, error) {
	if params.Status == "" {
		params.Status = tasks.StatusInbox
	}
	if !tasks.ValidStatus(params.Status) {
		return Task{}, fmt.Errorf("invalid task status")
	}
	now := params.Now.UTC()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var taskID int64
	if err := tx.QueryRowContext(ctx, `
		UPDATE resource_sequence SET last_id = last_id + 1
		WHERE resource = 'task' AND last_id < 9223372036854775807
		RETURNING last_id
	`).Scan(&taskID); err != nil {
		return Task{}, fmt.Errorf("allocate task identity: %w", err)
	}

	position := float64(0)
	if params.Position != nil {
		position = *params.Position
	} else if err := tx.QueryRowContext(
		ctx,
		"SELECT COALESCE(MAX(position), 0) + 1024 FROM task WHERE status = ? AND deleted_at IS NULL",
		params.Status,
	).Scan(&position); err != nil {
		return Task{}, fmt.Errorf("next task position: %w", err)
	}

	var completedAt any
	if params.Status == tasks.StatusDone {
		completedAt = now.Format(time.RFC3339)
	}
	var dueAt any
	if params.DueAt != nil {
		dueAt = params.DueAt.UTC().Format(time.RFC3339)
	}
	var projectID any
	if params.ProjectID != nil {
		projectID = *params.ProjectID
	}

	_, err = tx.ExecContext(ctx, `
		INSERT INTO task(
			id, project_id, title, notes, status, priority, position, due_at,
			agent_ready, created_at, updated_at, completed_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`,
		taskID,
		projectID,
		params.Title,
		params.Notes,
		params.Status,
		params.Priority,
		position,
		dueAt,
		boolInt(params.AgentReady),
		now.Format(time.RFC3339),
		now.Format(time.RFC3339),
		completedAt,
	)
	if err != nil {
		return Task{}, fmt.Errorf("insert task: %w", err)
	}
	if err := replaceTaskTags(ctx, tx, taskID, params.TagIDs); err != nil {
		return Task{}, err
	}
	if err := insertTaskEvent(ctx, tx, taskID, "created", map[string]any{
		"status": params.Status,
	}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.TaskByID(ctx, taskID)
}

func (s *Store) UpdateTask(ctx context.Context, id int64, params UpdateTaskParams, now time.Time) (Task, error) {
	current, err := s.TaskByID(ctx, id)
	if err != nil {
		return Task{}, err
	}

	if params.Title != nil {
		current.Title = *params.Title
	}
	if params.Notes != nil {
		current.Notes = *params.Notes
	}
	if params.ProjectSet {
		current.ProjectID = params.ProjectID
	}
	if params.Priority != nil {
		current.Priority = *params.Priority
	}
	if params.DueAtSet {
		current.DueAt = params.DueAt
	}
	if params.AgentReady != nil {
		current.AgentReady = *params.AgentReady
	}

	var projectID any
	if current.ProjectID != nil {
		projectID = *current.ProjectID
	}
	var dueAt any
	if current.DueAt != nil {
		dueAt = current.DueAt.UTC().Format(time.RFC3339)
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(ctx, `
		UPDATE task
		SET project_id = ?, title = ?, notes = ?, priority = ?, due_at = ?,
		    agent_ready = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL
	`,
		projectID,
		current.Title,
		current.Notes,
		current.Priority,
		dueAt,
		boolInt(current.AgentReady),
		now.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return Task{}, fmt.Errorf("update task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Task{}, err
	}
	if affected == 0 {
		return Task{}, ErrNotFound
	}
	if params.TagsSet {
		if err := replaceTaskTags(ctx, tx, id, params.TagIDs); err != nil {
			return Task{}, err
		}
	}
	if err := insertTaskEvent(ctx, tx, id, "updated", map[string]any{}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) MoveTask(
	ctx context.Context,
	id int64,
	status string,
	position *float64,
	now time.Time,
) (Task, error) {
	if !tasks.ValidStatus(status) {
		return Task{}, fmt.Errorf("invalid task status")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentStatus string
	var currentPosition float64
	var completedAt sql.NullString
	if err := tx.QueryRowContext(
		ctx,
		"SELECT status, position, completed_at FROM task WHERE id = ? AND deleted_at IS NULL",
		id,
	).Scan(&currentStatus, &currentPosition, &completedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Task{}, ErrNotFound
		}
		return Task{}, err
	}

	targetPosition := currentPosition
	if position != nil {
		targetPosition = *position
	} else if status != currentStatus {
		if err := tx.QueryRowContext(
			ctx,
			"SELECT COALESCE(MAX(position), 0) + 1024 FROM task WHERE status = ? AND deleted_at IS NULL",
			status,
		).Scan(&targetPosition); err != nil {
			return Task{}, err
		}
	}

	var newCompletedAt any
	if completedAt.Valid {
		newCompletedAt = completedAt.String
	}
	if status == tasks.StatusDone && !completedAt.Valid {
		newCompletedAt = now.UTC().Format(time.RFC3339)
	}
	if status != tasks.StatusDone {
		newCompletedAt = nil
	}

	if _, err := tx.ExecContext(ctx, `
		UPDATE task
		SET status = ?, position = ?, completed_at = ?, updated_at = ?
		WHERE id = ? AND deleted_at IS NULL
	`,
		status,
		targetPosition,
		newCompletedAt,
		now.UTC().Format(time.RFC3339),
		id,
	); err != nil {
		return Task{}, fmt.Errorf("move task: %w", err)
	}

	if err := rebalanceIfNeeded(ctx, tx, status); err != nil {
		return Task{}, err
	}

	if err := insertTaskEvent(ctx, tx, id, "moved", map[string]any{
		"from":     currentStatus,
		"to":       status,
		"position": targetPosition,
	}, now); err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return s.TaskByID(ctx, id)
}

func (s *Store) DeleteTask(ctx context.Context, id int64, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	result, err := tx.ExecContext(
		ctx,
		"UPDATE task SET deleted_at = ?, updated_at = ? WHERE id = ? AND deleted_at IS NULL",
		now.UTC().Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	if err := insertTaskEvent(ctx, tx, id, "deleted", map[string]any{}, now); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) AddComment(ctx context.Context, taskID int64, text string, now time.Time) (TaskEvent, error) {
	if _, err := s.TaskByID(ctx, taskID); err != nil {
		return TaskEvent{}, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return TaskEvent{}, err
	}
	defer func() { _ = tx.Rollback() }()
	if err := insertTaskEvent(ctx, tx, taskID, "commented", map[string]any{"text": text}, now); err != nil {
		return TaskEvent{}, err
	}
	var eventID int64
	if err := tx.QueryRowContext(ctx, "SELECT last_insert_rowid()").Scan(&eventID); err != nil {
		return TaskEvent{}, err
	}
	if err := tx.Commit(); err != nil {
		return TaskEvent{}, err
	}
	return TaskEvent{
		ID:        eventID,
		TaskID:    taskID,
		Kind:      "commented",
		Payload:   mustJSON(map[string]any{"text": text}),
		CreatedAt: now.UTC(),
	}, nil
}

func (s *Store) TaskEvents(ctx context.Context, taskID int64) ([]TaskEvent, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, kind, payload, created_at
		FROM task_event
		WHERE task_id = ?
		ORDER BY created_at, id
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task events: %w", err)
	}
	defer func() { _ = rows.Close() }()

	events := make([]TaskEvent, 0)
	for rows.Next() {
		var event TaskEvent
		var payload, createdAt string
		if err := rows.Scan(&event.ID, &event.TaskID, &event.Kind, &payload, &createdAt); err != nil {
			return nil, err
		}
		event.Payload = json.RawMessage(payload)
		event.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse task event created_at: %w", err)
		}
		events = append(events, event)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task events: %w", err)
	}
	return events, nil
}

func (s *Store) TaskLinks(ctx context.Context, taskID int64) ([]TaskLink, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, task_id, kind, external_id, url, created_at
		FROM task_link
		WHERE task_id = ?
		ORDER BY created_at, id
	`, taskID)
	if err != nil {
		return nil, fmt.Errorf("list task links: %w", err)
	}
	defer func() { _ = rows.Close() }()

	links := make([]TaskLink, 0)
	for rows.Next() {
		var link TaskLink
		var url, createdAt sql.NullString
		if err := rows.Scan(&link.ID, &link.TaskID, &link.Kind, &link.ExternalID, &url, &createdAt); err != nil {
			return nil, err
		}
		if url.Valid {
			link.URL = &url.String
		}
		link.CreatedAt, err = time.Parse(time.RFC3339, createdAt.String)
		if err != nil {
			return nil, fmt.Errorf("parse task link created_at: %w", err)
		}
		links = append(links, link)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate task links: %w", err)
	}
	return links, nil
}

func (s *Store) PurgeTrash(ctx context.Context, before time.Time) (int64, error) {
	result, err := s.db.ExecContext(
		ctx,
		"DELETE FROM task WHERE deleted_at IS NOT NULL AND deleted_at <= ?",
		before.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("purge trash: %w", err)
	}
	return result.RowsAffected()
}

const taskSelect = `
	SELECT t.id, t.project_id, t.title, t.notes, t.status, t.priority, t.position,
	       t.due_at, t.agent_ready, t.created_at, t.updated_at, t.completed_at,
	       t.deleted_at,
	       p.id, p.name, p.color, p.position, p.archived_at, p.created_at
	FROM task t
	LEFT JOIN project p ON p.id = t.project_id
`

type taskScanner interface {
	Scan(dest ...any) error
}

func scanTask(scanner taskScanner) (Task, error) {
	var task Task
	var projectID sql.NullInt64
	var dueAt, completedAt, deletedAt sql.NullString
	var agentReady int
	var createdAt, updatedAt string
	var projectProjectID sql.NullInt64
	var projectName, projectColor sql.NullString
	var projectPosition sql.NullFloat64
	var projectArchivedAt, projectCreatedAt sql.NullString

	if err := scanner.Scan(
		&task.ID,
		&projectID,
		&task.Title,
		&task.Notes,
		&task.Status,
		&task.Priority,
		&task.Position,
		&dueAt,
		&agentReady,
		&createdAt,
		&updatedAt,
		&completedAt,
		&deletedAt,
		&projectProjectID,
		&projectName,
		&projectColor,
		&projectPosition,
		&projectArchivedAt,
		&projectCreatedAt,
	); err != nil {
		return Task{}, err
	}

	if projectID.Valid {
		task.ProjectID = &projectID.Int64
	}
	task.AgentReady = agentReady != 0
	var err error
	task.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Task{}, fmt.Errorf("parse task created_at: %w", err)
	}
	task.UpdatedAt, err = time.Parse(time.RFC3339, updatedAt)
	if err != nil {
		return Task{}, fmt.Errorf("parse task updated_at: %w", err)
	}
	if dueAt.Valid {
		parsed, err := time.Parse(time.RFC3339, dueAt.String)
		if err != nil {
			return Task{}, fmt.Errorf("parse task due_at: %w", err)
		}
		task.DueAt = &parsed
	}
	if completedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, completedAt.String)
		if err != nil {
			return Task{}, fmt.Errorf("parse task completed_at: %w", err)
		}
		task.CompletedAt = &parsed
	}
	if deletedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, deletedAt.String)
		if err != nil {
			return Task{}, fmt.Errorf("parse task deleted_at: %w", err)
		}
		task.DeletedAt = &parsed
	}
	if projectProjectID.Valid {
		project := &Project{
			ID:       projectProjectID.Int64,
			Name:     projectName.String,
			Color:    projectColor.String,
			Position: projectPosition.Float64,
		}
		project.CreatedAt, err = time.Parse(time.RFC3339, projectCreatedAt.String)
		if err != nil {
			return Task{}, fmt.Errorf("parse project created_at: %w", err)
		}
		if projectArchivedAt.Valid {
			parsed, err := time.Parse(time.RFC3339, projectArchivedAt.String)
			if err != nil {
				return Task{}, fmt.Errorf("parse project archived_at: %w", err)
			}
			project.ArchivedAt = &parsed
		}
		task.Project = project
	}
	task.Tags = make([]Tag, 0)
	return task, nil
}

func (s *Store) attachTags(ctx context.Context, items []Task) error {
	if len(items) == 0 {
		return nil
	}
	byTask := make(map[int64][]Tag, len(items))
	ids := make([]any, 0, len(items))
	placeholders := make([]string, 0, len(items))
	for _, task := range items {
		ids = append(ids, task.ID)
		placeholders = append(placeholders, "?")
	}
	query := `
		SELECT tt.task_id, g.id, g.name, g.color, g.created_at
		FROM task_tag tt
		JOIN tag g ON g.id = tt.tag_id
		WHERE tt.task_id IN (` + strings.Join(placeholders, ",") + `)
		ORDER BY g.name COLLATE NOCASE, g.id
	`
	rows, err := s.db.QueryContext(ctx, query, ids...)
	if err != nil {
		return fmt.Errorf("load task tags: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var taskID int64
		var tag Tag
		var createdAt string
		if err := rows.Scan(&taskID, &tag.ID, &tag.Name, &tag.Color, &createdAt); err != nil {
			return err
		}
		tag.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return fmt.Errorf("parse tag created_at: %w", err)
		}
		byTask[taskID] = append(byTask[taskID], tag)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate task tags: %w", err)
	}
	for i := range items {
		if tags, ok := byTask[items[i].ID]; ok {
			items[i].Tags = tags
		}
	}
	return nil
}

func replaceTaskTags(ctx context.Context, tx *sql.Tx, taskID int64, tagIDs []int64) error {
	if _, err := tx.ExecContext(ctx, "DELETE FROM task_tag WHERE task_id = ?", taskID); err != nil {
		return fmt.Errorf("clear task tags: %w", err)
	}
	seen := make(map[int64]struct{}, len(tagIDs))
	for _, tagID := range tagIDs {
		if _, ok := seen[tagID]; ok {
			continue
		}
		seen[tagID] = struct{}{}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO task_tag(task_id, tag_id) VALUES (?, ?)",
			taskID, tagID,
		); err != nil {
			return fmt.Errorf("insert task tag: %w", err)
		}
	}
	return nil
}

func insertTaskEvent(
	ctx context.Context,
	tx *sql.Tx,
	taskID int64,
	kind string,
	payload map[string]any,
	now time.Time,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(
		ctx,
		"INSERT INTO task_event(task_id, kind, payload, created_at) VALUES (?, ?, ?, ?)",
		taskID, kind, string(body), now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("insert task event: %w", err)
	}
	return nil
}

func rebalanceIfNeeded(ctx context.Context, tx *sql.Tx, status string) error {
	rows, err := tx.QueryContext(ctx, `
		SELECT id, position
		FROM task
		WHERE status = ? AND deleted_at IS NULL
		ORDER BY position, id
	`, status)
	if err != nil {
		return err
	}
	var ids []int64
	var positions []float64
	for rows.Next() {
		var id int64
		var position float64
		if err := rows.Scan(&id, &position); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
		positions = append(positions, position)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if err := rows.Err(); err != nil {
		return err
	}

	needs := false
	for i := 1; i < len(positions); i++ {
		if positions[i]-positions[i-1] < 1e-9 {
			needs = true
			break
		}
	}
	if !needs {
		return nil
	}
	for i, id := range ids {
		if _, err := tx.ExecContext(
			ctx,
			"UPDATE task SET position = ? WHERE id = ?",
			float64((i+1)*1024), id,
		); err != nil {
			return fmt.Errorf("rebalance task position: %w", err)
		}
	}
	return nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func encodeTaskCursor(position float64, id int64) string {
	raw := strconv.FormatFloat(position, 'f', -1, 64) + ":" + strconv.FormatInt(id, 10)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeTaskCursor(cursor string) (float64, int64, error) {
	raw, err := base64.RawURLEncoding.DecodeString(cursor)
	if err != nil {
		return 0, 0, ErrInvalidCursor
	}
	positionText, idText, ok := strings.Cut(string(raw), ":")
	if !ok {
		return 0, 0, ErrInvalidCursor
	}
	position, err := strconv.ParseFloat(positionText, 64)
	if err != nil {
		return 0, 0, ErrInvalidCursor
	}
	id, err := strconv.ParseInt(idText, 10, 64)
	if err != nil {
		return 0, 0, ErrInvalidCursor
	}
	return position, id, nil
}

func mustJSON(value any) json.RawMessage {
	body, _ := json.Marshal(value)
	return body
}
