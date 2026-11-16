package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type CachedResponse struct {
	Status int
	Body   []byte
}

func (s *Store) GetIdempotency(ctx context.Context, key string, now time.Time) (CachedResponse, bool, error) {
	var response CachedResponse
	var body string
	var createdAt string
	err := s.db.QueryRowContext(
		ctx,
		"SELECT status, body, created_at FROM idempotency WHERE key = ?",
		key,
	).Scan(&response.Status, &body, &createdAt)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return CachedResponse{}, false, nil
		}
		return CachedResponse{}, false, fmt.Errorf("get idempotency key: %w", err)
	}
	created, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return CachedResponse{}, false, fmt.Errorf("parse idempotency created_at: %w", err)
	}
	if now.Sub(created) > 24*time.Hour {
		_, _ = s.db.ExecContext(ctx, "DELETE FROM idempotency WHERE key = ?", key)
		return CachedResponse{}, false, nil
	}
	response.Body = []byte(body)
	return response, true, nil
}

func (s *Store) PutIdempotency(
	ctx context.Context,
	key string,
	status int,
	body []byte,
	now time.Time,
) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO idempotency(key, status, body, created_at) VALUES (?, ?, ?, ?)
		 ON CONFLICT(key) DO UPDATE SET status = excluded.status, body = excluded.body,
		 created_at = excluded.created_at`,
		key, status, string(body), now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return fmt.Errorf("put idempotency key: %w", err)
	}
	return nil
}
