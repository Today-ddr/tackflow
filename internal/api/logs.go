package api

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/today-ddr/tackflow/internal/diagnostics"
)

var requestIDPattern = regexp.MustCompile(`^req_[0-9a-f]{16}$`)

func (s *Server) handleListLogs(w http.ResponseWriter, r *http.Request) {
	query := r.URL.Query()
	filter := diagnostics.Filter{Level: strings.ToUpper(strings.TrimSpace(query.Get("level"))), RequestID: strings.TrimSpace(query.Get("request_id")), Limit: 200}
	if filter.Level != "" && filter.Level != "DEBUG" && filter.Level != "INFO" && filter.Level != "WARN" && filter.Level != "ERROR" {
		s.writeError(w, r, http.StatusBadRequest, "invalid_log_filter", "日志级别不正确")
		return
	}
	if filter.RequestID != "" && !requestIDPattern.MatchString(filter.RequestID) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_log_filter", "请求 ID 格式不正确")
		return
	}
	if value := query.Get("limit"); value != "" {
		limit, err := strconv.Atoi(value)
		if err != nil || limit < 1 || limit > 500 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_log_filter", "日志数量须为 1 到 500")
			return
		}
		filter.Limit = limit
	}
	entries := []diagnostics.Entry{}
	if s.diagnostics != nil {
		entries = s.diagnostics.Entries(filter)
	}
	w.Header().Set("Cache-Control", "no-store")
	s.writeJSON(w, http.StatusOK, map[string]any{"entries": entries})
}
