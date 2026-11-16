package api

import (
	"net/http"
	"strconv"
	"strings"
)

const updateIntervalKey = "update.interval_hours"

var validUpdateIntervals = map[int]struct{}{
	0:  {},
	6:  {},
	12: {},
	24: {},
	48: {},
}

type settingsResponse struct {
	UpdateIntervalHours int    `json:"update_interval_hours"`
	Timezone            string `json:"timezone"`
}

func (s *Server) handleGetSettings(w http.ResponseWriter, r *http.Request) {
	settings, err := s.readSettings(r)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取设置")
		return
	}
	s.writeJSON(w, http.StatusOK, settings)
}

func (s *Server) handlePatchSettings(w http.ResponseWriter, r *http.Request) {
	var request struct {
		UpdateIntervalHours *int `json:"update_interval_hours"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if request.UpdateIntervalHours != nil {
		if _, ok := validUpdateIntervals[*request.UpdateIntervalHours]; !ok {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_interval", "更新频率不受支持")
			return
		}
		if err := s.store.SetSetting(
			r.Context(),
			updateIntervalKey,
			strconv.Itoa(*request.UpdateIntervalHours),
		); err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法保存设置")
			return
		}
	}
	settings, err := s.readSettings(r)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取设置")
		return
	}
	s.writeJSON(w, http.StatusOK, settings)
}

func (s *Server) readSettings(r *http.Request) (settingsResponse, error) {
	interval := 24
	value, ok, err := s.store.GetSetting(r.Context(), updateIntervalKey)
	if err != nil {
		return settingsResponse{}, err
	}
	if ok {
		parsed, err := strconv.Atoi(strings.TrimSpace(value))
		if err == nil {
			if _, valid := validUpdateIntervals[parsed]; valid {
				interval = parsed
			}
		}
	}
	return settingsResponse{
		UpdateIntervalHours: interval,
		Timezone:            s.cfg.TimezoneName,
	}, nil
}
