package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-webauthn/webauthn/protocol"
	"github.com/go-webauthn/webauthn/webauthn"
	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/store"
)

type passkeyResponse struct {
	ID         string  `json:"id"`
	Name       string  `json:"name"`
	CreatedAt  string  `json:"created_at"`
	LastUsedAt *string `json:"last_used_at"`
}

type passkeyFinishRequest struct {
	ChallengeID string          `json:"challenge_id"`
	Credential  json.RawMessage `json:"credential"`
}

func (s *Server) registerPasskeyRoutes(mux *http.ServeMux) {
	management := func(handler http.HandlerFunc) http.Handler {
		return passkeyNoStore(s.requireAuth(s.requireWeb(handler)))
	}
	mux.Handle("GET /api/v1/passkeys", management(s.handleListPasskeys))
	mux.Handle("POST /api/v1/passkeys/register/begin", management(s.handleBeginPasskeyRegistration))
	mux.Handle("POST /api/v1/passkeys/register/finish", management(s.handleFinishPasskeyRegistration))
	mux.Handle("DELETE /api/v1/passkeys/{id}", management(s.handleDeletePasskey))
	mux.Handle("POST /api/v1/auth/passkey/begin", passkeyNoStore(http.HandlerFunc(s.handleBeginPasskeyLogin)))
	mux.Handle("POST /api/v1/auth/passkey/finish", passkeyNoStore(http.HandlerFunc(s.handleFinishPasskeyLogin)))
}

func passkeyNoStore(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func passkeyMetadata(passkey store.Passkey) passkeyResponse {
	result := passkeyResponse{ID: passkey.ID, Name: passkey.Name, CreatedAt: passkey.CreatedAt.UTC().Format(time.RFC3339)}
	if passkey.LastUsedAt != nil {
		lastUsedAt := passkey.LastUsedAt.UTC().Format(time.RFC3339)
		result.LastUsedAt = &lastUsedAt
	}
	return result
}

func (s *Server) handleListPasskeys(w http.ResponseWriter, r *http.Request) {
	principal, _ := s.principal(r)
	passkeys, err := s.store.ListPasskeys(r.Context(), principal.User.ID)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	result := make([]passkeyResponse, 0, len(passkeys))
	for _, passkey := range passkeys {
		result = append(result, passkeyMetadata(passkey))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"passkeys": result})
}

func (s *Server) passkeyUser(r *http.Request, user store.User) (auth.PasskeyUser, []store.Passkey, error) {
	passkeys, err := s.store.ListPasskeys(r.Context(), user.ID)
	if err != nil {
		return auth.PasskeyUser{}, nil, err
	}
	result := auth.PasskeyUser{ID: user.ID, Username: user.Username, Credentials: make([]webauthn.Credential, 0, len(passkeys))}
	for _, passkey := range passkeys {
		result.Credentials = append(result.Credentials, passkey.Credential)
	}
	return result, passkeys, nil
}

func (s *Server) handleBeginPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name     string `json:"name"`
		Password string `json:"password"`
	}
	if err := decodePasskeyJSON(w, r, &request); err != nil {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	if !s.confirmSudo(w, r, request.Password) {
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if !utf8.ValidString(request.Name) || utf8.RuneCountInString(request.Name) < 1 || utf8.RuneCountInString(request.Name) > 80 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_name", "Passkey 名称长度应为 1–80 个字符")
		return
	}
	principal, _ := s.principal(r)
	user, _, err := s.passkeyUser(r, principal.User)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	relyingParty, err := auth.NewPasskey(s.cfg.PublicURL)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	options, session, err := relyingParty.BeginRegistration(user, webauthn.WithExclusions(webauthn.Credentials(user.Credentials).CredentialDescriptors()))
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	challenge, err := s.savePasskeyChallenge(r, session, store.PasskeyChallenge{
		Purpose: "register", UserID: principal.User.ID, SessionID: principal.Session.ID, Binding: principal.Session.ID, Name: request.Name,
	})
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"challenge_id": challenge.ID, "publicKey": options.Response})
}

func (s *Server) handleFinishPasskeyRegistration(w http.ResponseWriter, r *http.Request) {
	var request passkeyFinishRequest
	decodeErr := decodePasskeyJSON(w, r, &request)
	principal, _ := s.principal(r)
	challenge, session, err := s.consumePasskeyChallenge(r, request.ChallengeID, "register", principal.Session.ID)
	if decodeErr != nil || err != nil || challenge.UserID != principal.User.ID {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	relyingParty, err := auth.NewPasskey(s.cfg.PublicURL)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	user, _, err := s.passkeyUser(r, principal.User)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	parsed, err := protocol.ParseCredentialCreationResponseBytes(request.Credential)
	if err != nil {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	credential, err := relyingParty.CreateCredential(user, session, parsed)
	if err != nil {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	id, err := auth.NewPasskeyToken()
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	passkey := store.Passkey{ID: id, UserID: principal.User.ID, Name: challenge.Name, Credential: *credential, CreatedAt: s.now().UTC()}
	if err := s.store.CreatePasskey(r.Context(), passkey); err != nil {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{"passkey": passkeyMetadata(passkey)})
}

func (s *Server) handleBeginPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requirePasskeyOrigin(w, r) || !s.allowPasskeyAttempt(w, r, "begin") {
		return
	}
	user, err := s.store.UserByID(r.Context(), 1)
	if err != nil {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	passkeyUser, _, err := s.passkeyUser(r, user)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	if len(passkeyUser.Credentials) == 0 {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	relyingParty, err := auth.NewPasskey(s.cfg.PublicURL)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	options, session, err := relyingParty.BeginLogin(passkeyUser, webauthn.WithUserVerification(protocol.VerificationRequired))
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	binding, err := auth.NewPasskeyToken()
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	challenge, err := s.savePasskeyChallenge(r, session, store.PasskeyChallenge{Purpose: "login", UserID: user.ID, Binding: auth.HashAPIToken(binding)})
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	s.setPasskeyCookie(w, binding, int(auth.PasskeyChallengeTTL.Seconds()))
	s.writeJSON(w, http.StatusOK, map[string]any{"challenge_id": challenge.ID, "publicKey": options.Response})
}

func (s *Server) handleFinishPasskeyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requirePasskeyOrigin(w, r) || !s.allowPasskeyAttempt(w, r, "finish") {
		return
	}
	var request passkeyFinishRequest
	decodeErr := decodePasskeyJSON(w, r, &request)
	cookie, cookieErr := r.Cookie(auth.PasskeyCookieName(s.cfg.CookieSecure))
	if cookieErr != nil || cookie.Value == "" {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	challenge, session, err := s.consumePasskeyChallenge(r, request.ChallengeID, "login", auth.HashAPIToken(cookie.Value))
	s.setPasskeyCookie(w, "", -1)
	if decodeErr != nil || err != nil || challenge.UserID != 1 {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	user, err := s.store.UserByID(r.Context(), challenge.UserID)
	if err != nil {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	passkeyUser, passkeys, err := s.passkeyUser(r, user)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	relyingParty, err := auth.NewPasskey(s.cfg.PublicURL)
	if err != nil {
		s.writePasskeyUnavailable(w, r)
		return
	}
	credential, err := auth.ValidatePasskeyLogin(relyingParty, passkeyUser, session, request.Credential)
	if err != nil {
		s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
		return
	}
	for _, previous := range passkeys {
		if !bytes.Equal(previous.Credential.ID, credential.ID) {
			continue
		}
		if err := s.store.UpdatePasskeyUsage(r.Context(), previous, *credential, s.now().UTC()); err != nil {
			s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
			return
		}
		s.createSession(w, r, user)
		return
	}
	s.writeInvalidPasskey(w, r, http.StatusUnauthorized)
}

func (s *Server) handleDeletePasskey(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := decodePasskeyJSON(w, r, &request); err != nil && !errors.Is(err, io.EOF) {
		s.writeInvalidPasskey(w, r, http.StatusBadRequest)
		return
	}
	if !s.confirmSudo(w, r, request.Password) {
		return
	}
	principal, _ := s.principal(r)
	if err := s.store.DeletePasskey(r.Context(), principal.User.ID, r.PathValue("id")); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "passkey_not_found", "Passkey 不存在")
			return
		}
		s.writePasskeyUnavailable(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) savePasskeyChallenge(r *http.Request, session *webauthn.SessionData, challenge store.PasskeyChallenge) (store.PasskeyChallenge, error) {
	var err error
	if challenge.ID, err = auth.NewPasskeyToken(); err != nil {
		return store.PasskeyChallenge{}, err
	}
	if challenge.SessionData, err = json.Marshal(session); err != nil {
		return store.PasskeyChallenge{}, err
	}
	challenge.ExpiresAt = s.now().UTC().Add(auth.PasskeyChallengeTTL)
	err = s.store.SavePasskeyChallenge(r.Context(), challenge, s.now().UTC())
	return challenge, err
}

func (s *Server) consumePasskeyChallenge(r *http.Request, id, purpose, binding string) (store.PasskeyChallenge, webauthn.SessionData, error) {
	var session webauthn.SessionData
	challenge, err := s.store.ConsumePasskeyChallenge(r.Context(), id, purpose, binding, s.now().UTC())
	if err != nil {
		return store.PasskeyChallenge{}, session, err
	}
	err = json.Unmarshal(challenge.SessionData, &session)
	return challenge, session, err
}

func (s *Server) setPasskeyCookie(w http.ResponseWriter, value string, maxAge int) {
	cookie := &http.Cookie{
		Name: auth.PasskeyCookieName(s.cfg.CookieSecure), Value: value, Path: "/",
		HttpOnly: true, Secure: s.cfg.CookieSecure, SameSite: http.SameSiteStrictMode, MaxAge: maxAge,
	}
	if maxAge > 0 {
		cookie.Expires = s.now().UTC().Add(auth.PasskeyChallengeTTL)
	}
	http.SetCookie(w, cookie)
}

func (s *Server) requirePasskeyOrigin(w http.ResponseWriter, r *http.Request) bool {
	if origin := r.Header.Get("Origin"); origin != "" && origin != s.publicOrigin() {
		s.writeError(w, r, http.StatusForbidden, "csrf_failed", "请求来源不合法")
		return false
	}
	if site := r.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		s.writeError(w, r, http.StatusForbidden, "csrf_failed", "请求来源不合法")
		return false
	}
	return true
}

func (s *Server) allowPasskeyAttempt(w http.ResponseWriter, r *http.Request, purpose string) bool {
	key := "passkey:" + purpose + ":" + clientIP(r)
	s.limiter.mu.Lock()
	s.limiter.prune(key)
	allowed := len(s.limiter.history[key]) < s.limiter.limit
	if allowed {
		s.limiter.history[key] = append(s.limiter.history[key], s.now())
	}
	s.limiter.mu.Unlock()
	if !allowed {
		s.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请稍后再试")
	}
	return allowed
}

func decodePasskeyJSON(w http.ResponseWriter, r *http.Request, value any) error {
	decoder := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20))
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return fmt.Errorf("unexpected trailing JSON")
	}
	return nil
}

func (s *Server) writeInvalidPasskey(w http.ResponseWriter, r *http.Request, status int) {
	s.writeError(w, r, status, "invalid_passkey", "Passkey 验证失败，请重试或使用密码登录")
}

func (s *Server) writePasskeyUnavailable(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, http.StatusInternalServerError, "passkey_unavailable", "Passkey 暂时不可用，请使用密码登录")
}
