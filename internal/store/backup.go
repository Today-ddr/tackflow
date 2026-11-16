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
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("prepare backup file: %w", err)
	}
	if err := file.Close(); err != nil {
		return err
	}
	escaped := strings.ReplaceAll(destination, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("vacuum into backup: %w", err)
	}
	return nil
}
