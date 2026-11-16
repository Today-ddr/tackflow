package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Tag struct {
	ID        int64
	Name      string
	Color     string
	CreatedAt time.Time
	TaskCount int
}

func (s *Store) ListTags(ctx context.Context) ([]Tag, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT g.id, g.name, g.color, g.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM tag g
		LEFT JOIN task_tag tt ON tt.tag_id = g.id
		LEFT JOIN task t ON t.id = tt.task_id
		GROUP BY g.id
		ORDER BY g.name COLLATE NOCASE, g.id
	`)
	if err != nil {
		return nil, fmt.Errorf("list tags: %w", err)
	}
	defer func() { _ = rows.Close() }()

	tags := make([]Tag, 0)
	for rows.Next() {
		tag, err := scanTag(rows)
		if err != nil {
			return nil, err
		}
		tags = append(tags, tag)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate tags: %w", err)
	}
	return tags, nil
}

func (s *Store) TagByID(ctx context.Context, id int64) (Tag, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT g.id, g.name, g.color, g.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM tag g
		LEFT JOIN task_tag tt ON tt.tag_id = g.id
		LEFT JOIN task t ON t.id = tt.task_id
		WHERE g.id = ?
		GROUP BY g.id
	`, id)
	tag, err := scanTag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, err
	}
	return tag, nil
}

func (s *Store) TagByName(ctx context.Context, name string) (Tag, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT g.id, g.name, g.color, g.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM tag g
		LEFT JOIN task_tag tt ON tt.tag_id = g.id
		LEFT JOIN task t ON t.id = tt.task_id
		WHERE g.name = ?
		GROUP BY g.id
	`, name)
	tag, err := scanTag(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Tag{}, ErrNotFound
	}
	if err != nil {
		return Tag{}, err
	}
	return tag, nil
}

func (s *Store) CreateTag(ctx context.Context, name, color string, now time.Time) (Tag, error) {
	result, err := s.db.ExecContext(
		ctx,
		"INSERT INTO tag(name, color, created_at) VALUES (?, ?, ?)",
		name, color, now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return Tag{}, fmt.Errorf("insert tag: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Tag{}, err
	}
	return s.TagByID(ctx, id)
}

func (s *Store) EnsureTag(ctx context.Context, name, color string, now time.Time) (Tag, bool, error) {
	tag, err := s.TagByName(ctx, name)
	if err == nil {
		return tag, false, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return Tag{}, false, err
	}
	tag, err = s.CreateTag(ctx, name, color, now)
	if err != nil {
		return Tag{}, false, err
	}
	return tag, true, nil
}

func (s *Store) DeleteTag(ctx context.Context, id int64) error {
	result, err := s.db.ExecContext(ctx, "DELETE FROM tag WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("delete tag: %w", err)
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

type tagScanner interface {
	Scan(dest ...any) error
}

func scanTag(scanner tagScanner) (Tag, error) {
	var tag Tag
	var createdAt string
	if err := scanner.Scan(&tag.ID, &tag.Name, &tag.Color, &createdAt, &tag.TaskCount); err != nil {
		return Tag{}, err
	}
	parsed, err := time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Tag{}, fmt.Errorf("parse tag created_at: %w", err)
	}
	tag.CreatedAt = parsed
	return tag, nil
}
