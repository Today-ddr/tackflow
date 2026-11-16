package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type APIToken struct {
	ID         int64
	Name       string
	Prefix     string
	Hash       string
	Scopes     []string
	ProjectIDs []int64
	TagIDs     []int64
	CreatedAt  time.Time
	LastUsedAt *time.Time
	RevokedAt  *time.Time
}

type TokenResources struct {
	ProjectIDs []int64
	TagIDs     []int64
}

func (s *Store) CreateAPIToken(
	ctx context.Context,
	name, prefix, hash string,
	scopes []string,
	now time.Time,
	resources ...TokenResources,
) (APIToken, error) {
	scopesJSON, err := json.Marshal(scopes)
	if err != nil {
		return APIToken{}, err
	}
	allowed := TokenResources{ProjectIDs: []int64{}, TagIDs: []int64{}}
	if len(resources) > 0 {
		allowed = resources[0]
	}
	projectJSON, err := json.Marshal(allowed.ProjectIDs)
	if err != nil {
		return APIToken{}, err
	}
	tagJSON, err := json.Marshal(allowed.TagIDs)
	if err != nil {
		return APIToken{}, err
	}
	result, err := s.db.ExecContext(
		ctx,
		`INSERT INTO api_token(name, prefix, hash, scopes, created_at, project_ids, tag_ids)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		name, prefix, hash, string(scopesJSON), now.UTC().Format(time.RFC3339), string(projectJSON), string(tagJSON),
	)
	if err != nil {
		return APIToken{}, fmt.Errorf("insert api token: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return APIToken{}, err
	}
	return s.APITokenByID(ctx, id)
}

func (s *Store) APITokenByID(ctx context.Context, id int64) (APIToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, prefix, hash, scopes, created_at, last_used_at, revoked_at, project_ids, tag_ids
		FROM api_token WHERE id = ?
	`, id)
	token, err := scanAPIToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, err
	}
	return token, nil
}

func (s *Store) APITokenByHash(ctx context.Context, hash string) (APIToken, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT id, name, prefix, hash, scopes, created_at, last_used_at, revoked_at, project_ids, tag_ids
		FROM api_token WHERE hash = ? AND revoked_at IS NULL
	`, hash)
	token, err := scanAPIToken(row)
	if errors.Is(err, sql.ErrNoRows) {
		return APIToken{}, ErrNotFound
	}
	if err != nil {
		return APIToken{}, err
	}
	return token, nil
}

func (s *Store) ListAPITokens(ctx context.Context) ([]APIToken, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, name, prefix, hash, scopes, created_at, last_used_at, revoked_at, project_ids, tag_ids
		FROM api_token
		WHERE revoked_at IS NULL
		ORDER BY created_at DESC, id DESC
	`)
	if err != nil {
		return nil, fmt.Errorf("list api tokens: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tokens := make([]APIToken, 0)
	for rows.Next() {
		token, err := scanAPIToken(rows)
		if err != nil {
			return nil, err
		}
		tokens = append(tokens, token)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate api tokens: %w", err)
	}
	return tokens, nil
}

func (s *Store) TouchAPIToken(ctx context.Context, id int64, now time.Time) error {
	_, err := s.db.ExecContext(
		ctx,
		"UPDATE api_token SET last_used_at = ? WHERE id = ?",
		now.UTC().Format(time.RFC3339),
		id,
	)
	return err
}

func (s *Store) RevokeAPIToken(ctx context.Context, id int64, now time.Time) error {
	result, err := s.db.ExecContext(
		ctx,
		"UPDATE api_token SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL",
		now.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return fmt.Errorf("revoke api token: %w", err)
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

type apiTokenScanner interface {
	Scan(dest ...any) error
}

func scanAPIToken(scanner apiTokenScanner) (APIToken, error) {
	var token APIToken
	var scopesJSON, createdAt string
	var projectsJSON, tagsJSON string
	var lastUsedAt, revokedAt sql.NullString
	if err := scanner.Scan(
		&token.ID,
		&token.Name,
		&token.Prefix,
		&token.Hash,
		&scopesJSON,
		&createdAt,
		&lastUsedAt,
		&revokedAt,
		&projectsJSON,
		&tagsJSON,
	); err != nil {
		return APIToken{}, err
	}
	if err := json.Unmarshal([]byte(scopesJSON), &token.Scopes); err != nil {
		return APIToken{}, fmt.Errorf("parse api token scopes: %w", err)
	}
	if err := json.Unmarshal([]byte(projectsJSON), &token.ProjectIDs); err != nil {
		return APIToken{}, fmt.Errorf("parse api token projects: %w", err)
	}
	if err := json.Unmarshal([]byte(tagsJSON), &token.TagIDs); err != nil {
		return APIToken{}, fmt.Errorf("parse api token tags: %w", err)
	}
	if token.ProjectIDs == nil {
		token.ProjectIDs = []int64{}
	}
	if token.TagIDs == nil {
		token.TagIDs = []int64{}
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return APIToken{}, fmt.Errorf("parse api token created_at: %w", err)
	}
	token.CreatedAt = parsed
	if lastUsedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, lastUsedAt.String)
		if err != nil {
			return APIToken{}, fmt.Errorf("parse api token last_used_at: %w", err)
		}
		token.LastUsedAt = &parsed
	}
	if revokedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, revokedAt.String)
		if err != nil {
			return APIToken{}, fmt.Errorf("parse api token revoked_at: %w", err)
		}
		token.RevokedAt = &parsed
	}
	return token, nil
}
