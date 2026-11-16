package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/go-webauthn/webauthn/webauthn"
)

type Passkey struct {
	ID         string
	UserID     int64
	Name       string
	Credential webauthn.Credential
	Version    int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
}

type PasskeyChallenge struct {
	ID          string
	Purpose     string
	UserID      int64
	SessionID   string
	Binding     string
	Name        string
	SessionData []byte
	ExpiresAt   time.Time
}

func (s *Store) CreatePasskey(ctx context.Context, passkey Passkey) error {
	credentialJSON, err := json.Marshal(passkey.Credential)
	if err != nil {
		return err
	}
	transports, err := json.Marshal(passkey.Credential.Transport)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO passkey(id, user_id, name, credential_id, credential_json, transports, sign_count, created_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, passkey.ID, passkey.UserID, passkey.Name, passkey.Credential.ID, string(credentialJSON), string(transports), passkey.Credential.Authenticator.SignCount, passkey.CreatedAt.UTC().Format(time.RFC3339))
	return err
}

func (s *Store) ListPasskeys(ctx context.Context, userID int64) ([]Passkey, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, user_id, name, credential_json, version, created_at, last_used_at
		FROM passkey WHERE user_id = ? ORDER BY created_at, id
	`, userID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	passkeys := make([]Passkey, 0)
	for rows.Next() {
		var passkey Passkey
		var credentialJSON, createdAt string
		var lastUsedAt sql.NullString
		if err := rows.Scan(&passkey.ID, &passkey.UserID, &passkey.Name, &credentialJSON, &passkey.Version, &createdAt, &lastUsedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(credentialJSON), &passkey.Credential); err != nil {
			return nil, fmt.Errorf("decode passkey credential: %w", err)
		}
		if passkey.CreatedAt, err = time.Parse(time.RFC3339, createdAt); err != nil {
			return nil, err
		}
		if lastUsedAt.Valid {
			parsed, err := time.Parse(time.RFC3339, lastUsedAt.String)
			if err != nil {
				return nil, err
			}
			passkey.LastUsedAt = &parsed
		}
		passkeys = append(passkeys, passkey)
	}
	return passkeys, rows.Err()
}

func (s *Store) UpdatePasskeyUsage(ctx context.Context, previous Passkey, credential webauthn.Credential, now time.Time) error {
	credentialJSON, err := json.Marshal(credential)
	if err != nil {
		return err
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE passkey SET credential_json = ?, sign_count = ?, last_used_at = ?, version = version + 1
		WHERE id = ? AND user_id = ? AND credential_id = ? AND version = ?
	`, string(credentialJSON), credential.Authenticator.SignCount, now.UTC().Format(time.RFC3339), previous.ID, previous.UserID, credential.ID, previous.Version)
	return passkeyAffected(result, err)
}

func (s *Store) DeletePasskey(ctx context.Context, userID int64, id string) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM passkey WHERE id = ? AND user_id = ?", id, userID)
	return passkeyAffected(result, err)
}

func passkeyAffected(result sql.Result, err error) error {
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return ErrNotFound
	}
	return nil
}

func (s *Store) SavePasskeyChallenge(ctx context.Context, challenge PasskeyChallenge, now time.Time) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `DELETE FROM passkey_challenge WHERE expires_at <= ? OR (purpose = ? AND binding = ?)`, now.UTC().Format(time.RFC3339), challenge.Purpose, challenge.Binding); err != nil {
		return err
	}
	var sessionID any
	if challenge.SessionID != "" {
		sessionID = challenge.SessionID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO passkey_challenge(id, purpose, user_id, session_id, binding, name, session_data, expires_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
	`, challenge.ID, challenge.Purpose, challenge.UserID, sessionID, challenge.Binding, challenge.Name, string(challenge.SessionData), challenge.ExpiresAt.UTC().Format(time.RFC3339)); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) ConsumePasskeyChallenge(ctx context.Context, id, purpose, binding string, now time.Time) (PasskeyChallenge, error) {
	var challenge PasskeyChallenge
	var sessionID sql.NullString
	var sessionData, expiresAt string
	err := s.db.QueryRowContext(ctx, `
		DELETE FROM passkey_challenge
		WHERE id = ? AND purpose = ? AND binding = ? AND expires_at > ?
		RETURNING id, purpose, user_id, session_id, binding, name, session_data, expires_at
	`, id, purpose, binding, now.UTC().Format(time.RFC3339)).Scan(
		&challenge.ID, &challenge.Purpose, &challenge.UserID, &sessionID, &challenge.Binding, &challenge.Name, &sessionData, &expiresAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return PasskeyChallenge{}, ErrNotFound
	}
	if err != nil {
		return PasskeyChallenge{}, err
	}
	challenge.SessionID = sessionID.String
	challenge.SessionData = []byte(sessionData)
	challenge.ExpiresAt, err = time.Parse(time.RFC3339, expiresAt)
	return challenge, err
}
