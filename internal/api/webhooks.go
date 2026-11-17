package api

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/events"
	"github.com/today-ddr/tackflow/internal/store"
)

func (s *Server) registerWebhookRoutes(mux *http.ServeMux) {
	web := func(handler http.HandlerFunc) http.Handler {
		return s.requireAuth(s.requireWeb(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
			writer.Header().Set("Cache-Control", "no-store")
			handler(writer, request)
		})))
	}
	mux.Handle("GET /api/v1/webhooks", web(s.handleListWebhooks))
	mux.Handle("POST /api/v1/webhooks", web(s.handleCreateWebhook))
	mux.Handle("PATCH /api/v1/webhooks/{id}", web(s.handlePatchWebhook))
	mux.Handle("DELETE /api/v1/webhooks/{id}", web(s.handleDeleteWebhook))
	mux.Handle("GET /api/v1/webhooks/{id}/deliveries", web(s.handleWebhookDeliveries))
}

func (s *Server) StartWebhookDelivery(ctx context.Context) {
	worker := events.NewWorker(s.store, s.cfg.WebhookAllowPrivate, s.now)
	go worker.Run(ctx)
}

func (s *Server) handleListWebhooks(writer http.ResponseWriter, request *http.Request) {
	hooks, err := s.store.ListWebhooks(request.Context())
	if err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"webhooks": hooks})
}

func (s *Server) handleCreateWebhook(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Name     string   `json:"name"`
		URL      string   `json:"url"`
		Events   []string `json:"events"`
		Password string   `json:"password"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !s.confirmSudo(writer, request, body.Password) {
		return
	}
	body.Name = strings.TrimSpace(body.Name)
	if !s.validateWebhook(writer, request, body.Name, body.URL, body.Events, true) {
		return
	}
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	secret := base64.RawURLEncoding.EncodeToString(random)
	hook, err := s.store.CreateWebhook(request.Context(), body.Name, body.URL, body.Events, secret, s.now().UTC())
	if err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusCreated, map[string]any{"webhook": hook, "secret": secret})
}

func (s *Server) handlePatchWebhook(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Name     *string   `json:"name"`
		URL      *string   `json:"url"`
		Events   *[]string `json:"events"`
		Enabled  *bool     `json:"enabled"`
		Password string    `json:"password"`
	}
	if err := decodeJSON(writer, request, &body); err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !s.confirmSudo(writer, request, body.Password) {
		return
	}
	id, err := parseID(request)
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "bad_request", "Webhook ID 不正确")
		return
	}
	hook, err := s.store.WebhookByID(request.Context(), id)
	if err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	wasEnabled := hook.Enabled
	if body.Name != nil {
		hook.Name = strings.TrimSpace(*body.Name)
	}
	if body.URL != nil {
		hook.URL = *body.URL
	}
	if body.Events != nil {
		hook.Events = *body.Events
	}
	if body.Enabled != nil {
		hook.Enabled = *body.Enabled
	}
	if !s.validateWebhook(writer, request, hook.Name, hook.URL, hook.Events, body.URL != nil || (!wasEnabled && hook.Enabled)) {
		return
	}
	if err := s.store.UpdateWebhook(request.Context(), id, hook.Name, hook.URL, hook.Events, hook.Enabled); err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	hook, err = s.store.WebhookByID(request.Context(), id)
	if err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"webhook": hook})
}

func (s *Server) handleDeleteWebhook(writer http.ResponseWriter, request *http.Request) {
	var body struct {
		Password string `json:"password"`
	}
	if request.ContentLength != 0 {
		if err := decodeJSON(writer, request, &body); err != nil && !errors.Is(err, io.EOF) {
			s.writeError(writer, request, http.StatusBadRequest, "bad_request", "请求格式不正确")
			return
		}
	}
	if !s.confirmSudo(writer, request, body.Password) {
		return
	}
	id, err := parseID(request)
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "bad_request", "Webhook ID 不正确")
		return
	}
	if err := s.store.DeleteWebhook(request.Context(), id); err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	writer.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleWebhookDeliveries(writer http.ResponseWriter, request *http.Request) {
	id, err := parseID(request)
	if err != nil {
		s.writeError(writer, request, http.StatusBadRequest, "bad_request", "Webhook ID 不正确")
		return
	}
	deliveries, err := s.store.WebhookDeliveries(request.Context(), id)
	if err != nil {
		s.writeWebhookError(writer, request, err)
		return
	}
	s.writeJSON(writer, http.StatusOK, map[string]any{"deliveries": deliveries})
}

func (s *Server) validateWebhook(writer http.ResponseWriter, request *http.Request, name, target string, subscriptions []string, checkURL bool) bool {
	if name == "" || len([]rune(name)) > 80 {
		s.writeError(writer, request, http.StatusUnprocessableEntity, "invalid_name", "Webhook 名称长度不正确")
		return false
	}
	valid := len(subscriptions) > 0 && len(subscriptions) <= 4
	seen := make(map[string]bool)
	for _, subscription := range subscriptions {
		if seen[subscription] || (subscription != "*" && subscription != "task.*" && subscription != "project.*" && subscription != "tag.*") {
			valid = false
		}
		seen[subscription] = true
	}
	if !valid {
		s.writeError(writer, request, http.StatusUnprocessableEntity, "invalid_events", "Webhook 事件订阅不正确")
		return false
	}
	if checkURL {
		ctx, cancel := context.WithTimeout(request.Context(), 5*time.Second)
		defer cancel()
		if _, err := events.NewURLPolicy(s.cfg.WebhookAllowPrivate).Validate(ctx, target); err != nil {
			s.writeError(writer, request, http.StatusUnprocessableEntity, "invalid_url", "Webhook 地址不允许或无法解析")
			return false
		}
	}
	return true
}

func (s *Server) writeWebhookError(writer http.ResponseWriter, request *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(writer, request, http.StatusNotFound, "webhook_not_found", "Webhook 不存在")
		return
	}
	s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "Webhook 操作失败")
}
