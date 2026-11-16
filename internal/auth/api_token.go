package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
)

const (
	ScopeCapture = "capture"
	ScopeRead    = "read"
	ScopeWrite   = "write"
)

var APITokenScopes = []string{ScopeCapture, ScopeRead, ScopeWrite}

// NewAPIToken returns the plaintext shown once to the user, its display prefix,
// and the SHA-256 hash stored in SQLite.
func NewAPIToken() (token, prefix, hash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", "", fmt.Errorf("generate api token: %w", err)
	}
	token = "tk_" + base64.RawURLEncoding.EncodeToString(raw)
	prefix = token[:11]
	return token, prefix, HashAPIToken(token), nil
}

func HashAPIToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func ValidAPITokenScopes(scopes []string) bool {
	if len(scopes) == 0 {
		return false
	}
	seen := make(map[string]struct{}, len(scopes))
	for _, scope := range scopes {
		if !slices.Contains(APITokenScopes, scope) {
			return false
		}
		if _, ok := seen[scope]; ok {
			return false
		}
		seen[scope] = struct{}{}
	}
	return true
}

func HasScope(scopes []string, required string) bool {
	return slices.Contains(scopes, required) || slices.Contains(scopes, ScopeWrite) && required != ScopeWrite
}

func BearerToken(header string) (string, bool) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") {
		return "", false
	}
	token = strings.TrimSpace(token)
	if !strings.HasPrefix(token, "tk_") {
		return "", false
	}
	return token, true
}
