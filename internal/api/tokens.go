package api

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/store"
)

type tokenResponse struct {
	ID         int64    `json:"id"`
	Name       string   `json:"name"`
	Prefix     string   `json:"prefix"`
	Scopes     []string `json:"scopes"`
	ProjectIDs []int64  `json:"project_ids"`
	TagIDs     []int64  `json:"tag_ids"`
	CreatedAt  string   `json:"created_at"`
	LastUsedAt *string  `json:"last_used_at"`
	ExpiresAt  *string  `json:"expires_at"`
}

func tokenFromStore(token store.APIToken) tokenResponse {
	result := tokenResponse{
		ID: token.ID, Name: token.Name, Prefix: token.Prefix, Scopes: token.Scopes,
		ProjectIDs: token.ProjectIDs, TagIDs: token.TagIDs, CreatedAt: token.CreatedAt.UTC().Format(time.RFC3339),
	}
	if token.LastUsedAt != nil {
		value := token.LastUsedAt.UTC().Format(time.RFC3339)
		result.LastUsedAt = &value
	}
	if token.ExpiresAt != nil {
		value := token.ExpiresAt.UTC().Format(time.RFC3339)
		result.ExpiresAt = &value
	}
	return result
}

func (s *Server) handleListTokens(w http.ResponseWriter, r *http.Request) {
	tokens, err := s.store.ListAPITokens(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取 API 令牌")
		return
	}
	result := make([]tokenResponse, 0, len(tokens))
	for _, token := range tokens {
		result = append(result, tokenFromStore(token))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"tokens": result})
}

func (s *Server) handleCreateToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name       string   `json:"name"`
		Scopes     []string `json:"scopes"`
		ProjectIDs []int64  `json:"project_ids"`
		TagIDs     []int64  `json:"tag_ids"`
		ExpiresAt  *string  `json:"expires_at"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len([]rune(request.Name)) > 80 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_name", "令牌名称长度不正确")
		return
	}
	if !auth.ValidAPITokenScopes(request.Scopes) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_scopes", "令牌权限不正确")
		return
	}
	now := s.now().UTC()
	var expiresAt *time.Time
	if request.ExpiresAt != nil {
		parsed, err := time.Parse(time.RFC3339, *request.ExpiresAt)
		parsed = parsed.UTC().Truncate(time.Second)
		if err != nil || !parsed.After(now) || parsed.After(now.Add(365*24*time.Hour)) {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_expiry", "有效期须为未来 365 天内的 RFC3339 时间")
			return
		}
		expiresAt = &parsed
	}
	if len(request.ProjectIDs) > 100 || len(request.TagIDs) > 100 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_resources", "项目或标签范围过大")
		return
	}
	for _, id := range request.ProjectIDs {
		if id <= 0 || s.validateProject(r, id) != nil {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_resources", "项目不存在或已归档")
			return
		}
	}
	for _, id := range request.TagIDs {
		if _, err := s.store.TagByID(r.Context(), id); id <= 0 || err != nil {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_resources", "标签不存在")
			return
		}
	}
	rawToken, prefix, hash, err := auth.NewAPIToken()
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法生成令牌")
		return
	}
	token, err := s.store.CreateAPIToken(
		r.Context(), request.Name, prefix, hash, request.Scopes, now,
		store.TokenOptions{ProjectIDs: request.ProjectIDs, TagIDs: request.TagIDs, ExpiresAt: expiresAt},
	)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法保存令牌")
		return
	}
	s.writeJSON(w, http.StatusCreated, map[string]any{
		"token":     rawToken,
		"api_token": tokenFromStore(token),
	})
}

func (s *Server) authorizeTokenSecretReplay(w http.ResponseWriter, r *http.Request) bool {
	if principal, ok := s.principal(r); ok && principal.Session != nil {
		return true
	}
	s.writeError(w, r, http.StatusForbidden, "web_only", "该操作只能在网页中使用")
	return false
}

func (s *Server) handleRevokeToken(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if r.ContentLength != 0 {
		if err := decodeJSON(w, r, &request); err != nil && !errors.Is(err, io.EOF) {
			s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
			return
		}
	}
	if !s.confirmSudo(w, r, request.Password) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "令牌 ID 不正确")
		return
	}
	if err := s.store.RevokeAPIToken(r.Context(), id, s.now().UTC()); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "token_not_found", "令牌不存在")
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法撤销令牌")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
