package main

import (
	"bytes"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunRejectsUnknownCommand(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	code := run([]string{"unknown"}, mapGetenv(nil), &output)
	if code == 0 {
		t.Fatal("run(unknown) = 0, want non-zero")
	}
	if !strings.Contains(output.String(), "usage:") {
		t.Errorf("output = %q, want usage text", output.String())
	}
}

func TestHealthcheck(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			t.Errorf("healthcheck path = %q, want /healthz", r.URL.Path)
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	}))
	defer server.Close()

	addr := strings.TrimPrefix(server.URL, "http://")
	var output bytes.Buffer
	code := run([]string{"healthcheck"}, mapGetenv(map[string]string{
		"TACK_ADDR": addr,
	}), &output)
	if code != 0 {
		t.Fatalf("run(healthcheck) = %d, output = %q", code, output.String())
	}
}

func TestHealthcheckReportsUnavailableServer(t *testing.T) {
	t.Parallel()

	var output bytes.Buffer
	code := run([]string{"healthcheck"}, mapGetenv(map[string]string{
		"TACK_ADDR": "127.0.0.1:1",
	}), &output)
	if code == 0 {
		t.Fatalf("run(healthcheck) = 0, want non-zero")
	}
}

func TestRunMigrateAndBackup(t *testing.T) {
	t.Parallel()

	dataDir := t.TempDir()
	env := mapGetenv(map[string]string{
		"TACK_DATA_DIR":       dataDir,
		"TACK_PUBLIC_URL":     "http://localhost:8080",
		"TACK_SESSION_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{4}, 32)),
	})
	var output bytes.Buffer
	if code := run([]string{"migrate"}, env, &output); code != 0 {
		t.Fatalf("run(migrate) = %d, output = %q", code, output.String())
	}
	if _, err := os.Stat(filepath.Join(dataDir, "tack.db")); err != nil {
		t.Fatalf("database not created: %v", err)
	}

	output.Reset()
	if code := run([]string{"backup"}, env, &output); code != 0 {
		t.Fatalf("run(backup) = %d, output = %q", code, output.String())
	}
	matches, err := filepath.Glob(filepath.Join(dataDir, "backups", "tack-*.db"))
	if err != nil {
		t.Fatalf("glob backup: %v", err)
	}
	if len(matches) != 1 {
		t.Fatalf("backup matches = %v, want one file", matches)
	}
	if info, err := os.Stat(matches[0]); err != nil || info.Size() == 0 {
		t.Fatalf("backup file is empty or missing: info=%v err=%v", info, err)
	}
}

func mapGetenv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}
