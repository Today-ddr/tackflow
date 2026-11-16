package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Project struct {
	ID         int64
	Name       string
	Color      string
	Position   float64
	ArchivedAt *time.Time
	CreatedAt  time.Time
	TaskCount  int
}

func (s *Store) ListProjects(ctx context.Context, includeArchived bool) ([]Project, error) {
	query := `
		SELECT p.id, p.name, p.color, p.position, p.archived_at, p.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM project p
		LEFT JOIN task t ON t.project_id = p.id
	`
	if !includeArchived {
		query += " WHERE p.archived_at IS NULL"
	}
	query += " GROUP BY p.id ORDER BY p.position, p.id"

	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("list projects: %w", err)
	}
	defer func() { _ = rows.Close() }()

	projects := make([]Project, 0)
	for rows.Next() {
		project, err := scanProject(rows)
		if err != nil {
			return nil, err
		}
		projects = append(projects, project)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate projects: %w", err)
	}
	return projects, nil
}

func (s *Store) ProjectByID(ctx context.Context, id int64) (Project, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.color, p.position, p.archived_at, p.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM project p
		LEFT JOIN task t ON t.project_id = p.id
		WHERE p.id = ?
		GROUP BY p.id
	`, id)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) ProjectByName(ctx context.Context, name string) (Project, error) {
	row := s.db.QueryRowContext(ctx, `
		SELECT p.id, p.name, p.color, p.position, p.archived_at, p.created_at,
		       COALESCE(SUM(CASE WHEN t.deleted_at IS NULL THEN 1 ELSE 0 END), 0)
		FROM project p
		LEFT JOIN task t ON t.project_id = p.id
		WHERE p.name = ?
		GROUP BY p.id
	`, name)
	project, err := scanProject(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Project{}, ErrNotFound
	}
	if err != nil {
		return Project{}, err
	}
	return project, nil
}

func (s *Store) CreateProject(ctx context.Context, name, color string, now time.Time) (Project, error) {
	row := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(position), 0) + 1024 FROM project")
	var position float64
	if err := row.Scan(&position); err != nil {
		return Project{}, fmt.Errorf("next project position: %w", err)
	}

	result, err := s.db.ExecContext(
		ctx,
		"INSERT INTO project(name, color, position, created_at) VALUES (?, ?, ?, ?)",
		name, color, position, now.UTC().Format(time.RFC3339),
	)
	if err != nil {
		return Project{}, fmt.Errorf("insert project: %w", err)
	}
	id, err := result.LastInsertId()
	if err != nil {
		return Project{}, err
	}
	return s.ProjectByID(ctx, id)
}

func (s *Store) UpdateProject(
	ctx context.Context,
	id int64,
	name, color *string,
	archived *bool,
	now time.Time,
) (Project, error) {
	project, err := s.ProjectByID(ctx, id)
	if err != nil {
		return Project{}, err
	}
	if name != nil {
		project.Name = *name
	}
	if color != nil {
		project.Color = *color
	}
	if archived != nil {
		if *archived {
			project.ArchivedAt = ptrTime(now.UTC())
		} else {
			project.ArchivedAt = nil
		}
	}

	var archivedAt any
	if project.ArchivedAt != nil {
		archivedAt = project.ArchivedAt.UTC().Format(time.RFC3339)
	}
	if _, err := s.db.ExecContext(
		ctx,
		"UPDATE project SET name = ?, color = ?, archived_at = ? WHERE id = ?",
		project.Name, project.Color, archivedAt, id,
	); err != nil {
		return Project{}, fmt.Errorf("update project: %w", err)
	}
	return s.ProjectByID(ctx, id)
}

func (s *Store) ArchiveProject(ctx context.Context, id int64, now time.Time) error {
	result, err := s.db.ExecContext(
		ctx,
		"UPDATE project SET archived_at = ? WHERE id = ? AND archived_at IS NULL",
		now.UTC().Format(time.RFC3339),
		id,
	)
	if err != nil {
		return fmt.Errorf("archive project: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected == 0 {
		if _, err := s.ProjectByID(ctx, id); err != nil {
			return err
		}
	}
	return nil
}

type projectScanner interface {
	Scan(dest ...any) error
}

func scanProject(scanner projectScanner) (Project, error) {
	var project Project
	var archivedAt sql.NullString
	var createdAt string
	if err := scanner.Scan(
		&project.ID,
		&project.Name,
		&project.Color,
		&project.Position,
		&archivedAt,
		&createdAt,
		&project.TaskCount,
	); err != nil {
		return Project{}, err
	}
	var err error
	project.CreatedAt, err = time.Parse(time.RFC3339, createdAt)
	if err != nil {
		return Project{}, fmt.Errorf("parse project created_at: %w", err)
	}
	if archivedAt.Valid {
		parsed, err := time.Parse(time.RFC3339, archivedAt.String)
		if err != nil {
			return Project{}, fmt.Errorf("parse project archived_at: %w", err)
		}
		project.ArchivedAt = &parsed
	}
	return project, nil
}

func ptrTime(value time.Time) *time.Time {
	return &value
}
