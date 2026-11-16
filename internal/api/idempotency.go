package api

import (
	"bytes"
	"net/http"
	"strings"
)

const idempotencyKeyHeader = "Idempotency-Key"

type capturedResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newCapturedResponse() *capturedResponse {
	return &capturedResponse{header: make(http.Header), status: http.StatusOK}
}

func (w *capturedResponse) Header() http.Header {
	return w.header
}

func (w *capturedResponse) WriteHeader(status int) {
	if w.status != http.StatusOK || w.body.Len() > 0 {
		return
	}
	w.status = status
}

func (w *capturedResponse) Write(data []byte) (int, error) {
	return w.body.Write(data)
}

func (w *capturedResponse) flushTo(target http.ResponseWriter) {
	for key, values := range w.header {
		target.Header()[key] = values
	}
	target.WriteHeader(w.status)
	if w.body.Len() > 0 {
		_, _ = target.Write(w.body.Bytes())
	}
}

func (s *Server) withIdempotency(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader))
		if key == "" {
			next.ServeHTTP(w, r)
			return
		}
		if len(key) > 200 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key 过长")
			return
		}
		cached, ok, err := s.store.GetIdempotency(r.Context(), key, s.now().UTC())
		if err != nil {
			s.logger.Error("read idempotency key", "error", err)
			next.ServeHTTP(w, r)
			return
		}
		if ok {
			w.Header().Set("X-Tack-Idempotent-Replay", "true")
			if len(cached.Body) > 0 && w.Header().Get("Content-Type") == "" {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
			}
			w.WriteHeader(cached.Status)
			if len(cached.Body) > 0 {
				_, _ = w.Write(cached.Body)
			}
			return
		}

		captured := newCapturedResponse()
		next.ServeHTTP(captured, r)
		if captured.status >= 200 && captured.status < 300 {
			if err := s.store.PutIdempotency(
				r.Context(),
				key,
				captured.status,
				captured.body.Bytes(),
				s.now().UTC(),
			); err != nil {
				s.logger.Error("store idempotency key", "error", err)
			}
		}
		captured.flushTo(w)
	})
}
