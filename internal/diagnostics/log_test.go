package diagnostics

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLogRotatesPersistsAndFiltersSafeFields(t *testing.T) {
	directory := t.TempDir()
	output := &bytes.Buffer{}
	log, err := Open(directory, output, Options{MaxBytes: 512, MaxFiles: 3, MaxEntries: 8})
	if err != nil {
		t.Fatal(err)
	}
	logger := slog.New(log.Handler())
	for index := range 25 {
		logger.Info("request", "request_id", "req_safe", "method", "GET", "route", "GET /api/v1/tasks/{id}", "status", 200, "duration_ms", index)
	}
	logger.Warn("request", "request_id", "req_failure", "method", "POST", "route", "POST /api/v1/tokens", "status", 422, "error_code", "invalid_scopes", "password", "private-value", "authorization", "tk_private_secret", "error", errors.New("password=private-value authorization: tk_private_secret"))
	entries := log.Entries(Filter{Level: "WARN", RequestID: "req_failure", Limit: 50})
	if len(entries) != 1 || entries[0].ErrorCode != "invalid_scopes" || entries[0].RequestID != "req_failure" {
		t.Fatalf("filtered entries = %+v", entries)
	}
	if len(log.Entries(Filter{Limit: 50})) != 8 {
		t.Fatal("memory retention exceeded or dropped valid entries")
	}
	if err := log.Close(); err != nil {
		t.Fatal(err)
	}
	files, err := filepath.Glob(filepath.Join(directory, "*.jsonl"))
	if err != nil || len(files) > 3 || len(files) < 2 {
		t.Fatalf("rotating files = %v, %v", files, err)
	}
	for _, filename := range files {
		info, err := os.Stat(filename)
		if err != nil || info.Size() > 512 || info.Mode().Perm() != 0600 {
			t.Fatalf("private bounded file = %v, %v", info, err)
		}
		data, err := os.ReadFile(filename)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "private-value") || strings.Contains(string(data), "tk_private_secret") {
			t.Fatal("persistent log revealed sensitive data")
		}
	}
	if strings.Contains(output.String(), "private-value") || strings.Contains(output.String(), "tk_private_secret") {
		t.Fatal("stdout log revealed sensitive data")
	}
	reopened, err := Open(directory, output, Options{MaxBytes: 512, MaxFiles: 3, MaxEntries: 8})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = reopened.Close() }()
	if entries := reopened.Entries(Filter{Level: "WARN", Limit: 10}); len(entries) != 1 || entries[0].RequestID != "req_failure" {
		t.Fatalf("restart lost diagnostic entry: %+v", entries)
	}
	if err := slog.New(reopened.Handler()).Handler().Handle(context.Background(), slog.NewRecord(time.Now(), slog.LevelInfo, "unknown untrusted message tk_private_secret", 0)); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(output.String(), "unknown untrusted") {
		t.Fatal("unknown event text must not enter diagnostics")
	}
}
