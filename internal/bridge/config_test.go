package bridge

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testEnvironment(t *testing.T) map[string]string {
	t.Helper()
	return map[string]string{
		"TACK_URL": "https://tack.example", "TACK_API_TOKEN": "tk_test_secret",
		"CODEG_URL": "http://127.0.0.1:4000", "CODEG_API_TOKEN": "codeg_test_secret",
		"CODEG_PROJECT_ID": "7", "TACK_BRIDGE_WORKER": "bridge-test",
		"TACK_BRIDGE_STATE": filepath.Join(t.TempDir(), "state.json"),
	}
}

func TestConfigRequiresExplicitDestinationsAndCredentials(t *testing.T) {
	for _, name := range []string{"TACK_URL", "TACK_API_TOKEN", "CODEG_URL", "CODEG_API_TOKEN", "CODEG_PROJECT_ID", "TACK_BRIDGE_STATE", "TACK_BRIDGE_WORKER"} {
		t.Run(name, func(t *testing.T) {
			env := testEnvironment(t)
			delete(env, name)
			_, err := LoadConfig(func(key string) string { return env[key] })
			if err == nil || !strings.Contains(err.Error(), name) {
				t.Fatalf("missing %s: %v", name, err)
			}
		})
	}
}

func TestConfigDefaultsAndValidation(t *testing.T) {
	env := testEnvironment(t)
	config, err := LoadConfig(func(key string) string { return env[key] })
	if err != nil || config.ResultStatus != "review" || config.PollInterval != 10*time.Second || config.RequestTimeout != 15*time.Second || config.FolderID != 7 {
		t.Fatalf("defaults: %#v, %v", config, err)
	}
	for _, test := range []struct{ key, value string }{
		{"TACK_URL", "https://user:secret@tack.example"},
		{"TACK_URL", "https://tack.example?token=secret"},
		{"CODEG_URL", "ftp://codeg.example"},
		{"CODEG_URL", "http://codeg.example"},
		{"CODEG_URL", "https://codeg.example/#secret"},
		{"CODEG_PROJECT_ID", "0"}, {"CODEG_PROJECT_ID", "2147483648"},
		{"TACK_BRIDGE_POLL_INTERVAL", "0s"}, {"TACK_BRIDGE_POLL_INTERVAL", "2m"},
		{"TACK_BRIDGE_HTTP_TIMEOUT", "2m"}, {"TACK_BRIDGE_HTTP_TIMEOUT", "bad-secret"},
		{"TACK_BRIDGE_RESULT_STATUS", "todo"}, {"TACK_API_TOKEN", "secret\nheader"},
	} {
		t.Run(test.key+test.value, func(t *testing.T) {
			values := testEnvironment(t)
			values[test.key] = test.value
			_, err := LoadConfig(func(key string) string { return values[key] })
			if err == nil || strings.Contains(err.Error(), "secret") {
				t.Fatalf("unsafe config accepted or leaked: %v", err)
			}
		})
	}
}
