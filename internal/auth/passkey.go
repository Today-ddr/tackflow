package auth

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
)

const PasskeyChallengeTTL = 5 * time.Minute

type PasskeyUser struct {
	ID          int64
	Username    string
	Credentials []webauthn.Credential
}

func (user PasskeyUser) WebAuthnID() []byte {
	return binary.BigEndian.AppendUint64(nil, uint64(user.ID))
}

func (user PasskeyUser) WebAuthnName() string {
	return user.Username
}

func (user PasskeyUser) WebAuthnDisplayName() string {
	return user.Username
}

func (user PasskeyUser) WebAuthnCredentials() []webauthn.Credential {
	return user.Credentials
}

func NewPasskey(publicURL string) (*webauthn.WebAuthn, error) {
	parsed, err := url.Parse(publicURL)
	if err != nil || parsed.Hostname() == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("invalid passkey public URL")
	}
	hostname := strings.ToLower(parsed.Hostname())
	if parsed.Scheme != "https" && (parsed.Scheme != "http" || hostname != "localhost") {
		return nil, fmt.Errorf("passkeys require HTTPS or HTTP localhost")
	}
	return webauthn.New(&webauthn.Config{
		RPID: hostname, RPDisplayName: "Tack",
		RPOrigins: []string{parsed.Scheme + "://" + strings.ToLower(parsed.Host)},
		AuthenticatorSelection: protocol.AuthenticatorSelection{
			ResidentKey: protocol.ResidentKeyRequirementPreferred, UserVerification: protocol.VerificationRequired,
		},
		AttestationPreference: protocol.PreferNoAttestation,
		Timeouts: webauthn.TimeoutsConfig{
			Login:        webauthn.TimeoutConfig{Timeout: PasskeyChallengeTTL},
			Registration: webauthn.TimeoutConfig{Timeout: PasskeyChallengeTTL},
		},
	})
}

func NewPasskeyToken() (string, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(random), nil
}

func ValidatePasskeyLogin(relyingParty *webauthn.WebAuthn, user PasskeyUser, session webauthn.SessionData, proof []byte) (*webauthn.Credential, error) {
	parsed, err := protocol.ParseCredentialRequestResponseBytes(proof)
	if err != nil {
		return nil, err
	}
	credential, err := relyingParty.ValidateLogin(user, session, parsed)
	if err != nil {
		return nil, err
	}
	if credential.Authenticator.CloneWarning && !credential.Flags.BackupEligible {
		return nil, fmt.Errorf("passkey counter did not advance")
	}
	return credential, nil
}

func PasskeyCookieName(secure bool) string {
	if secure {
		return "__Host-tack_passkey"
	}
	return "tack_passkey"
}
