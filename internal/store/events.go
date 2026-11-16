package store

import (
	"context"
	"time"
)

type ChangeEvent struct {
	ID         int64     `json:"id"`
	Resource   string    `json:"resource"`
	ResourceID int64     `json:"resource_id"`
	Kind       string    `json:"kind"`
	CreatedAt  time.Time `json:"created_at"`
}

func (s *Store) LatestChangeID(ctx context.Context) (int64, error) {
	var id int64
	err := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(id), 0) FROM change_event").Scan(&id)
	return id, err
}

func (s *Store) ChangesAfter(ctx context.Context, cursor int64, limit int) ([]ChangeEvent, error) {
	if cursor < 0 {
		return nil, ErrInvalidCursor
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, resource, resource_id, kind, created_at FROM change_event
		WHERE id > ? ORDER BY id LIMIT ?`, cursor, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := make([]ChangeEvent, 0, limit)
	for rows.Next() {
		var event ChangeEvent
		var createdAt string
		if err := rows.Scan(&event.ID, &event.Resource, &event.ResourceID, &event.Kind, &createdAt); err != nil {
			return nil, err
		}
		event.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
		if err != nil {
			return nil, err
		}
		result = append(result, event)
	}
	return result, rows.Err()
}
