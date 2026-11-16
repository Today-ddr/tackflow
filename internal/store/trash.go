package store

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"time"
)

const trashRetention = 30 * 24 * time.Hour

type trashCursor struct {
	DeletedAt string `json:"deleted_at"`
	ID        int64  `json:"id"`
}

func (s *Store) ListTrash(ctx context.Context, limit int, cursor string, now time.Time) ([]Task, string, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 500 {
		limit = 500
	}
	query := taskSelect + " WHERE t.deleted_at IS NOT NULL AND t.deleted_at > ?"
	args := []any{now.UTC().Add(-trashRetention).Format(time.RFC3339)}
	if cursor != "" {
		decoded, err := decodeTrashCursor(cursor)
		if err != nil {
			return nil, "", err
		}
		query += " AND (t.deleted_at < ? OR (t.deleted_at = ? AND t.id < ?))"
		args = append(args, decoded.DeletedAt, decoded.DeletedAt, decoded.ID)
	}
	query += " ORDER BY t.deleted_at DESC, t.id DESC LIMIT ?"
	args = append(args, limit+1)
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, "", err
	}
	items := make([]Task, 0, limit)
	for rows.Next() {
		task, err := scanTask(rows)
		if err != nil {
			_ = rows.Close()
			return nil, "", err
		}
		items = append(items, task)
	}
	if err := rows.Close(); err != nil {
		return nil, "", err
	}
	if err := rows.Err(); err != nil {
		return nil, "", err
	}
	next := ""
	if len(items) > limit {
		items = items[:limit]
		last := items[len(items)-1]
		encoded, err := json.Marshal(trashCursor{DeletedAt: last.DeletedAt.UTC().Format(time.RFC3339), ID: last.ID})
		if err != nil {
			return nil, "", err
		}
		next = base64.RawURLEncoding.EncodeToString(encoded)
	}
	if err := s.attachTags(ctx, items); err != nil {
		return nil, "", err
	}
	return items, next, nil
}

func (s *Store) RestoreTask(ctx context.Context, taskID int64, now time.Time) (Task, error) {
	now = now.UTC()
	tx, err := s.beginTaskWrite(ctx)
	if err != nil {
		return Task{}, err
	}
	defer func() { _ = tx.Rollback() }()
	task, err := taskInTransaction(ctx, tx, taskID)
	if errors.Is(err, ErrNotFound) || err == nil && (task.DeletedAt == nil || !task.DeletedAt.After(now.Add(-trashRetention))) {
		return Task{}, ErrNotFound
	}
	if err != nil {
		return Task{}, err
	}
	var position float64
	if err := tx.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0) + 1024 FROM task WHERE status = ? AND deleted_at IS NULL", task.Status).Scan(&position); err != nil {
		return Task{}, err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE task SET deleted_at = NULL, position = ?, updated_at = ?,
		completed_at = CASE WHEN status = 'done' THEN COALESCE(completed_at, ?) ELSE NULL END
		WHERE id = ?
	`, position, now.Format(time.RFC3339), now.Format(time.RFC3339), taskID); err != nil {
		return Task{}, err
	}
	if err := insertTaskEvent(ctx, tx, taskID, "restored", map[string]any{"status": task.Status}, now); err != nil {
		return Task{}, err
	}
	task, err = taskInTransaction(ctx, tx, taskID)
	if err != nil {
		return Task{}, err
	}
	if err := tx.Commit(); err != nil {
		return Task{}, err
	}
	return task, nil
}

func decodeTrashCursor(value string) (trashCursor, error) {
	if len(value) > 512 {
		return trashCursor{}, ErrInvalidCursor
	}
	encoded, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return trashCursor{}, ErrInvalidCursor
	}
	var decoded trashCursor
	if err := json.Unmarshal(encoded, &decoded); err != nil || decoded.ID <= 0 {
		return trashCursor{}, ErrInvalidCursor
	}
	parsed, err := time.Parse(time.RFC3339, decoded.DeletedAt)
	if err != nil {
		return trashCursor{}, ErrInvalidCursor
	}
	decoded.DeletedAt = parsed.UTC().Format(time.RFC3339)
	return decoded, nil
}
