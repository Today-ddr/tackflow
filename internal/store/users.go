package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

type User struct {
	ID           int64
	Username     string
	PasswordHash string
}

// BootstrapAdmin creates the single user only when the database has no user yet.
func (s *Store) BootstrapAdmin(ctx context.Context, username, passwordHash string, now time.Time) (bool, error) {
	username = strings.TrimSpace(username)
	if username == "" || passwordHash == "" {
		return false, fmt.Errorf("admin username and password hash are required")
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()

	var count int
	if err := tx.QueryRowContext(ctx, "SELECT COUNT(*) FROM user").Scan(&count); err != nil {
		return false, err
	}
	if count > 0 {
		return false, tx.Commit()
	}

	timestamp := now.UTC().Format(time.RFC3339)
	if _, err := tx.ExecContext(
		ctx,
		"INSERT INTO user(id, username, password_hash, created_at, updated_at) VALUES (1, ?, ?, ?, ?)",
		username, passwordHash, timestamp, timestamp,
	); err != nil {
		return false, err
	}
	return true, tx.Commit()
}

func (s *Store) UserByUsername(ctx context.Context, username string) (User, error) {
	var user User
	err := s.db.QueryRowContext(
		ctx,
		"SELECT id, username, password_hash FROM user WHERE username = ?",
		username,
	).Scan(&user.ID, &user.Username, &user.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}

func (s *Store) UserByID(ctx context.Context, id int64) (User, error) {
	var user User
	err := s.db.QueryRowContext(
		ctx,
		"SELECT id, username, password_hash FROM user WHERE id = ?",
		id,
	).Scan(&user.ID, &user.Username, &user.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNotFound
	}
	if err != nil {
		return User{}, err
	}
	return user, nil
}
