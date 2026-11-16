package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/today/tack/internal/store"
	taskdomain "github.com/today/tack/internal/tasks"
)

func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	includeArchived := r.URL.Query().Get("include_archived") == "1" ||
		r.URL.Query().Get("include_archived") == "true"
	projects, err := s.store.ListProjects(r.Context(), includeArchived)
	if err == nil {
		projects, err = s.visibleProjects(r, projects)
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取项目")
		return
	}
	result := make([]projectDTO, 0, len(projects))
	for _, project := range projects {
		result = append(result, projectDTOFromStore(project))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"projects": result})
}

func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	name := strings.TrimSpace(request.Name)
	if name == "" || len([]rune(name)) > 80 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_name", "项目名称长度不正确")
		return
	}
	if _, err := s.store.ProjectByName(r.Context(), name); err == nil {
		s.writeError(w, r, http.StatusConflict, "project_exists", "项目已存在")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法检查项目")
		return
	}
	color := strings.TrimSpace(request.Color)
	if color == "" {
		color = taskdomain.ColorForName(name)
	}
	if !taskdomain.ValidColor(color) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_color", "项目颜色不正确")
		return
	}
	project, err := s.store.CreateProject(r.Context(), name, color, s.now().UTC())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法创建项目")
		return
	}
	s.writeJSON(w, http.StatusCreated, projectDTOFromStore(project))
}

func (s *Server) handlePatchProject(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "项目 ID 不正确")
		return
	}
	var request struct {
		Name     *string `json:"name"`
		Color    *string `json:"color"`
		Archived *bool   `json:"archived"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if request.Archived != nil && !s.requireSudo(w, r) {
		return
	}
	if request.Name != nil {
		name := strings.TrimSpace(*request.Name)
		if name == "" || len([]rune(name)) > 80 {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_name", "项目名称长度不正确")
			return
		}
		request.Name = &name
	}
	if request.Color != nil {
		color := strings.TrimSpace(*request.Color)
		if !taskdomain.ValidColor(color) {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_color", "项目颜色不正确")
			return
		}
		request.Color = &color
	}
	project, err := s.store.UpdateProject(
		r.Context(), id, request.Name, request.Color, request.Archived, s.now().UTC(),
	)
	if err != nil {
		s.writeProjectError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, projectDTOFromStore(project))
}

func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	if !s.requireSudo(w, r) {
		return
	}
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "项目 ID 不正确")
		return
	}
	if err := s.store.ArchiveProject(r.Context(), id, s.now().UTC()); err != nil {
		s.writeProjectError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
