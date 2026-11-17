package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/store"
)

type sessionResponse struct {
	ID         string  `json:"id"`
	CreatedAt  string  `json:"created_at"`
	LastSeenAt string  `json:"last_seen_at"`
	ExpiresAt  string  `json:"expires_at"`
	UserAgent  string  `json:"user_agent"`
	IP         string  `json:"ip"`
	Current    bool    `json:"current"`
	SudoUntil  *string `json:"sudo_until"`
}

func (s *Server) handleSudo(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	authValue, _ := s.principal(r)
	ok, err := auth.VerifyPassword(authValue.User.PasswordHash, request.Password)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "验证失败")
		return
	}
	if !ok {
		s.writeError(w, r, http.StatusForbidden, "invalid_password", "密码不正确")
		return
	}
	until := s.now().UTC().Add(5 * time.Minute)
	if err := s.store.SetSessionSudo(r.Context(), authValue.Session.ID, until); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法开启 sudo 模式")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{
		"sudo_expires_at": until.Format(time.RFC3339),
	})
}

func (s *Server) handlePassword(w http.ResponseWriter, r *http.Request) {
	var request struct {
		CurrentPassword string `json:"current_password"`
		NewPassword     string `json:"new_password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !s.confirmSudo(w, r, request.CurrentPassword) {
		return
	}
	if err := auth.ValidatePassword(request.NewPassword); err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "weak_password", err.Error())
		return
	}
	hash, err := auth.HashPassword(request.NewPassword)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法更新密码")
		return
	}
	authValue, _ := s.principal(r)
	if err := s.store.UpdatePassword(r.Context(), hash, s.now().UTC()); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法更新密码")
		return
	}
	_ = s.store.DeleteAllSessions(r.Context(), authValue.User.ID)
	s.clearSessionCookie(w)
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	authValue, _ := s.principal(r)
	sessions, err := s.store.ListSessions(r.Context(), authValue.User.ID, s.now().UTC())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取登录设备")
		return
	}
	result := make([]sessionResponse, 0, len(sessions))
	for _, session := range sessions {
		var sudoUntil *string
		if session.SudoUntil != nil && session.SudoUntil.After(s.now().UTC()) {
			value := session.SudoUntil.Format(time.RFC3339)
			sudoUntil = &value
		}
		result = append(result, sessionResponse{
			ID:         session.ID,
			CreatedAt:  session.CreatedAt.Format(time.RFC3339),
			LastSeenAt: session.LastSeenAt.Format(time.RFC3339),
			ExpiresAt:  session.ExpiresAt.Format(time.RFC3339),
			UserAgent:  session.UserAgent,
			IP:         session.IP,
			Current:    session.ID == authValue.Session.ID,
			SudoUntil:  sudoUntil,
		})
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"sessions": result})
}

func (s *Server) handleDeleteSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "缺少会话 ID")
		return
	}
	authValue, _ := s.principal(r)
	if err := s.store.DeleteSessionByID(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "session_not_found", "登录设备不存在")
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法移除登录设备")
		return
	}
	if id == authValue.Session.ID {
		s.clearSessionCookie(w)
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     auth.CookieName(s.cfg.CookieSecure),
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		Secure:   s.cfg.CookieSecure,
		SameSite: http.SameSiteLaxMode,
		MaxAge:   -1,
	})
}

func (s *Server) userPasswordMatches(r *http.Request, password string) bool {
	authValue, ok := s.principal(r)
	if !ok || strings.TrimSpace(password) == "" {
		return false
	}
	valid, err := auth.VerifyPassword(authValue.User.PasswordHash, password)
	return err == nil && valid
}
