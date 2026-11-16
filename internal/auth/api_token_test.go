package auth

import (
	"strings"
	"testing"
)

func TestNewAPIToken(t *testing.T) {
	t.Parallel()

	token, prefix, hash, err := NewAPIToken()
	if err != nil {
		t.Fatalf("NewAPIToken: %v", err)
	}
	if !strings.HasPrefix(token, "tk_") || len(token) < 20 {
		t.Fatalf("token = %q", token)
	}
	if prefix != token[:11] {
		t.Fatalf("prefix = %q, token = %q", prefix, token)
	}
	if len(hash) != 64 || hash != HashAPIToken(token) {
		t.Fatalf("hash = %q", hash)
	}
}

func TestValidAPITokenScopes(t *testing.T) {
	t.Parallel()

	if !ValidAPITokenScopes([]string{ScopeCapture, ScopeRead}) {
		t.Fatal("valid scopes rejected")
	}
	if ValidAPITokenScopes(nil) {
		t.Fatal("empty scopes accepted")
	}
	if ValidAPITokenScopes([]string{"admin"}) {
		t.Fatal("unknown scope accepted")
	}
	if ValidAPITokenScopes([]string{ScopeRead, ScopeRead}) {
		t.Fatal("duplicate scope accepted")
	}
}

func TestHasScope(t *testing.T) {
	t.Parallel()

	scopes := []string{ScopeRead}
	if !HasScope(scopes, ScopeRead) {
		t.Fatal("read scope missing")
	}
	if HasScope(scopes, ScopeWrite) {
		t.Fatal("write scope unexpectedly granted")
	}
	if !HasScope([]string{ScopeWrite}, ScopeCapture) {
		t.Fatal("write should imply capture")
	}
}

func TestBearerToken(t *testing.T) {
	t.Parallel()

	token, ok := BearerToken("Bearer tk_abc")
	if !ok || token != "tk_abc" {
		t.Fatalf("BearerToken = %q, %v", token, ok)
	}
	if _, ok := BearerToken("Basic tk_abc"); ok {
		t.Fatal("basic scheme accepted")
	}
}
