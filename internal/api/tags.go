package api

import (
	"errors"
	"net/http"
	"strings"

	"github.com/today/tack/internal/store"
	taskdomain "github.com/today/tack/internal/tasks"
)

func (s *Server) handleListTags(w http.ResponseWriter, r *http.Request) {
	tags, err := s.store.ListTags(r.Context())
	if err == nil {
		tags, err = s.visibleTags(r, tags)
	}
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取标签")
		return
	}
	result := make([]tagDTO, 0, len(tags))
	for _, tag := range tags {
		result = append(result, tagDTOFromStore(tag))
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"tags": result})
}

func (s *Server) handleCreateTag(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Name  string `json:"name"`
		Color string `json:"color"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	name := strings.TrimSpace(strings.TrimPrefix(request.Name, "#"))
	if name == "" || len([]rune(name)) > 80 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_name", "标签名称长度不正确")
		return
	}
	if _, err := s.store.TagByName(r.Context(), name); err == nil {
		s.writeError(w, r, http.StatusConflict, "tag_exists", "标签已存在")
		return
	} else if !errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法检查标签")
		return
	}
	color := strings.TrimSpace(request.Color)
	if color == "" {
		color = taskdomain.ColorForName(name)
	}
	if !taskdomain.ValidColor(color) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_color", "标签颜色不正确")
		return
	}
	tag, err := s.store.CreateTag(r.Context(), name, color, s.now().UTC())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法创建标签")
		return
	}
	s.writeJSON(w, http.StatusCreated, tagDTOFromStore(tag))
}

func (s *Server) handleDeleteTag(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "标签 ID 不正确")
		return
	}
	if err := s.store.DeleteTag(r.Context(), id); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusNotFound, "tag_not_found", "标签不存在")
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法删除标签")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handlePatchTag(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "标签 ID 不正确")
		return
	}
	var request struct {
		Name  *string `json:"name"`
		Color *string `json:"color"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	tag, err := s.store.UpdateTag(r.Context(), id, request.Name, request.Color)
	switch {
	case errors.Is(err, store.ErrNotFound):
		s.writeError(w, r, http.StatusNotFound, "tag_not_found", "标签不存在")
	case errors.Is(err, store.ErrTagExists):
		s.writeError(w, r, http.StatusConflict, "tag_exists", "标签已存在")
	case errors.Is(err, store.ErrInvalidTag):
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tag", "标签名称或颜色不正确")
	case err != nil:
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法修改标签")
	default:
		s.writeJSON(w, http.StatusOK, tagDTOFromStore(tag))
	}
}
