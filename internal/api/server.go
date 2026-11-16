package api

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
)

const requestIDKey contextKey = "request_id"

type contextKey string

type BuildInfo struct {
	Version   string
	Commit    string
	BuildTime string
}

type Server struct {
	cfg     config.Config
	store   *store.Store
	assets  fs.FS
	build   BuildInfo
	logger  *slog.Logger
	now     func() time.Time
	limiter *loginLimiter
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
	return &Server{
		cfg:     cfg,
		store:   st,
		assets:  assets,
		build:   build,
		logger:  logger,
		now:     now,
		limiter: newLoginLimiter(now, 5, time.Minute),
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
	mux.HandleFunc("POST /api/v1/auth/login", s.handleLogin)
	mux.Handle("GET /api/v1/auth/me", s.requireAuth(http.HandlerFunc(s.handleMe)))
	mux.Handle("POST /api/v1/auth/logout", s.requireAuth(http.HandlerFunc(s.handleLogout)))
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
	User    store.User
	Session store.Session
}

type authContextKey struct{}

func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
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

		ctx := context.WithValue(r.Context(), authContextKey{}, authContext{User: user, Session: session})
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (s *Server) handleMe(w http.ResponseWriter, r *http.Request) {
	authValue, ok := r.Context().Value(authContextKey{}).(authContext)
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"user": map[string]string{
			"username": authValue.User.Username,
		},
		"sudo_expires_at": nil,
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

	cookieValue, sessionID, err := auth.NewSessionToken(s.cfg.SessionSecret)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "登录失败")
		return
	}
	now := s.now().UTC()
	expiresAt := now.Add(s.cfg.SessionTTL)
	if err := s.store.CreateSession(r.Context(), sessionID, user.ID, now, expiresAt, r.UserAgent(), ip); err != nil {
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
	if !s.requireCSRF(w, r) {
		return
	}
	authValue, ok := r.Context().Value(authContextKey{}).(authContext)
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
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
	if contentType := mime.TypeByExtension(path.Ext(assetPath)); contentType != "" {
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
