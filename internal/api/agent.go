package api

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/auth"
	"github.com/today-ddr/tackflow/internal/store"
)

type agentClaimDTO struct {
	ID        string `json:"id"`
	ExpiresAt string `json:"expires_at"`
}

func (s *Server) registerAgentRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/agent/next", s.requireAuth(s.requireScope(auth.ScopeRead, http.HandlerFunc(s.handleAgentNext))))
	mux.Handle("POST /api/v1/agent/claim", s.requireAuth(s.requireScope(auth.ScopeWrite, http.HandlerFunc(s.handleAgentClaim))))
	mux.Handle("POST /api/v1/agent/tasks/{id}/heartbeat", s.requireAuth(s.requireScope(auth.ScopeWrite, http.HandlerFunc(s.handleAgentHeartbeat))))
	mux.Handle("POST /api/v1/agent/tasks/{id}/result", s.requireAuth(s.requireScope(auth.ScopeWrite, http.HandlerFunc(s.handleAgentResult))))
}

func (s *Server) handleAgentNext(w http.ResponseWriter, r *http.Request) {
	if claim := r.URL.Query().Get("claim"); claim != "" && claim != "0" && claim != "false" {
		s.writeError(w, r, http.StatusBadRequest, "use_post_claim", "领取任务请使用 POST /api/v1/agent/claim")
		return
	}
	task, err := s.store.NextAgentTask(r.Context(), s.taskFilter(r))
	if err != nil {
		s.writeAgentError(w, r, err)
		return
	}
	var result *taskDTO
	if task != nil && s.taskAllowed(r, *task) {
		converted := taskDTOFromStore(*task)
		result = &converted
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"task": result})
}

func (s *Server) handleAgentClaim(w http.ResponseWriter, r *http.Request) {
	owner := s.agentOwner(r)
	var replay *store.AgentClaimReplay
	if key := strings.TrimSpace(r.Header.Get(idempotencyKeyHeader)); key != "" {
		if len(key) > 200 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_idempotency_key", "Idempotency-Key 过长")
			return
		}
		digest := sha256.Sum256([]byte(owner + "\x00" + r.Method + "\x00" + r.URL.Path + "\x00" + key))
		replay = &store.AgentClaimReplay{
			Key:  "v2:" + hex.EncodeToString(digest[:]),
			Seal: s.sealIdempotentBody,
			Open: s.openIdempotentBody,
		}
	}
	var request struct {
		Worker string `json:"worker"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	task, claim, err := s.store.ClaimAgentTask(r.Context(), s.taskFilter(r), owner, strings.TrimSpace(request.Worker), s.now().UTC(), replay)
	if err != nil {
		s.writeAgentError(w, r, err)
		return
	}
	if replay != nil && replay.Replayed {
		w.Header().Set("X-Tack-Idempotent-Replay", "true")
	}
	var taskResult *taskDTO
	var claimResult *agentClaimDTO
	if task != nil {
		converted := taskDTOFromStore(*task)
		taskResult = &converted
		convertedClaim := agentClaimFromStore(*claim)
		claimResult = &convertedClaim
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"task": taskResult, "claim": claimResult})
}

func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	if !s.requireTaskAccess(w, r, id) {
		return
	}
	var request struct {
		ClaimID string `json:"claim_id"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if request.ClaimID == "" || len(request.ClaimID) > 200 {
		s.writeAgentError(w, r, store.ErrInvalidAgentInput)
		return
	}
	claim, err := s.store.HeartbeatAgentTask(r.Context(), id, s.agentOwner(r), request.ClaimID, s.now().UTC())
	if err != nil {
		s.writeAgentError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"claim": agentClaimFromStore(claim)})
}

func (s *Server) handleAgentResult(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	if !s.requireTaskAccess(w, r, id) {
		return
	}
	var request store.AgentResultParams
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	task, err := s.store.CompleteAgentTask(r.Context(), id, s.agentOwner(r), request, s.now().UTC())
	if err != nil {
		s.writeAgentError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, taskDTOFromStore(task))
}

func (s *Server) agentOwner(r *http.Request) string {
	principal, ok := s.principal(r)
	if !ok {
		return ""
	}
	if principal.APIToken != nil {
		return "token:" + strconv.FormatInt(principal.APIToken.ID, 10)
	}
	if principal.Session != nil {
		return "session:" + principal.Session.ID
	}
	return ""
}

func (s *Server) writeAgentError(w http.ResponseWriter, r *http.Request, err error) {
	switch {
	case errors.Is(err, store.ErrAgentClaimLost):
		s.writeError(w, r, http.StatusConflict, "claim_lost", "任务租约已失效，请重新领取任务")
	case errors.Is(err, store.ErrInvalidAgentInput):
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_agent_input", "工作者、租约、结果状态、评论或外链不正确")
	case errors.Is(err, store.ErrAgentLinkConflict):
		s.writeError(w, r, http.StatusConflict, "link_conflict", "外部关联已属于其他任务")
	default:
		s.writeTaskError(w, r, err)
	}
}

func agentClaimFromStore(claim store.AgentClaim) agentClaimDTO {
	return agentClaimDTO{ID: claim.ID, ExpiresAt: claim.ExpiresAt.UTC().Format(time.RFC3339)}
}
