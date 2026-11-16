package config

import (
	"bytes"
	"encoding/base64"
	"strings"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Parallel()

	secret := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32))
	cfg, err := Load(mapEnv(map[string]string{
		"TACK_PUBLIC_URL":     "https://tack.example.com/",
		"TACK_SESSION_SECRET": secret,
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if cfg.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Addr, ":8080")
	}
	if cfg.PublicURL != "https://tack.example.com" {
		t.Errorf("PublicURL = %q", cfg.PublicURL)
	}
	if cfg.DataDir != "./data" {
		t.Errorf("DataDir = %q, want %q", cfg.DataDir, "./data")
	}
	if cfg.AdminUsername != "admin" {
		t.Errorf("AdminUsername = %q, want %q", cfg.AdminUsername, "admin")
	}
	if !cfg.CookieSecure {
		t.Error("CookieSecure = false, want true for HTTPS")
	}
	if cfg.SessionTTL != 30*24*time.Hour {
		t.Errorf("SessionTTL = %v, want %v", cfg.SessionTTL, 30*24*time.Hour)
	}
	if cfg.TimezoneName != "Asia/Shanghai" {
		t.Errorf("TimezoneName = %q, want Asia/Shanghai", cfg.TimezoneName)
	}
}

func TestLoadRequiresPublicURL(t *testing.T) {
	t.Parallel()

	_, err := Load(mapEnv(map[string]string{
		"TACK_SESSION_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	}))
	if err == nil || !strings.Contains(err.Error(), "TACK_PUBLIC_URL") {
		t.Fatalf("Load() error = %v, want TACK_PUBLIC_URL error", err)
	}
}

func TestLoadRejectsInvalidPublicURL(t *testing.T) {
	t.Parallel()

	tests := []string{
		"ftp://tack.example.com",
		"https://",
		"not-a-url",
	}
	for _, publicURL := range tests {
		publicURL := publicURL
		t.Run(publicURL, func(t *testing.T) {
			t.Parallel()

			_, err := Load(mapEnv(map[string]string{
				"TACK_PUBLIC_URL":     publicURL,
				"TACK_SESSION_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
			}))
			if err == nil {
				t.Fatalf("Load() error = nil, want invalid URL error")
			}
		})
	}
}

func TestLoadUsesInsecureCookieForHTTP(t *testing.T) {
	t.Parallel()

	cfg, err := Load(mapEnv(map[string]string{
		"TACK_PUBLIC_URL":     "http://localhost:8080",
		"TACK_SESSION_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
	}))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if cfg.CookieSecure {
		t.Error("CookieSecure = true, want false for HTTP")
	}
}

func TestLoadRejectsWrongSecretLength(t *testing.T) {
	t.Parallel()

	_, err := Load(mapEnv(map[string]string{
		"TACK_PUBLIC_URL":     "https://tack.example.com",
		"TACK_SESSION_SECRET": base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 31)),
	}))
	if err == nil || !strings.Contains(err.Error(), "32 bytes") {
		t.Fatalf("Load() error = %v, want 32-byte secret error", err)
	}
}

func mapEnv(values map[string]string) func(string) string {
	return func(key string) string {
		return values[key]
	}
}

func TestWebhookPrivateTargetsRequireExplicitOptIn(t *testing.T) {
	for _, value := range []string{"", "false", "true", "invalid"} {
		cfg, err := Load(mapEnv(map[string]string{
			"TACK_PUBLIC_URL":            "http://localhost:8080",
			"TACK_SESSION_SECRET":        base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{7}, 32)),
			"TACK_WEBHOOK_ALLOW_PRIVATE": value,
		}))
		if value == "invalid" {
			if err == nil {
				t.Fatal("invalid private-target flag accepted")
			}
			continue
		}
		if err != nil || cfg.WebhookAllowPrivate != (value == "true") {
			t.Fatalf("private-target flag %q = %v, %v", value, cfg.WebhookAllowPrivate, err)
		}
	}
}
