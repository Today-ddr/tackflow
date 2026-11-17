package api

import (
	"context"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

type statusWriter struct {
	http.ResponseWriter
	status int
}

type requestLogInfo struct {
	errorCode string
}

type requestLogInfoKey struct{}

func (w *statusWriter) Unwrap() http.ResponseWriter {
	return w.ResponseWriter
}

func (w *statusWriter) WriteHeader(status int) {
	w.status = status
	w.ResponseWriter.WriteHeader(status)
}

func (s *Server) withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := newRequestID()
		info := &requestLogInfo{}
		ctx := context.WithValue(r.Context(), requestIDKey, id)
		ctx = context.WithValue(ctx, requestLogInfoKey{}, info)
		r = r.WithContext(ctx)
		w.Header().Set("X-Request-Id", id)
		recorder := &statusWriter{ResponseWriter: w, status: http.StatusOK}
		start := time.Now()
		next.ServeHTTP(recorder, r)
		level := slog.LevelInfo
		if recorder.status >= http.StatusInternalServerError {
			level = slog.LevelError
		} else if recorder.status >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
		route := r.Pattern
		if route == "" {
			route = "GET /"
		} else if !strings.Contains(route, " ") {
			route = r.Method + " " + route
		}
		record := slog.NewRecord(time.Now(), level, "request", 0)
		record.AddAttrs(
			slog.String("request_id", id),
			slog.String("method", r.Method),
			slog.String("route", route),
			slog.Int("status", recorder.status),
			slog.Int64("duration_ms", time.Since(start).Milliseconds()),
		)
		if info.errorCode != "" {
			record.AddAttrs(slog.String("error_code", info.errorCode))
		}
		_ = s.logger.Handler().Handle(r.Context(), record)
	})
}

func setRequestErrorCode(r *http.Request, code string) {
	if info, ok := r.Context().Value(requestLogInfoKey{}).(*requestLogInfo); ok {
		info.errorCode = code
	}
}

func (s *Server) withSecurityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; img-src 'self' data:; style-src 'self' 'unsafe-inline'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}
