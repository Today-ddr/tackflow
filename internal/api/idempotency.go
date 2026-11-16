package api

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
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

func (s *Server) withIdempotency(next http.Handler, authorizeReplay ...func(http.ResponseWriter, *http.Request) bool) http.Handler {
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
		principal, authorized := s.principal(r)
		if !authorized {
			s.writeError(w, r, http.StatusUnauthorized, "unauthorized", "请先登录")
			return
		}
		owner := ""
		if principal.APIToken != nil {
			owner = fmt.Sprintf("token:%d", principal.APIToken.ID)
		} else if principal.Session != nil {
			owner = "session:" + principal.Session.ID
		}
		digest := sha256.Sum256([]byte(owner + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + key))
		key = "v2:" + hex.EncodeToString(digest[:])
		cached, ok, err := s.store.GetIdempotency(r.Context(), key, s.now().UTC())
		if err != nil {
			s.logger.Error("read idempotency key", "error", err)
			s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取幂等结果，请稍后重试")
			return
		}
		if ok {
			if cached.Status == 0 {
				s.writeError(w, r, http.StatusConflict, "idempotency_uncertain", "上次操作结果尚未确认，请先查询当前状态，避免重复操作")
				return
			}
			for _, authorize := range authorizeReplay {
				if !authorize(w, r) {
					return
				}
			}
			body, err := s.openIdempotentBody(key, cached.Body)
			if err != nil {
				s.writeError(w, r, http.StatusInternalServerError, "internal_error", "幂等结果无法验证，请检查服务配置")
				return
			}
			cached.Body = body
			if s.resourceLimited(r) && len(cached.Body) > 0 {
				id := int64(0)
				if r.URL.Path == "/api/v1/tasks" || r.URL.Path == "/api/v1/capture" {
					var item struct {
						ID int64 `json:"id"`
					}
					if json.Unmarshal(cached.Body, &item) == nil {
						id = item.ID
					}
				} else if strings.HasPrefix(r.URL.Path, "/api/v1/tasks/") || strings.HasPrefix(r.URL.Path, "/api/v1/agent/tasks/") {
					id, _ = parseID(r)
				} else if r.URL.Path == "/api/v1/agent/claim" {
					var claim struct {
						Task struct {
							ID int64 `json:"id"`
						} `json:"task"`
					}
					if json.Unmarshal(cached.Body, &claim) == nil {
						id = claim.Task.ID
					}
				}
				if id > 0 && !s.requireTaskAccess(w, r, id) {
					return
				}
			}
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

		reserved, err := s.store.ReserveIdempotency(r.Context(), key, s.now().UTC())
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法记录幂等请求，请稍后重试")
			return
		}
		if !reserved {
			s.writeError(w, r, http.StatusConflict, "idempotency_uncertain", "相同请求正在处理，请稍后查询结果")
			return
		}
		captured := newCapturedResponse()
		next.ServeHTTP(captured, r)
		persistContext, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 5*time.Second)
		defer cancel()
		if captured.status >= 200 && captured.status < 300 {
			sealed, err := s.sealIdempotentBody(key, captured.body.Bytes())
			if err != nil {
				s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法保存幂等结果")
				return
			}
			if err := s.store.PutIdempotency(
				persistContext,
				key,
				captured.status,
				sealed,
				s.now().UTC(),
			); err != nil {
				s.logger.Error("store idempotency key", "error", err)
				s.writeError(w, r, http.StatusInternalServerError, "idempotency_uncertain", "操作结果未能确认，请先查询当前状态，避免重复操作")
				return
			}
		} else if captured.status >= 400 && captured.status < 500 {
			if err := s.store.DeleteIdempotency(persistContext, key); err != nil {
				s.logger.Error("release rejected idempotency key", "error", err)
			}
		}
		captured.flushTo(w)
	})
}

func (s *Server) idempotentCipher() (cipher.AEAD, error) {
	key := sha256.Sum256(append([]byte("tack:idempotency:v1:"), s.cfg.SessionSecret...))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCMWithRandomNonce(block)
}

func (s *Server) sealIdempotentBody(key string, body []byte) ([]byte, error) {
	encryption, err := s.idempotentCipher()
	if err != nil {
		return nil, err
	}
	sealed := encryption.Seal(nil, nil, body, []byte(key))
	return []byte(base64.RawStdEncoding.EncodeToString(sealed)), nil
}

func (s *Server) openIdempotentBody(key string, body []byte) ([]byte, error) {
	sealed, err := base64.RawStdEncoding.DecodeString(string(body))
	if err != nil {
		return nil, err
	}
	encryption, err := s.idempotentCipher()
	if err != nil {
		return nil, err
	}
	return encryption.Open(nil, nil, sealed, []byte(key))
}
