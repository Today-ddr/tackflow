package api

import (
	"net/http"
	"strings"
)

func (s *Server) handleAgentConnection(w http.ResponseWriter, r *http.Request) {
	principal, ok := s.principal(r)
	if !ok {
		s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先认证")
		return
	}
	var token *tokenResponse
	kind := "session"
	if principal.APIToken != nil {
		value := tokenFromStore(*principal.APIToken)
		token = &value
		kind = "token"
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]any{
		"api_base_url":  strings.TrimRight(s.cfg.PublicURL, "/") + "/api/v1",
		"auth_kind":     kind,
		"transport":     "rest",
		"mcp_supported": false,
		"token":         token,
	})
}
