package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/today/tack/internal/update"
)

type updateStatusResponse struct {
	CurrentVersion  string               `json:"current_version"`
	LatestVersion   *string              `json:"latest_version"`
	UpdateAvailable bool                 `json:"update_available"`
	IntervalHours   int                  `json:"interval_hours"`
	LastCheckedAt   *string              `json:"last_checked_at"`
	LastError       string               `json:"last_error"`
	ReleaseBody     string               `json:"release_body"`
	ReleaseURL      string               `json:"release_url"`
	PublishedAt     *string              `json:"published_at"`
	Configured      bool                 `json:"configured"`
	Updater         *update.UpdaterState `json:"updater,omitempty"`
}

func (s *Server) handleUpdateStatus(w http.ResponseWriter, r *http.Request) {
	status, err := s.updateStatus(r)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取更新状态")
		return
	}
	s.writeJSON(w, http.StatusOK, status)
}

func (s *Server) handleUpdateCheck(w http.ResponseWriter, r *http.Request) {
	if s.updateSvc == nil {
		s.writeUpdateUnavailable(w, r)
		return
	}
	status, err := s.updateSvc.CheckNow(r.Context())
	if err != nil {
		switch {
		case errors.Is(err, update.ErrNotConfigured):
			s.writeError(w, r, http.StatusUnprocessableEntity, "update_not_configured", "尚未配置 GitHub 仓库")
		case errors.Is(err, update.ErrRateLimited):
			s.writeError(w, r, http.StatusTooManyRequests, "update_rate_limited", "检查太频繁，请稍后再试")
		default:
			s.writeError(w, r, http.StatusBadGateway, "update_check_failed", "检查更新失败")
		}
		return
	}
	response := updateStatusResponseFrom(status)
	if updater, ok := s.readUpdaterState(); ok {
		response.Updater = &updater
	}
	s.writeJSON(w, http.StatusOK, response)
}

func (s *Server) handleUpdateApply(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Version  string `json:"version"`
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !s.confirmSudo(w, r, request.Password) {
		return
	}
	request.Version = strings.TrimSpace(request.Version)
	if !update.IsReleaseTag(request.Version) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_version", "版本号不正确")
		return
	}
	newer, err := update.IsNewer(s.build.Version, request.Version)
	if err != nil || !newer {
		s.writeError(w, r, http.StatusUnprocessableEntity, "version_not_newer", "目标版本必须高于当前版本")
		return
	}
	if s.updater == nil {
		s.writeUpdateUnavailable(w, r)
		return
	}
	state, err := s.updater.Apply(r.Context(), request.Version)
	if err != nil {
		if errors.Is(err, update.ErrUpdaterNotConfigured) {
			s.writeUpdateUnavailable(w, r)
			return
		}
		s.writeError(w, r, http.StatusBadGateway, "update_apply_failed", "无法启动更新")
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"updater": state})
}

func (s *Server) handleUpdateRollback(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Password string `json:"password"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !s.confirmSudo(w, r, request.Password) {
		return
	}
	if s.updater == nil {
		s.writeUpdateUnavailable(w, r)
		return
	}
	state, err := s.updater.Rollback(r.Context())
	if err != nil {
		if errors.Is(err, update.ErrUpdaterNotConfigured) {
			s.writeUpdateUnavailable(w, r)
			return
		}
		s.writeError(w, r, http.StatusBadGateway, "rollback_failed", "无法启动回滚")
		return
	}
	s.writeJSON(w, http.StatusAccepted, map[string]any{"updater": state})
}

func (s *Server) updateStatus(r *http.Request) (updateStatusResponse, error) {
	var status update.Status
	var err error
	if s.updateSvc != nil {
		status, err = s.updateSvc.Status(r.Context())
		if err != nil {
			return updateStatusResponse{}, err
		}
	} else {
		status = update.Status{
			CurrentVersion: s.build.Version,
			IntervalHours:  24,
		}
	}
	response := updateStatusResponseFrom(status)
	if updater, ok := s.readUpdaterState(); ok {
		response.Updater = &updater
	}
	return response, nil
}

func updateStatusResponseFrom(status update.Status) updateStatusResponse {
	return updateStatusResponse{
		CurrentVersion:  status.CurrentVersion,
		LatestVersion:   status.LatestVersion,
		UpdateAvailable: status.UpdateAvailable,
		IntervalHours:   status.IntervalHours,
		LastCheckedAt:   status.LastCheckedAt,
		LastError:       status.LastError,
		ReleaseBody:     status.ReleaseBody,
		ReleaseURL:      status.ReleaseURL,
		PublishedAt:     status.PublishedAt,
		Configured:      status.Configured,
	}
}

func (s *Server) readUpdaterState() (update.UpdaterState, bool) {
	state, ok, err := update.ReadUpdaterState(s.cfg.DataDir)
	if err != nil {
		return update.UpdaterState{}, false
	}
	return state, ok
}

func (s *Server) confirmSudo(w http.ResponseWriter, r *http.Request, password string) bool {
	authValue, ok := s.principal(r)
	if !ok || authValue.Session == nil {
		s.writeError(w, r, http.StatusForbidden, "web_only", "该操作只能在网页中使用")
		return false
	}
	if authValue.Session.SudoUntil != nil && authValue.Session.SudoUntil.After(s.now().UTC()) {
		return true
	}
	if strings.TrimSpace(password) == "" {
		s.writeError(w, r, http.StatusForbidden, "sudo_required", "请重新输入密码")
		return false
	}
	if !s.userPasswordMatches(r, password) {
		s.writeError(w, r, http.StatusForbidden, "invalid_password", "密码不正确")
		return false
	}
	until := s.now().UTC().Add(5 * time.Minute)
	if err := s.store.SetSessionSudo(r.Context(), authValue.Session.ID, until); err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法开启 sudo 模式")
		return false
	}
	return true
}

func (s *Server) writeUpdateUnavailable(w http.ResponseWriter, r *http.Request) {
	s.writeError(w, r, http.StatusServiceUnavailable, "update_unavailable", "更新服务尚未配置")
}
