package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRewriteEnvValue(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	if err := os.WriteFile(path, []byte("A=1\nTACK_IMAGE_TAG=v0.1.0\nB=2\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := rewriteEnvValue(path, "TACK_IMAGE_TAG", "v0.2.0"); err != nil {
		t.Fatalf("rewriteEnvValue: %v", err)
	}
	body, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(body); got != "A=1\nTACK_IMAGE_TAG=v0.2.0\nB=2\n" {
		t.Fatalf("env = %q", got)
	}
	if readEnvValue(path, "TACK_IMAGE_TAG") != "v0.2.0" {
		t.Fatal("readEnvValue did not see new tag")
	}
}

func TestReleaseRegexp(t *testing.T) {
	t.Parallel()

	for _, value := range []string{"v0.1.0", "v1.12.3"} {
		if !releaseRegexp.MatchString(value) {
			t.Fatalf("%q rejected", value)
		}
	}
	for _, value := range []string{"latest", "v1.2", "v1.2.3-rc1"} {
		if releaseRegexp.MatchString(value) {
			t.Fatalf("%q accepted", value)
		}
	}
}

func TestRestoreBackupReplacesDatabaseAndRemovesWAL(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	backup := filepath.Join(dir, "backup.db")
	if err := os.WriteFile(backup, []byte("backup contents"), 0o600); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(dir, "data")
	if err := os.MkdirAll(dataDir, 0o700); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dataDir, "tack.db")
	if err := os.WriteFile(target, []byte("old contents"), 0o640); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if err := os.WriteFile(target+suffix, []byte("sidecar"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	if err := restoreBackup(backup, dataDir); err != nil {
		t.Fatalf("restoreBackup: %v", err)
	}
	body, err := os.ReadFile(target)
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != "backup contents" {
		t.Fatalf("restored contents = %q", body)
	}
	info, err := os.Stat(target)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o640 {
		t.Fatalf("restored mode = %o, want 640", info.Mode().Perm())
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(target + suffix); !os.IsNotExist(err) {
			t.Fatalf("sidecar %s still exists: %v", suffix, err)
		}
	}
}
