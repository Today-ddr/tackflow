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
	CreatedAt  time.Time
	ExpiresAt  time.Time
	LastSeenAt time.Time
	SudoUntil  *time.Time
	UserAgent  string
	IP         string
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
	var createdAt, expiresAt, lastSeenAt string
	var sudoUntil sql.NullString
	var userAgent, ip sql.NullString
	err := s.db.QueryRowContext(
		ctx,
		`SELECT id, user_id, created_at, expires_at, last_seen_at, sudo_until, user_agent, ip
		 FROM session
		 WHERE id = ? AND expires_at > ?`,
		id,
		now.UTC().Format(time.RFC3339),
	).Scan(
		&session.ID,
		&session.UserID,
		&createdAt,
		&expiresAt,
		&lastSeenAt,
		&sudoUntil,
		&userAgent,
		&ip,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Session{}, ErrNotFound
	}
	if err != nil {
		return Session{}, err
	}

	session.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session created: %w", err)
	}
	session.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session expiry: %w", err)
	}
	session.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session last seen: %w", err)
	}
	if sudoUntil.Valid {
		parsed, err := time.Parse(time.RFC3339, sudoUntil.String)
		if err != nil {
			return Session{}, fmt.Errorf("parse session sudo: %w", err)
		}
		session.SudoUntil = &parsed
	}
	session.UserAgent = userAgent.String
	session.IP = ip.String
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

func (s *Store) DeleteSessionByID(ctx context.Context, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM session WHERE id = ?", id)
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

func (s *Store) SetSessionSudo(ctx context.Context, id string, until time.Time) error {
	result, err := s.db.ExecContext(
		ctx,
		"UPDATE session SET sudo_until = ? WHERE id = ?",
		until.UTC().Format(time.RFC3339),
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

func (s *Store) ListSessions(ctx context.Context, userID int64, now time.Time) ([]Session, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, created_at, expires_at, last_seen_at, sudo_until, user_agent, ip
		FROM session
		WHERE user_id = ? AND expires_at > ?
		ORDER BY last_seen_at DESC, id
	`, userID, now.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer func() { _ = rows.Close() }()

	sessions := make([]Session, 0)
	for rows.Next() {
		session, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		sessions = append(sessions, session)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sessions: %w", err)
	}
	return sessions, nil
}

func (s *Store) DeleteOtherSessions(ctx context.Context, userID int64, currentID string) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM session WHERE user_id = ? AND id <> ?", userID, currentID)
	return err
}

func (s *Store) DeleteAllSessions(ctx context.Context, userID int64) error {
	_, err := s.db.ExecContext(ctx, "DELETE FROM session WHERE user_id = ?", userID)
	return err
}

type sessionScanner interface {
	Scan(dest ...any) error
}

func scanSession(scanner sessionScanner) (Session, error) {
	var session Session
	var createdAt, expiresAt, lastSeenAt string
	var sudoUntil, userAgent, ip sql.NullString
	if err := scanner.Scan(
		&session.ID,
		&session.UserID,
		&createdAt,
		&expiresAt,
		&lastSeenAt,
		&sudoUntil,
		&userAgent,
		&ip,
	); err != nil {
		return Session{}, err
	}
	var err error
	session.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session created: %w", err)
	}
	session.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session expires: %w", err)
	}
	session.LastSeenAt, err = time.Parse(time.RFC3339, lastSeenAt)
	if err != nil {
		return Session{}, fmt.Errorf("parse session last seen: %w", err)
	}
	if sudoUntil.Valid {
		parsed, err := time.Parse(time.RFC3339, sudoUntil.String)
		if err != nil {
			return Session{}, fmt.Errorf("parse session sudo: %w", err)
		}
		session.SudoUntil = &parsed
	}
	session.UserAgent = userAgent.String
	session.IP = ip.String
	return session, nil
}
