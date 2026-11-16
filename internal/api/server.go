package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"

	"github.com/today/tack/internal/auth"
	"github.com/today/tack/internal/config"
	"github.com/today/tack/internal/store"
	"github.com/today/tack/internal/update"
)

const requestIDKey contextKey = "request_id"

type contextKey string

type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

type Server struct {
	cfg          config.Config
	store        *store.Store
	assets       fs.FS
	build        BuildInfo
	logger       *slog.Logger
	now          func() time.Time
	limiter      *loginLimiter
	changelog    string
	updateSvc    *update.Service
	updater      *update.UpdaterClient
	mutationGate chan struct{}
}

func NewServer(
	cfg config.Config,
	st *store.Store,
	assets fs.FS,
	build BuildInfo,
	logger *slog.Logger,
	now func() time.Time,
) *Server {
	if logger == nil {
		logger = slog.Default()
	}
	if now == nil {
		now = time.Now
	}
	if cfg.Timezone == nil {
		cfg.Timezone = time.Local
	}
	if cfg.TimezoneName == "" {
		cfg.TimezoneName = cfg.Timezone.String()
	}
	return &Server{
		cfg:          cfg,
		store:        st,
		assets:       assets,
		build:        build,
		logger:       logger,
		now:          now,
		limiter:      newLoginLimiter(now, 5, time.Minute),
		mutationGate: make(chan struct{}, 1),
	}
}

func (s *Server) Handler() http.Handler {
	return s.withRequestID(s.withSecurityHeaders(s.routes()))
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /readyz", s.handleReady)
	mux.HandleFunc("GET /api/v1/version", s.handleVersion)
	mux.HandleFunc("GET /api/v1/changelog", s.handleChangelog)
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.Handle("GET /api/v1/auth/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("GET /api/v1/agent/connection", s.requireAuth(http.HandlerFunc(s.handleAgentConnection)))
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
	mux.Handle("POST /api/v1/auth/sudo", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleSudo))))
	mux.Handle("POST /api/v1/auth/password", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handlePassword))))

	mux.Handle("GET /api/v1/tasks", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleListTasks))))
	mux.Handle("POST /api/v1/tasks", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleCreateTask)))))
	mux.Handle("GET /api/v1/tasks/{id}", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleGetTask))))
	mux.Handle("PATCH /api/v1/tasks/{id}", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handlePatchTask)))))
	mux.Handle("DELETE /api/v1/tasks/{id}", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleDeleteTask)))))
	mux.Handle("POST /api/v1/tasks/{id}/move", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleMoveTask)))))
	mux.Handle("POST /api/v1/tasks/{id}/comments", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleCommentTask)))))
	mux.Handle("GET /api/v1/tasks/{id}/events", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleTaskEvents))))
	mux.Handle("POST /api/v1/capture", s.requireAuth(s.requireScope(auth.ScopeCapture, s.withIdempotency(http.HandlerFunc(s.handleCapture)))))

	mux.Handle("GET /api/v1/projects", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleListProjects))))
	mux.Handle("POST /api/v1/projects", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleCreateProject)))))
	mux.Handle("PATCH /api/v1/projects/{id}", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handlePatchProject)))))
	mux.Handle("DELETE /api/v1/projects/{id}", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleDeleteProject)))))

	mux.Handle("GET /api/v1/tags", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleListTags))))
	mux.Handle("POST /api/v1/tags", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleCreateTag)))))
	mux.Handle("PATCH /api/v1/tags/{id}", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handlePatchTag)))))
	mux.Handle("DELETE /api/v1/tags/{id}", s.requireAuth(s.requireScope(auth.ScopeWrite, s.withIdempotency(http.HandlerFunc(s.handleDeleteTag)))))

	mux.Handle("GET /api/v1/tokens", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleListTokens))))
	mux.Handle("POST /api/v1/tokens", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleCreateToken), s.authorizeTokenSecretReplay))))
	mux.Handle("DELETE /api/v1/tokens/{id}", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleRevokeToken)))))

	mux.Handle("GET /api/v1/sessions", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleListSessions))))
	mux.Handle("DELETE /api/v1/sessions/{id}", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleDeleteSession))))

	mux.Handle("GET /api/v1/settings", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleGetSettings))))
	mux.Handle("PATCH /api/v1/settings", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handlePatchSettings)))))
	mux.Handle("GET /api/v1/update/status", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleUpdateStatus))))
	mux.Handle("POST /api/v1/update/check", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleUpdateCheck)))))
	mux.Handle("POST /api/v1/update/apply", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleUpdateApply)))))
	mux.Handle("POST /api/v1/update/rollback", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleUpdateRollback)))))
	mux.Handle("GET /api/v1/export", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleExport))))
	s.registerPasskeyRoutes(mux)
	s.registerEventRoutes(mux)
	s.registerWebhookRoutes(mux)
	s.registerAgentRoutes(mux)
	s.registerTrashRoutes(mux)

	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok\n"))
}

func (s *Server) handleReady(w http.ResponseWriter, r *http.Request) {
	if err := s.store.Ping(r.Context()); err != nil {
		s.writeError(w, r, http.StatusServiceUnavailable, "not_ready", "数据库未就绪")
		return
	}
	s.handleHealth(w, r)
}

func (s *Server) handleVersion(w http.ResponseWriter, _ *http.Request) {
	s.writeJSON(w, http.StatusOK, map[string]string{
		"version":    s.build.Version,
		"commit":     s.build.Commit,
		"build_time": s.build.BuildTime,
	})
}

type authContext struct {
	User     store.User
	Session  *store.Session
	APIToken *store.APIToken
}

type authContextKey struct{}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if header := strings.TrimSpace(r.Header.Get("Authorization")); header != "" {
			rawToken, ok := auth.BearerToken(header)
			if !ok {
				s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "API 令牌无效")
				return
			}
			apiToken, err := s.store.APITokenByHash(r.Context(), auth.HashAPIToken(rawToken))
			if err != nil || apiToken.ExpiresAt != nil && !apiToken.ExpiresAt.After(s.now().UTC()) {
				s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "API 令牌无效")
				return
			}
			user, err := s.store.UserByID(r.Context(), 1)
			if err != nil {
				s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "用户不存在")
				return
			}
			_ = s.store.TouchAPIToken(r.Context(), apiToken.ID, s.now().UTC())
			ctx := context.WithValue(r.Context(), authContextKey{}, authContext{
				User:     user,
				APIToken: &apiToken,
			})
			r = r.WithContext(ctx)
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if !s.prepareMutation(w, r) {
					return
				}
				defer func() { <-s.mutationGate }()
				current, err := s.store.APITokenByHash(r.Context(), apiToken.Hash)
				if err != nil || current.ExpiresAt != nil && !current.ExpiresAt.After(s.now().UTC()) {
					s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "API 令牌已失效")
					return
				}
			}
			if s.resourceLimited(r) && r.Method != http.MethodGet && r.Method != http.MethodHead &&
				(strings.HasPrefix(r.URL.Path, "/api/v1/projects") || strings.HasPrefix(r.URL.Path, "/api/v1/tags")) {
				s.writeError(w, r, http.StatusForbidden, "resource_forbidden", "受限令牌不能修改全局项目或标签")
				return
			}
			next.ServeHTTP(w, r)
			return
		}

		cookie, err := r.Cookie(auth.CookieName(s.cfg.CookieSecure))
		if err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}

		sessionID, err := auth.ParseSessionToken(s.cfg.SessionSecret, cookie.Value)
		if err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "登录已失效")
			return
		}
		now := s.now().UTC()
		session, err := s.store.SessionByID(r.Context(), sessionID, now)
		if err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "登录已失效")
			return
		}
		expiresAt := now.Add(s.cfg.SessionTTL)
		if err := s.store.TouchSession(r.Context(), session.ID, now, expiresAt); err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "登录已失效")
			return
		}
		user, err := s.store.UserByID(r.Context(), session.UserID)
		if err != nil {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "用户不存在")
			return
		}

		principal := authContext{User: user, Session: &session}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.requireCSRF(w, r) {
				return
			}
		}
		ctx := context.WithValue(r.Context(), authContextKey{}, principal)
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			if !s.prepareMutation(w, r) {
				return
			}
			defer func() { <-s.mutationGate }()
			if _, err := s.store.SessionByID(r.Context(), session.ID, s.now().UTC()); err != nil {
				s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "登录已失效")
				return
			}
		}
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) prepareMutation(w http.ResponseWriter, r *http.Request) bool {
	controller := http.NewResponseController(w)
	_ = controller.SetReadDeadline(time.Now().Add(10 * time.Second))
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20))
	_ = r.Body.Close()
	_ = controller.SetReadDeadline(time.Time{})
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求体过大、未完整送达或已超时")
		return false
	}
	r.Body = io.NopCloser(bytes.NewReader(body))
	select {
	case <-r.Context().Done():
		return false
	case s.mutationGate <- struct{}{}:
		if r.Context().Err() != nil {
			<-s.mutationGate
			return false
		}
		return true
	}
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	authValue, ok := s.principal(r)
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	var sudoExpiresAt *time.Time
	authKind := "token"
	if authValue.Session != nil {
		authKind = "session"
		if authValue.Session.SudoUntil != nil && authValue.Session.SudoUntil.After(s.now().UTC()) {
			sudoExpiresAt = authValue.Session.SudoUntil
		}
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{
			"username": authValue.User.Username,
		},
		"auth_kind":       authKind,
		"sudo_expires_at": sudoExpiresAt,
	})
}

func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !s.limiter.Allow(ip) {
		s.writeError(w, r, http.StatusTooManyRequests, "rate_limited", "尝试次数过多，请稍后再试")
		return
	}

	var request struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}

	user, err := s.store.UserByUsername(r.Context(), strings.TrimSpace(request.Username))
	if errors.Is(err, store.ErrNotFound) {
		s.limiter.RecordFailure(ip)
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}

	ok, err := auth.VerifyPassword(user.PasswordHash, request.Password)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	if !ok {
		s.limiter.RecordFailure(ip)
		s.writeError(w, r, http.StatusUnauthorized, "invalid_credentials", "用户名或密码错误")
		return
	}

	s.createSession(w, r, user)
}

func (s *Server) createSession(w http.ResponseWriter, r *http.Request, user store.User) {
	cookieValue, sessionID, err := auth.NewSessionToken(s.cfg.SessionSecret)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), sessionID, user.ID, now, expiresAt, r.UserAgent(), clientIP(r)); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName(s.cfg.CookieSecure),
		Value:    cookieValue,
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		Expires:  expiresAt,
		MaxAge:   int(s.cfg.SessionTTL.Seconds()),
	})
	s.writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{"username": user.Username},
	})
}

func (s *Server) handleLogout(w http.ResponseWriter, r *http.Request) {
	authValue, ok := s.principal(r)
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	if authValue.Session == nil {
		s.writeError(w, r, http.StatusForbidden, "web_only", "该操作只能在网页中使用")
		return
	}
	if err := s.store.DeleteSession(r.Context(), authValue.Session.ID); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "退出失败")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName(s.cfg.CookieSecure),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleChangelog(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.changelog))
}

func (s *Server) SetChangelog(changelog string) {
	s.changelog = changelog
}

func (s *Server) SetUpdateService(service *update.Service, client *update.UpdaterClient) {
	s.updateSvc = service
	s.updater = client
}

func (s *Server) principal(r *http.Request) (authContext, bool) {
	value, ok := r.Context().Value(authContextKey{}).(authContext)
	return value, ok
}

func (s *Server) requireWeb(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authValue, ok := s.principal(r)
		if !ok || authValue.Session == nil {
			s.writeError(w, r, http.StatusForbidden, "web_only", "该操作只能在网页中使用")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireScope(required string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authValue, ok := s.principal(r)
		if !ok {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		if authValue.APIToken != nil && !auth.HasScope(authValue.APIToken.Scopes, required) {
			s.writeError(w, r, http.StatusForbidden, "insufficient_scope", "API 令牌权限不足")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) requireSudo(w http.ResponseWriter, r *http.Request) bool {
	authValue, ok := s.principal(r)
	if !ok || authValue.Session == nil {
		s.writeError(w, r, http.StatusForbidden, "web_only", "该操作只能在网页中使用")
		return false
	}
	if authValue.Session.SudoUntil == nil || !authValue.Session.SudoUntil.After(s.now().UTC()) {
		s.writeError(w, r, http.StatusForbidden, "sudo_required", "请重新输入密码")
		return false
	}
	return true
}

func (s *Server) requireCSRF(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("X-Tack-Client") != "web" {
		s.writeError(w, r, http.StatusForbidden, "csrf_failed", "缺少客户端标识")
		return false
	}
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

func (s *Server) publicOrigin() string {
	parsed, err := url.Parse(s.cfg.PublicURL)
	if err != nil {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, "/api/") {
		s.writeError(w, r, http.StatusNotFound, "not_found", "接口不存在")
		return
	}

	assetPath := path.Clean(strings.TrimPrefix(r.URL.Path, "/"))
	if assetPath == "." || assetPath == "/" {
		assetPath = "index.html"
	}
	data, err := fs.ReadFile(s.assets, assetPath)
	if err != nil {
		assetPath = "index.html"
		data, err = fs.ReadFile(s.assets, assetPath)
	}
	if err != nil {
		s.writeError(w, r, http.StatusNotFound, "not_found", "资源不存在")
		return
	}
	extension := path.Ext(assetPath)
	contentType := mime.TypeByExtension(extension)
	if extension == ".webmanifest" {
		contentType = "application/manifest+json"
	}
	if contentType != "" {
		w.Header().Set("Content-Type", contentType)
	}
	http.ServeContent(w, r, assetPath, time.Time{}, bytes.NewReader(data))
}

func (s *Server) writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func (s *Server) writeError(w http.ResponseWriter, r *http.Request, status int, code, message string) {
	s.writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":       code,
			"message":    message,
			"request_id": requestID(r.Context()),
		},
	})
}

func decodeJSON(w http.ResponseWriter, r *http.Request, value any) error {
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("multiple JSON values")
	}
	return nil
}

func requestID(ctx context.Context) string {
	value, _ := ctx.Value(requestIDKey).(string)
	return value
}

func newRequestID() string {
	random := make([]byte, 8)
	if _, err := rand.Read(random); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(random)
}

func clientIP(r *http.Request) string {
	host, _, err := netSplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func netSplitHostPort(address string) (string, string, error) {
	if strings.HasPrefix(address, "[") {
		end := strings.IndexByte(address, ']')
		if end == -1 {
			return "", "", fmt.Errorf("invalid address")
		}
		return address[1:end], "", nil
	}
	host, port, ok := strings.Cut(address, ":")
	if !ok {
		return "", "", fmt.Errorf("invalid address")
	}
	return host, port, nil
}
