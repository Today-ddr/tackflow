package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

const sessionTokenBytes = 32

// NewSessionToken returns the cookie value and the database session ID.
func NewSessionToken(secret []byte) (cookieValue, sessionID string, err error) {
	if len(secret) != sessionTokenBytes {
		return "", "", fmt.Errorf("session secret must be 32 bytes")
	}

	raw := make([]byte, sessionTokenBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate session token: %w", err)
	}
	rawText := base64.RawURLEncoding.EncodeToString(raw)
	return rawText + "." + signSessionToken(secret, rawText), sessionIDHash(rawText), nil
}

// ParseSessionToken verifies the cookie signature and returns the DB session ID.
func ParseSessionToken(secret []byte, cookieValue string) (string, error) {
	if len(secret) != sessionTokenBytes {
		return "", fmt.Errorf("session secret must be 32 bytes")
	}
	rawText, signature, ok := strings.Cut(cookieValue, ".")
	if !ok || rawText == "" || signature == "" || strings.Contains(signature, ".") {
		return "", fmt.Errorf("invalid session token")
	}
	if _, err := base64.RawURLEncoding.DecodeString(rawText); err != nil {
		return "", fmt.Errorf("invalid session token: %w", err)
	}
	if !hmac.Equal([]byte(signature), []byte(signSessionToken(secret, rawText))) {
		return "", fmt.Errorf("invalid session token signature")
	}
	return sessionIDHash(rawText), nil
}

func signSessionToken(secret []byte, rawText string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(rawText))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func sessionIDHash(rawText string) string {
	sum := sha256.Sum256([]byte(rawText))
	return hex.EncodeToString(sum[:])
}

// CookieName avoids the __Host- prefix when local HTTP cannot satisfy its Secure requirement.
func CookieName(secure bool) string {
	if secure {
		return "__Host-tack_session"
	}
	return "tack_session"
}
