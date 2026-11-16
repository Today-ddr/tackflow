package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Session struct {
	ID         string
	UserID     int64
	ExpiresAt  time.Time
	LastSeenAt time.Time
}

func (s *Store) CreateSession(
	ctx context.Context,
	id string,
	userID int64,
	now, expiresAt time.Time,
	userAgent, ip string,
) error {
	_, err := s.db.ExecContext(
		ctx,
		`INSERT INTO session(id, user_id, created_at, expires_at, last_seen_at, user_agent, ip)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		id,
		userID,
		now.UTC().Format(time.RFC3339),
		expiresAt.UTC().Format(time.RFC3339),
		now.UTC().Format(time.RFC3339),
		userAgent,
		ip,
	)
	if err != nil {
		return fmt.Errorf("insert session: %w", err)
	}
	return nil
}

func (s *Store) SessionByID(ctx context.Context, id string, now time.Time) (Session, error) {
	var session Session
	var expiresAt, lastSeenAt string
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, user_id, expires_at, last_seen_at
		 FROM session
		 WHERE id = ? AND expires_at > ?`,
		id,
		now.UTC().Format(time.RFC3339),
	).Scan(&session.ID, &session.UserID, &expiresAt, &lastSeenAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}

	session.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session expiry: %w", err)
	}
	session.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session last seen: %w", err)
	}
	return session, nil
}

func (s *Store) TouchSession(ctx context.Context, id string, now, expiresAt time.Time) error {
	result, err := s.db.ExecContext(
		ctx,
		"UPDATE session SET last_seen_at = ?, expires_at = ? WHERE id = ?",
		now.UTC().Format(time.RFC3339),
		expiresAt.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) DeleteSession(ctx context.Context, id string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM session WHERE id = ?", id)
	return err
}
