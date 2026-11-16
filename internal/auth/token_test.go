package auth

import (
	"encoding/hex"
	"strings"
	"testing"
)

func TestSessionTokenRoundTrip(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	cookieValue, sessionID, err := NewSessionToken(secret)
	if err != nil {
		t.Fatalf("NewSessionToken() error = %v", err)
	}
	if strings.Count(cookieValue, ".") != 1 {
		t.Fatalf("cookie value = %q, want one separator", cookieValue)
	}
	if len(sessionID) != 64 {
		t.Fatalf("session ID length = %d, want 64", len(sessionID))
	}
	if _, err := hex.DecodeString(sessionID); err != nil {
		t.Fatalf("session ID is not hex: %v", err)
	}

	parsedID, err := ParseSessionToken(secret, cookieValue)
	if err != nil {
		t.Fatalf("ParseSessionToken() error = %v", err)
	}
	if parsedID != sessionID {
		t.Fatalf("ParseSessionToken() = %q, want %q", parsedID, sessionID)
	}
}

func TestParseSessionTokenRejectsTampering(t *testing.T) {
	t.Parallel()

	secret := []byte("0123456789abcdef0123456789abcdef")
	cookieValue, _, err := NewSessionToken(secret)
	if err != nil {
		t.Fatalf("NewSessionToken() error = %v", err)
	}

	parts := strings.Split(cookieValue, ".")
	parts[1] = strings.Repeat("A", len(parts[1]))
	if _, err := ParseSessionToken(secret, strings.Join(parts, ".")); err == nil {
		t.Fatal("ParseSessionToken(tampered) error = nil, want error")
	}

	if _, err := ParseSessionToken([]byte("abcdef0123456789abcdef0123456789"), cookieValue); err == nil {
		t.Fatal("ParseSessionToken(wrong secret) error = nil, want error")
	}
}

func TestCookieName(t *testing.T) {
	t.Parallel()

	if got := CookieName(true); got != "__Host-tack_session" {
		t.Errorf("CookieName(true) = %q", got)
	}
	if got := CookieName(false); got != "tack_session" {
		t.Errorf("CookieName(false) = %q", got)
	}
}
