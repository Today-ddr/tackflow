package api

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

func (s *Server) registerEventRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/events", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleEvents))))
}

func (s *Server) handleEvents(writer http.ResponseWriter, request *http.Request) {
	poll := time.NewTicker(time.Second)
	heartbeat := time.NewTicker(15 * time.Second)
	defer poll.Stop()
	defer heartbeat.Stop()
	s.streamEvents(writer, request, poll.C, heartbeat.C)
}

func (s *Server) streamEvents(writer http.ResponseWriter, request *http.Request, poll, heartbeat <-chan time.Time) {
	if origin := request.Header.Get("Origin"); origin != "" && origin != s.publicOrigin() {
		s.writeError(writer, request, http.StatusForbidden, "origin_forbidden", "请求来源不合法")
		return
	}
	if site := request.Header.Get("Sec-Fetch-Site"); site != "" && site != "same-origin" && site != "none" {
		s.writeError(writer, request, http.StatusForbidden, "origin_forbidden", "请求来源不合法")
		return
	}
	if !eventResponseCanFlush(writer) {
		s.writeError(writer, request, http.StatusInternalServerError, "stream_unavailable", "事件流不可用")
		return
	}
	principal, ok := s.principal(request)
	if !ok || principal.Session == nil {
		s.writeError(writer, request, http.StatusUnauthorized, "unauthorized", "请先登录")
		return
	}
	sessionValid := func() bool {
		_, err := s.store.SessionByID(request.Context(), principal.Session.ID, s.now().UTC())
		return err == nil
	}
	if !sessionValid() {
		s.writeError(writer, request, http.StatusUnauthorized, "unauthorized", "登录已失效")
		return
	}
	latest, err := s.store.LatestChangeID(request.Context())
	if err != nil {
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "无法读取事件")
		return
	}
	cursor := latest
	rawCursor := request.Header.Get("Last-Event-ID")
	provided := rawCursor != ""
	if !provided && request.URL.Query().Has("cursor") {
		rawCursor = request.URL.Query().Get("cursor")
		provided = true
	}
	if provided {
		cursor, err = strconv.ParseInt(rawCursor, 10, 64)
		if err != nil || cursor < 0 || cursor > latest {
			s.writeError(writer, request, http.StatusBadRequest, "invalid_cursor", "事件游标不正确")
			return
		}
	}
	batch, err := s.store.ChangesAfter(request.Context(), cursor, 100)
	if err != nil {
		s.writeError(writer, request, http.StatusInternalServerError, "internal_error", "无法读取事件")
		return
	}
	controller := http.NewResponseController(writer)
	cancelledWrite := make(chan struct{})
	stop := context.AfterFunc(request.Context(), func() {
		defer close(cancelledWrite)
		_ = controller.SetWriteDeadline(time.Now())
	})
	defer func() {
		if !stop() {
			<-cancelledWrite
		}
	}()
	flush := func() bool {
		if err := controller.Flush(); err != nil {
			return false
		}
		_ = controller.SetWriteDeadline(time.Time{})
		return true
	}
	writer.Header().Set("Content-Type", "text/event-stream")
	writer.Header().Set("Cache-Control", "no-cache, no-store")
	writer.Header().Set("X-Accel-Buffering", "no")
	_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(writer, "id: %d\nevent: ready\ndata: {\"cursor\":%d}\n\n", cursor, cursor); err != nil || !flush() {
		return
	}
	for request.Context().Err() == nil {
		if !sessionValid() {
			return
		}
		if len(batch) > 0 {
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			for _, event := range batch {
				body, err := json.Marshal(event)
				if err != nil {
					return
				}
				if _, err := fmt.Fprintf(writer, "id: %d\nevent: change\ndata: %s\n\n", event.ID, body); err != nil {
					return
				}
				cursor = event.ID
			}
			if !flush() {
				return
			}
			if len(batch) == 100 {
				batch, err = s.store.ChangesAfter(request.Context(), cursor, 100)
				if err != nil {
					return
				}
				continue
			}
		}
		select {
		case <-request.Context().Done():
			return
		case <-heartbeat:
			if !sessionValid() {
				return
			}
			_ = controller.SetWriteDeadline(time.Now().Add(5 * time.Second))
			if _, err := io.WriteString(writer, ": heartbeat\n\n"); err != nil || !flush() {
				return
			}
		case <-poll:
		}
		batch, err = s.store.ChangesAfter(request.Context(), cursor, 100)
		if err != nil {
			return
		}
	}
}

func eventResponseCanFlush(writer http.ResponseWriter) bool {
	for range 32 {
		switch wrapped := writer.(type) {
		case interface{ FlushError() error }:
			return true
		case http.Flusher:
			return true
		case interface{ Unwrap() http.ResponseWriter }:
			writer = wrapped.Unwrap()
		default:
			return false
		}
	}
	return false
}
