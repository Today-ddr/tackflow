package config

import (
	"encoding/base64"
	"fmt"
	"net/url"
	"strings"
	"time"
)

const defaultSessionTTL = 30 * 24 * time.Hour

// Config contains the process-wide settings needed before the HTTP server starts.
type Config struct {
	Addr          string
	PublicURL     string
	DataDir       string
	AdminUsername string
	AdminPassword string
	SessionSecret []byte
	CookieSecure  bool
	SessionTTL    time.Duration
}

// Load reads configuration from environment values without mutating the process.
func Load(getenv func(string) string) (Config, error) {
	publicURL := strings.TrimSpace(getenv("TACK_PUBLIC_URL"))
	if publicURL == "" {
		return Config{}, fmt.Errorf("TACK_PUBLIC_URL is required")
	}

	parsedURL, err := url.Parse(publicURL)
	if err != nil || parsedURL.Host == "" || (parsedURL.Scheme != "http" && parsedURL.Scheme != "https") {
		return Config{}, fmt.Errorf("TACK_PUBLIC_URL must be an absolute http or https URL")
	}

	secretText := strings.TrimSpace(getenv("TACK_SESSION_SECRET"))
	if secretText == "" {
		return Config{}, fmt.Errorf("TACK_SESSION_SECRET is required")
	}
	sessionSecret, err := base64.StdEncoding.DecodeString(secretText)
	if err != nil {
		return Config{}, fmt.Errorf("TACK_SESSION_SECRET must be base64: %w", err)
	}
	if len(sessionSecret) != 32 {
		return Config{}, fmt.Errorf("TACK_SESSION_SECRET must decode to exactly 32 bytes")
	}

	cfg := Config{
		Addr:          valueOrDefault(getenv("TACK_ADDR"), ":8080"),
		PublicURL:     strings.TrimRight(publicURL, "/"),
		DataDir:       valueOrDefault(getenv("TACK_DATA_DIR"), "./data"),
		AdminUsername: valueOrDefault(getenv("TACK_ADMIN_USERNAME"), "admin"),
		AdminPassword: getenv("TACK_ADMIN_PASSWORD"),
		SessionSecret: sessionSecret,
		CookieSecure:  parsedURL.Scheme == "https",
		SessionTTL:    defaultSessionTTL,
	}
	return cfg, nil
}

func valueOrDefault(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return value
}
