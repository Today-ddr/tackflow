package store

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Backup writes a consistent copy without stopping the application.
func (s *Store) Backup(ctx context.Context, destination string) error {
	if err := os.MkdirAll(filepath.Dir(destination), 0o700); err != nil {
		return fmt.Errorf("create backup directory: %w", err)
	}
	escaped := strings.ReplaceAll(destination, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return fmt.Errorf("vacuum into backup: %w", err)
	}
	return nil
}
