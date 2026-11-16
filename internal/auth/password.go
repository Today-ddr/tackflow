package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

const (
	argonTime    uint32 = 3
	argonMemory  uint32 = 64 * 1024
	argonThreads uint8  = 2
	argonKeyLen  uint32 = 32
	argonSaltLen        = 16
	phcAlgorithm        = "argon2id"
)

var weakPasswords = map[string]struct{}{
	"123456789012": {},
	"password1234": {},
	"adminadmin12": {},
	"qwertyuiop12": {},
	"letmein12345": {},
	"tacktacktack": {},
}

// ValidatePassword enforces the project's length and weak-password policy.
func ValidatePassword(password string) error {
	if len([]rune(password)) < 12 {
		return fmt.Errorf("password must be at least 12 characters")
	}
	if len([]rune(password)) > 1024 {
		return fmt.Errorf("password must be at most 1024 characters")
	}
	if _, weak := weakPasswords[strings.ToLower(strings.TrimSpace(password))]; weak {
		return fmt.Errorf("password is too common")
	}
	return nil
}

// HashPassword produces an Argon2id PHC string using the project parameters.
func HashPassword(password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate password salt: %w", err)
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return fmt.Sprintf(
		"$%s$v=19$m=%d,t=%d,p=%d$%s$%s",
		phcAlgorithm,
		argonMemory,
		argonTime,
		argonThreads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(hash),
	), nil
}

// VerifyPassword checks a password against an Argon2id PHC string.
func VerifyPassword(encoded, password string) (bool, error) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != phcAlgorithm || parts[2] != "v=19" {
		return false, fmt.Errorf("invalid password hash")
	}

	memory, iterations, parallelism, err := parsePHCParams(parts[3])
	if err != nil {
		return false, err
	}
	if memory != argonMemory || iterations != argonTime || parallelism != argonThreads {
		return false, fmt.Errorf("unsupported password hash parameters")
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil || len(salt) != argonSaltLen {
		return false, fmt.Errorf("invalid password salt")
	}
	expected, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil || len(expected) != int(argonKeyLen) {
		return false, fmt.Errorf("invalid password hash")
	}

	actual := argon2.IDKey([]byte(password), salt, iterations, memory, parallelism, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1, nil
}

func parsePHCParams(value string) (uint32, uint32, uint8, error) {
	var memory, iterations uint32
	var parallelism uint8
	parts := strings.Split(value, ",")
	if len(parts) != 3 {
		return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
	}
	for _, part := range parts {
		key, rawValue, ok := strings.Cut(part, "=")
		if !ok {
			return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
		}
		switch key {
		case "m":
			parsed, err := strconv.ParseUint(rawValue, 10, 32)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
			}
			memory = uint32(parsed)
		case "t":
			parsed, err := strconv.ParseUint(rawValue, 10, 32)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
			}
			iterations = uint32(parsed)
		case "p":
			parsed, err := strconv.ParseUint(rawValue, 10, 8)
			if err != nil {
				return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
			}
			parallelism = uint8(parsed)
		default:
			return 0, 0, 0, fmt.Errorf("invalid password hash parameters")
		}
	}
	return memory, iterations, parallelism, nil
}
