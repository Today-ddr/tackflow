package api

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/fxamacker/cbor/v2"
	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/config"
	"github.com/today-ddr/tackflow/internal/store"
)

const passkeyTestOrigin = "https://tack.example"

type passkeyHarness struct {
	server  *Server
	handler http.Handler
	now     time.Time
}

type passkeyBeginResult struct {
	ChallengeID string `json:"challenge_id"`
	PublicKey   struct {
		Challenge string `json:"challenge"`
		RP        struct {
			ID string `json:"id"`
		} `json:"rp"`
		RPID string `json:"rpId"`
		User struct {
			ID string `json:"id"`
		} `json:"user"`
		AuthenticatorSelection struct {
			UserVerification string `json:"userVerification"`
			ResidentKey      string `json:"residentKey"`
		} `json:"authenticatorSelection"`
		UserVerification string `json:"userVerification"`
		AllowCredentials []struct {
			ID string `json:"id"`
		} `json:"allowCredentials"`
		ExcludeCredentials []struct {
			ID string `json:"id"`
		} `json:"excludeCredentials"`
	} `json:"publicKey"`
}

type softwarePasskey struct {
	privateKey *ecdsa.PrivateKey
	id         []byte
	userHandle string
}

func newPasskeyHarness(t *testing.T) *passkeyHarness {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	harness := &passkeyHarness{now: time.Now().UTC().Truncate(time.Second)}
	hash, err := auth.HashPassword(testPassword)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.BootstrapAdmin(t.Context(), "admin", hash, harness.now); err != nil {
		t.Fatal(err)
	}
	harness.server = NewServer(config.Config{
		PublicURL: passkeyTestOrigin, CookieSecure: true,
		SessionSecret: bytes.Repeat([]byte{7}, 32), SessionTTL: 30 * 24 * time.Hour,
	}, st, fstest.MapFS{}, BuildInfo{}, slog.New(slog.NewTextHandler(io.Discard, nil)), func() time.Time { return harness.now })
	harness.handler = harness.server.Handler()
	return harness
}

func (harness *passkeyHarness) session(t *testing.T, sudo bool) *http.Cookie {
	t.Helper()
	value, id, err := auth.NewSessionToken(harness.server.cfg.SessionSecret)
	if err != nil {
		t.Fatal(err)
	}
	if err := harness.server.store.CreateSession(t.Context(), id, 1, harness.now, harness.now.Add(time.Hour), "test", "127.0.0.1"); err != nil {
		t.Fatal(err)
	}
	if sudo {
		if err := harness.server.store.SetSessionSudo(t.Context(), id, harness.now.Add(5*time.Minute)); err != nil {
			t.Fatal(err)
		}
	}
	return &http.Cookie{Name: auth.CookieName(true), Value: value}
}

func (harness *passkeyHarness) request(t *testing.T, method, path string, body any, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var payload []byte
	if body != nil {
		var err error
		payload, err = json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
	}
	req := httptest.NewRequest(method, passkeyTestOrigin+path, bytes.NewReader(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Tack-Client", "web")
	req.Header.Set("Origin", passkeyTestOrigin)
	if cookie != nil {
		req.AddCookie(cookie)
	}
	for key, value := range headers {
		req.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, req)
	return response
}

func requirePasskeyStatus(t *testing.T, response *httptest.ResponseRecorder, want int) {
	t.Helper()
	if response.Code != want {
		t.Fatalf("status = %d, want %d: %s", response.Code, want, response.Body.String())
	}
}

func decodePasskeyBegin(t *testing.T, response *httptest.ResponseRecorder) passkeyBeginResult {
	t.Helper()
	requirePasskeyStatus(t, response, http.StatusOK)
	var result passkeyBeginResult
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if result.ChallengeID == "" || result.PublicKey.Challenge == "" {
		t.Fatalf("missing challenge: %s", response.Body.String())
	}
	if response.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("authentication response must not be cached")
	}
	return result
}

func newSoftwarePasskey(t *testing.T) *softwarePasskey {
	t.Helper()
	privateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	id := make([]byte, 32)
	if _, err := rand.Read(id); err != nil {
		t.Fatal(err)
	}
	return &softwarePasskey{privateKey: privateKey, id: id}
}

func passkeyJSON(t *testing.T, value any) []byte {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return payload
}

func (credential *softwarePasskey) proof(t *testing.T, challenge, origin, rpID string, flags byte, counter uint32, registration, forged bool) map[string]any {
	t.Helper()
	ceremony := "webauthn.get"
	if registration {
		ceremony = "webauthn.create"
	}
	clientData := passkeyJSON(t, map[string]any{"type": ceremony, "challenge": challenge, "origin": origin, "crossOrigin": false})
	rpHash := sha256.Sum256([]byte(rpID))
	authData := append([]byte{}, rpHash[:]...)
	authData = append(authData, flags)
	authData = binary.BigEndian.AppendUint32(authData, counter)
	if registration {
		publicKey, err := cbor.Marshal(map[int]any{
			1: 2, 3: -7, -1: 1,
			-2: credential.privateKey.X.FillBytes(make([]byte, 32)),
			-3: credential.privateKey.Y.FillBytes(make([]byte, 32)),
		})
		if err != nil {
			t.Fatal(err)
		}
		authData = append(authData, make([]byte, 16)...)
		authData = binary.BigEndian.AppendUint16(authData, uint16(len(credential.id)))
		authData = append(authData, credential.id...)
		authData = append(authData, publicKey...)
	}
	clientHash := sha256.Sum256(clientData)
	signedData := append(append([]byte{}, authData...), clientHash[:]...)
	digest := sha256.Sum256(signedData)
	signature, err := ecdsa.SignASN1(rand.Reader, credential.privateKey, digest[:])
	if err != nil {
		t.Fatal(err)
	}
	if forged {
		signature[len(signature)-1] ^= 1
	}
	response := map[string]any{"clientDataJSON": base64.RawURLEncoding.EncodeToString(clientData)}
	if registration {
		attestation, err := cbor.Marshal(map[string]any{
			"fmt": "packed", "authData": authData,
			"attStmt": map[string]any{"alg": -7, "sig": signature},
		})
		if err != nil {
			t.Fatal(err)
		}
		response["attestationObject"] = base64.RawURLEncoding.EncodeToString(attestation)
		response["transports"] = []string{"internal"}
	} else {
		response["authenticatorData"] = base64.RawURLEncoding.EncodeToString(authData)
		response["signature"] = base64.RawURLEncoding.EncodeToString(signature)
		response["userHandle"] = credential.userHandle
	}
	return map[string]any{
		"id": base64.RawURLEncoding.EncodeToString(credential.id), "rawId": base64.RawURLEncoding.EncodeToString(credential.id),
		"type": "public-key", "authenticatorAttachment": "platform", "response": response, "clientExtensionResults": map[string]any{},
	}
}

func (harness *passkeyHarness) register(t *testing.T, cookie *http.Cookie, flags byte) (*softwarePasskey, string) {
	t.Helper()
	begin := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "MacBook"}, cookie, nil))
	if begin.PublicKey.RP.ID != "tack.example" || begin.PublicKey.AuthenticatorSelection.UserVerification != "required" || begin.PublicKey.AuthenticatorSelection.ResidentKey != "preferred" {
		t.Fatalf("unsafe registration options: %+v", begin.PublicKey)
	}
	credential := newSoftwarePasskey(t)
	credential.userHandle = begin.PublicKey.User.ID
	response := harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", map[string]any{
		"challenge_id": begin.ChallengeID,
		"credential":   credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", flags, 0, true, false),
	}, cookie, nil)
	requirePasskeyStatus(t, response, http.StatusCreated)
	var result struct {
		Passkey struct {
			ID string `json:"id"`
		} `json:"passkey"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil || result.Passkey.ID == "" {
		t.Fatalf("invalid registration response: %s, %v", response.Body.String(), err)
	}
	return credential, result.Passkey.ID
}

func (harness *passkeyHarness) beginLogin(t *testing.T) (passkeyBeginResult, *http.Cookie) {
	t.Helper()
	response := harness.request(t, http.MethodPost, "/api/v1/auth/passkey/begin", nil, nil, nil)
	begin := decodePasskeyBegin(t, response)
	if begin.PublicKey.RPID != "tack.example" || begin.PublicKey.UserVerification != "required" || len(begin.PublicKey.AllowCredentials) != 1 {
		t.Fatalf("unsafe login options: %+v", begin.PublicKey)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name != auth.CookieName(true) && cookie.MaxAge > 0 {
			if !cookie.HttpOnly || !cookie.Secure || cookie.Path != "/" || cookie.Domain != "" || cookie.SameSite != http.SameSiteStrictMode || cookie.MaxAge != 300 {
				t.Fatalf("unsafe challenge cookie: %+v", cookie)
			}
			return begin, cookie
		}
	}
	t.Fatal("missing browser binding cookie")
	return begin, nil
}

func TestPasskeySignedRoundTripAndPasswordFallback(t *testing.T) {
	harness := newPasskeyHarness(t)
	cookie := harness.session(t, true)
	credential, id := harness.register(t, cookie, 0x45)
	list := harness.request(t, http.MethodGet, "/api/v1/passkeys", nil, cookie, nil)
	requirePasskeyStatus(t, list, http.StatusOK)
	var listed struct {
		Passkeys []map[string]any `json:"passkeys"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Passkeys) != 1 || len(listed.Passkeys[0]) != 4 || listed.Passkeys[0]["id"] != id || listed.Passkeys[0]["name"] != "MacBook" || listed.Passkeys[0]["last_used_at"] != nil {
		t.Fatalf("invalid or leaking metadata: %s", list.Body.String())
	}
	nextRegistration := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "Another"}, cookie, nil))
	if len(nextRegistration.PublicKey.ExcludeCredentials) != 1 || nextRegistration.PublicKey.ExcludeCredentials[0].ID != base64.RawURLEncoding.EncodeToString(credential.id) {
		t.Fatal("registered credential not excluded")
	}
	begin, browserCookie := harness.beginLogin(t)
	if begin.PublicKey.AllowCredentials[0].ID != base64.RawURLEncoding.EncodeToString(credential.id) {
		t.Fatal("login allows incorrect credential")
	}
	proof := map[string]any{"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false)}
	loginResponse := harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", proof, browserCookie, map[string]string{"Idempotency-Key": "same-proof"})
	requirePasskeyStatus(t, loginResponse, http.StatusOK)
	var sessionCookie *http.Cookie
	for _, candidate := range loginResponse.Result().Cookies() {
		if candidate.Name == auth.CookieName(true) {
			sessionCookie = candidate
		}
	}
	if sessionCookie == nil || !strings.Contains(loginResponse.Body.String(), `"username":"admin"`) {
		t.Fatalf("ordinary login session missing: %s", loginResponse.Body.String())
	}
	me := harness.request(t, http.MethodGet, "/api/v1/auth/me", nil, sessionCookie, nil)
	requirePasskeyStatus(t, me, http.StatusOK)
	if !strings.Contains(me.Body.String(), `"auth_kind":"session"`) || !strings.Contains(me.Body.String(), `"sudo_expires_at":null`) {
		t.Fatalf("passkey session must not grant sudo: %s", me.Body.String())
	}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", proof, browserCookie, map[string]string{"Idempotency-Key": "same-proof"}), http.StatusUnauthorized)
	list = harness.request(t, http.MethodGet, "/api/v1/passkeys", nil, cookie, nil)
	if strings.Contains(list.Body.String(), `"last_used_at":null`) {
		t.Fatal("last use not persisted")
	}
	requirePasskeyStatus(t, harness.request(t, http.MethodDelete, "/api/v1/passkeys/"+id, nil, cookie, nil), http.StatusNoContent)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/begin", nil, nil, nil), http.StatusUnauthorized)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/login", map[string]string{"username": "admin", "password": testPassword}, nil, nil), http.StatusOK)
}

func TestPasskeyManagementAuthSudoAndCSRF(t *testing.T) {
	harness := newPasskeyHarness(t)
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/passkeys"},
		{http.MethodPost, "/api/v1/passkeys/register/begin"},
		{http.MethodPost, "/api/v1/passkeys/register/finish"},
		{http.MethodDelete, "/api/v1/passkeys/unknown"},
	} {
		requirePasskeyStatus(t, harness.request(t, endpoint.method, endpoint.path, nil, nil, nil), http.StatusUnauthorized)
	}
	cookie := harness.session(t, false)
	body := map[string]string{"name": "Device"}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", body, cookie, nil), http.StatusForbidden)
	requirePasskeyStatus(t, harness.request(t, http.MethodDelete, "/api/v1/passkeys/unknown", nil, cookie, nil), http.StatusForbidden)
	body["password"] = testPassword
	decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", body, cookie, nil))
	for _, headers := range []map[string]string{{"X-Tack-Client": ""}, {"Origin": "https://evil.example"}, {"Sec-Fetch-Site": "cross-site"}} {
		for _, path := range []string{"/api/v1/passkeys/register/begin", "/api/v1/passkeys/register/finish"} {
			requirePasskeyStatus(t, harness.request(t, http.MethodPost, path, body, cookie, headers), http.StatusForbidden)
		}
		requirePasskeyStatus(t, harness.request(t, http.MethodDelete, "/api/v1/passkeys/unknown", nil, cookie, headers), http.StatusForbidden)
	}
	for _, name := range []string{"", "   ", strings.Repeat("界", 81)} {
		requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": name}, cookie, nil), http.StatusUnprocessableEntity)
	}
	raw, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := harness.server.store.CreateAPIToken(t.Context(), "all", prefix, hash, []string{auth.ScopeRead, auth.ScopeWrite}, harness.now); err != nil {
		t.Fatal(err)
	}
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/passkeys"},
		{http.MethodPost, "/api/v1/passkeys/register/begin"},
		{http.MethodPost, "/api/v1/passkeys/register/finish"},
		{http.MethodDelete, "/api/v1/passkeys/unknown"},
	} {
		requirePasskeyStatus(t, harness.request(t, endpoint.method, endpoint.path, body, nil, map[string]string{"Authorization": "Bearer " + raw}), http.StatusForbidden)
	}
}

func TestPasskeyRegistrationRejectsInvalidProofAndReplay(t *testing.T) {
	harness := newPasskeyHarness(t)
	cookie := harness.session(t, true)
	for _, scenario := range []struct {
		name, origin, rpID string
		flags              byte
		forged             bool
	}{
		{"signature", passkeyTestOrigin, "tack.example", 0x45, true},
		{"verification", passkeyTestOrigin, "tack.example", 0x41, false},
		{"origin", "https://evil.example", "tack.example", 0x45, false},
		{"rp", passkeyTestOrigin, "evil.example", 0x45, false},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			begin := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "Device"}, cookie, nil))
			credential := newSoftwarePasskey(t)
			body := map[string]any{"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, scenario.origin, scenario.rpID, scenario.flags, 0, true, scenario.forged)}
			requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", body, cookie, nil), http.StatusBadRequest)
			body["credential"] = credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false)
			requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", body, cookie, nil), http.StatusBadRequest)
		})
	}
}

func TestPasskeyLoginRejectsInvalidProofAndConsumesChallenge(t *testing.T) {
	harness := newPasskeyHarness(t)
	credential, _ := harness.register(t, harness.session(t, true), 0x45)
	for _, scenario := range []string{"signature", "UV", "UP", "origin", "RPID", "challenge", "user", "unknown", "malformed", "backup-flags"} {
		t.Run(scenario, func(t *testing.T) {
			harness.now = harness.now.Add(time.Minute)
			begin, cookie := harness.beginLogin(t)
			origin, rpID, challenge, flags := passkeyTestOrigin, "tack.example", begin.PublicKey.Challenge, byte(0x05)
			forged := scenario == "signature"
			switch scenario {
			case "UV":
				flags = 0x01
			case "UP":
				flags = 0x04
			case "origin":
				origin = "https://evil.example"
			case "RPID":
				rpID = "evil.example"
			case "challenge":
				challenge = base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{1}, 32))
			case "backup-flags":
				flags = 0x1d
			}
			proof := credential.proof(t, challenge, origin, rpID, flags, 1, false, forged)
			if scenario == "user" {
				proof["response"].(map[string]any)["userHandle"] = base64.RawURLEncoding.EncodeToString([]byte("another-user"))
			}
			if scenario == "unknown" {
				unknown := base64.RawURLEncoding.EncodeToString([]byte("unknown-credential"))
				proof["id"], proof["rawId"] = unknown, unknown
			}
			if scenario == "malformed" {
				proof = map[string]any{"type": "public-key"}
			}
			body := map[string]any{"challenge_id": begin.ChallengeID, "credential": proof}
			failed := harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", body, cookie, nil)
			requirePasskeyStatus(t, failed, http.StatusUnauthorized)
			for _, responseCookie := range failed.Result().Cookies() {
				if responseCookie.Name == auth.CookieName(true) {
					t.Fatal("invalid proof issued a session")
				}
			}
			body["credential"] = credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false)
			requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", body, cookie, nil), http.StatusUnauthorized)
		})
	}
}

func TestPasskeyChallengePurposeSessionBrowserAndExpiry(t *testing.T) {
	harness := newPasskeyHarness(t)
	owner := harness.session(t, true)
	otherSession := harness.session(t, true)
	credential, _ := harness.register(t, owner, 0x45)
	registration := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "second"}, owner, nil))
	secondCredential := newSoftwarePasskey(t)
	registrationBody := map[string]any{"challenge_id": registration.ChallengeID, "credential": secondCredential.proof(t, registration.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false)}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", registrationBody, otherSession, nil), http.StatusBadRequest)
	loginBegin, browserCookie := harness.beginLogin(t)
	loginBody := map[string]any{"challenge_id": loginBegin.ChallengeID, "credential": credential.proof(t, loginBegin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false)}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", registrationBody, browserCookie, nil), http.StatusUnauthorized)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", loginBody, owner, nil), http.StatusBadRequest)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", loginBody, nil, nil), http.StatusUnauthorized)
	_, differentBrowser := harness.beginLogin(t)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", loginBody, differentBrowser, nil), http.StatusUnauthorized)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", loginBody, browserCookie, nil), http.StatusOK)
	harness.now = harness.now.Add(5 * time.Minute)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", registrationBody, owner, nil), http.StatusBadRequest)
	begin, cookie := harness.beginLogin(t)
	harness.now = harness.now.Add(5 * time.Minute)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", map[string]any{
		"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 2, false, false),
	}, cookie, nil), http.StatusUnauthorized)
}

func TestPasskeyRegistrationChallengeIsOneTimeAndSessionBound(t *testing.T) {
	harness := newPasskeyHarness(t)
	cookie := harness.session(t, true)
	begin := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "first"}, cookie, nil))
	credential := newSoftwarePasskey(t)
	body := map[string]any{"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false)}
	secondBegin := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "second"}, cookie, nil))
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", body, cookie, nil), http.StatusBadRequest)
	body = map[string]any{"challenge_id": secondBegin.ChallengeID, "credential": credential.proof(t, secondBegin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false)}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", body, cookie, nil), http.StatusCreated)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", body, cookie, nil), http.StatusBadRequest)
	duplicateBegin := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "duplicate"}, cookie, nil))
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", map[string]any{
		"challenge_id": duplicateBegin.ChallengeID, "credential": credential.proof(t, duplicateBegin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false),
	}, cookie, nil), http.StatusBadRequest)
	sessionID, err := auth.ParseSessionToken(harness.server.cfg.SessionSecret, cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	revoked := decodePasskeyBegin(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/begin", map[string]string{"name": "revoked"}, cookie, nil))
	if err := harness.server.store.DeleteSession(t.Context(), sessionID); err != nil {
		t.Fatal(err)
	}
	if _, err := harness.server.store.ConsumePasskeyChallenge(t.Context(), revoked.ChallengeID, "register", sessionID, harness.now); err != store.ErrNotFound {
		t.Fatalf("revoked session retained a challenge: %v", err)
	}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/passkeys/register/finish", map[string]any{
		"challenge_id": revoked.ChallengeID, "credential": credential.proof(t, revoked.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x45, 0, true, false),
	}, cookie, nil), http.StatusUnauthorized)
}

func TestPasskeyCountersSupportSyncedAndCounterlessCredentials(t *testing.T) {
	for _, scenario := range []struct {
		name     string
		flags    byte
		counters []uint32
		statuses []int
	}{
		{"device", 0x05, []uint32{1, 1, 0, 2}, []int{200, 401, 401, 200}},
		{"counterless", 0x05, []uint32{0, 0, 0}, []int{200, 200, 200}},
		{"synced", 0x1d, []uint32{2, 1, 0, 3}, []int{200, 200, 200, 200}},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			harness := newPasskeyHarness(t)
			credential, _ := harness.register(t, harness.session(t, true), scenario.flags|0x40)
			for index, counter := range scenario.counters {
				begin, cookie := harness.beginLogin(t)
				response := harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", map[string]any{
					"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", scenario.flags, counter, false, false),
				}, cookie, nil)
				requirePasskeyStatus(t, response, scenario.statuses[index])
			}
			stored, err := harness.server.store.ListPasskeys(t.Context(), 1)
			if err != nil || len(stored) != 1 {
				t.Fatalf("missing credential: %+v, %v", stored, err)
			}
			if stored[0].Credential.Authenticator.SignCount != scenario.counters[len(scenario.counters)-1] || len(stored[0].Credential.Transport) != 1 || stored[0].Credential.Transport[0] != "internal" {
				t.Fatalf("counter or transports not persisted: %+v", stored[0].Credential)
			}
		})
	}
}

func TestPasskeyLoginOriginRateLimitAndDeletedCredential(t *testing.T) {
	harness := newPasskeyHarness(t)
	owner := harness.session(t, true)
	credential, id := harness.register(t, owner, 0x45)
	for _, path := range []string{"/api/v1/auth/passkey/begin", "/api/v1/auth/passkey/finish"} {
		for _, headers := range []map[string]string{{"Origin": "https://evil.example"}, {"Sec-Fetch-Site": "cross-site"}} {
			requirePasskeyStatus(t, harness.request(t, http.MethodPost, path, nil, nil, headers), http.StatusForbidden)
		}
	}
	for attempt := 0; attempt < 5; attempt++ {
		requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", map[string]any{"challenge_id": "invalid", "credential": nil}, nil, nil), http.StatusUnauthorized)
	}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", nil, nil, nil), http.StatusTooManyRequests)
	for attempt := 0; attempt < 5; attempt++ {
		response := harness.request(t, http.MethodPost, "/api/v1/auth/passkey/begin", nil, nil, map[string]string{"Origin": "", "X-Tack-Client": "", "Idempotency-Key": "begin"})
		decodePasskeyBegin(t, response)
		if response.Header().Get("X-Tack-Idempotent-Replay") != "" {
			t.Fatal("begin response was replayed")
		}
	}
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/begin", nil, nil, nil), http.StatusTooManyRequests)
	harness.now = harness.now.Add(time.Minute)
	begin, cookie := harness.beginLogin(t)
	requirePasskeyStatus(t, harness.request(t, http.MethodDelete, "/api/v1/passkeys/"+id, nil, owner, nil), http.StatusNoContent)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", map[string]any{
		"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false),
	}, cookie, nil), http.StatusUnauthorized)
}

func TestPasskeyConcurrentFinishCreatesOnlyOneSession(t *testing.T) {
	harness := newPasskeyHarness(t)
	credential, _ := harness.register(t, harness.session(t, true), 0x45)
	begin, cookie := harness.beginLogin(t)
	proof := map[string]any{"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false)}
	var workers sync.WaitGroup
	statuses := make(chan int, 5)
	for attempt := 0; attempt < 5; attempt++ {
		workers.Go(func() {
			statuses <- harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", proof, cookie, nil).Code
		})
	}
	workers.Wait()
	close(statuses)
	successful := 0
	for status := range statuses {
		if status == http.StatusOK {
			successful++
		} else if status != http.StatusUnauthorized {
			t.Errorf("unexpected concurrent status: %d", status)
		}
	}
	if successful != 1 {
		t.Fatalf("successful proofs = %d, want exactly 1", successful)
	}
	sessions, err := harness.server.store.ListSessions(t.Context(), 1, harness.now)
	if err != nil || len(sessions) != 2 {
		t.Fatalf("expected owner session plus one passkey session: %d, %v", len(sessions), err)
	}
}

func TestPasskeyMalformedJSONConsumesIdentifiedChallenge(t *testing.T) {
	harness := newPasskeyHarness(t)
	credential, _ := harness.register(t, harness.session(t, true), 0x45)
	begin, cookie := harness.beginLogin(t)
	body := map[string]any{"challenge_id": begin.ChallengeID, "credential": credential.proof(t, begin.PublicKey.Challenge, passkeyTestOrigin, "tack.example", 0x05, 1, false, false)}
	malformed := append(passkeyJSON(t, body), []byte(" trailing garbage")...)
	req := httptest.NewRequest(http.MethodPost, passkeyTestOrigin+"/api/v1/auth/passkey/finish", bytes.NewReader(malformed))
	req.AddCookie(cookie)
	req.Header.Set("Origin", passkeyTestOrigin)
	response := httptest.NewRecorder()
	harness.handler.ServeHTTP(response, req)
	requirePasskeyStatus(t, response, http.StatusUnauthorized)
	requirePasskeyStatus(t, harness.request(t, http.MethodPost, "/api/v1/auth/passkey/finish", body, cookie, nil), http.StatusUnauthorized)
}
