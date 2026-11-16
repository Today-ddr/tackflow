package auth

import (
	"strings"
	"testing"
)

const testPassword = "correct horse battery staple"

func TestHashPasswordAndVerify(t *testing.T) {
	t.Parallel()

	encoded, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("HashPassword() = %q, want argon2id PHC prefix", encoded)
	}

	ok, err := VerifyPassword(encoded, testPassword)
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if !ok {
		t.Fatal("VerifyPassword() = false, want true")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	t.Parallel()

	encoded, err := HashPassword(testPassword)
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}

	ok, err := VerifyPassword(encoded, "not the password")
	if err != nil {
		t.Fatalf("VerifyPassword() error = %v", err)
	}
	if ok {
		t.Fatal("VerifyPassword() = true, want false")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	t.Parallel()

	for _, encoded := range []string{
		"",
		"$argon2id$v=19$m=65536,t=3,p=2$bad",
		"$argon2id$v=19$m=65536,t=3,p=2$not-base64$not-base64",
		"$argon2i$v=19$m=65536,t=3,p=2$c2FsdA$aGFzaA",
	} {
		if _, err := VerifyPassword(encoded, testPassword); err == nil {
			t.Errorf("VerifyPassword(%q) error = nil, want error", encoded)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	t.Parallel()

	if err := ValidatePassword(testPassword); err != nil {
		t.Fatalf("valid password rejected: %v", err)
	}
	if err := ValidatePassword("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if err := ValidatePassword("password1234"); err == nil {
		t.Fatal("common password accepted")
	}
	if err := ValidatePassword(strings.Repeat("x", 1025)); err == nil {
		t.Fatal("long password accepted")
	}
}
