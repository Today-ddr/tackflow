package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/today/tack/internal/tasks"
)

var (
	ErrTagExists  = errors.New("tag already exists")
	ErrInvalidTag = errors.New("invalid tag")
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

func (s *Store) UpdateTag(ctx context.Context, id int64, name, color *string) (Tag, error) {
	if name != nil {
		normalized := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(*name), "#"))
		if normalized == "" || len([]rune(normalized)) > 80 {
			return Tag{}, ErrInvalidTag
		}
		name = &normalized
	}
	if color != nil {
		normalized := strings.TrimSpace(*color)
		if !tasks.ValidColor(normalized) {
			return Tag{}, ErrInvalidTag
		}
		color = &normalized
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE tag SET name = COALESCE(?, name), color = COALESCE(?, color)
		WHERE id = ? AND NOT EXISTS (SELECT 1 FROM tag duplicate WHERE duplicate.name = ? AND duplicate.id <> ?)
	`, name, color, id, name, id)
	if err != nil {
		return Tag{}, fmt.Errorf("update tag: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Tag{}, err
	}
	if affected == 0 {
		if _, err := s.TagByID(ctx, id); err != nil {
			return Tag{}, err
		}
		return Tag{}, ErrTagExists
	}
	return s.TagByID(ctx, id)
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
