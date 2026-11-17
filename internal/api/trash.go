package api

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/today-ddr/tackflow/internal/store"
)

type trashTaskDTO struct {
	taskDTO
	DeletedAt string `json:"deleted_at"`
}

func (s *Server) registerTrashRoutes(mux *http.ServeMux) {
	mux.Handle("GET /api/v1/trash", s.requireAuth(s.requireWeb(http.HandlerFunc(s.handleListTrash))))
	mux.Handle("POST /api/v1/tasks/{id}/restore", s.requireAuth(s.requireWeb(s.withIdempotency(http.HandlerFunc(s.handleRestoreTask)))))
}

func (s *Server) handleListTrash(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 1 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "分页数量不正确")
			return
		}
		limit = parsed
	}
	items, cursor, err := s.store.ListTrash(r.Context(), limit, strings.TrimSpace(r.URL.Query().Get("cursor")), s.now().UTC())
	if errors.Is(err, store.ErrInvalidCursor) {
		s.writeError(w, r, http.StatusBadRequest, "invalid_cursor", "分页游标不正确")
		return
	}
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	result := make([]trashTaskDTO, 0, len(items))
	for _, task := range items {
		result = append(result, trashTaskDTO{taskDTO: taskDTOFromStore(task), DeletedAt: task.DeletedAt.UTC().Format(time.RFC3339)})
	}
	var next *string
	if cursor != "" {
		next = &cursor
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"tasks": result, "next_cursor": next})
}

func (s *Server) handleRestoreTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	task, err := s.store.RestoreTask(r.Context(), id, s.now().UTC())
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, taskDTOFromStore(task))
}
