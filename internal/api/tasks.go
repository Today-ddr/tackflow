package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/today/tack/internal/capture"
	"github.com/today/tack/internal/store"
	taskdomain "github.com/today/tack/internal/tasks"
)

type projectDTO struct {
	ID         int64   `json:"id"`
	Name       string  `json:"name"`
	Color      string  `json:"color"`
	Position   float64 `json:"position"`
	ArchivedAt *string `json:"archived_at"`
	CreatedAt  string  `json:"created_at"`
	TaskCount  int     `json:"task_count"`
}

type tagDTO struct {
	ID        int64  `json:"id"`
	Name      string `json:"name"`
	Color     string `json:"color"`
	CreatedAt string `json:"created_at"`
	TaskCount int    `json:"task_count"`
}

type taskDTO struct {
	ID          int64       `json:"id"`
	ProjectID   *int64      `json:"project_id"`
	Title       string      `json:"title"`
	Notes       string      `json:"notes"`
	Status      string      `json:"status"`
	Priority    int         `json:"priority"`
	Position    float64     `json:"position"`
	DueAt       *string     `json:"due_at"`
	AgentReady  bool        `json:"agent_ready"`
	CreatedAt   string      `json:"created_at"`
	UpdatedAt   string      `json:"updated_at"`
	CompletedAt *string     `json:"completed_at"`
	Project     *projectDTO `json:"project"`
	Tags        []tagDTO    `json:"tags"`
}

type eventDTO struct {
	ID        int64           `json:"id"`
	Kind      string          `json:"kind"`
	Payload   json.RawMessage `json:"payload"`
	CreatedAt string          `json:"created_at"`
}

type linkDTO struct {
	ID         int64   `json:"id"`
	Kind       string  `json:"kind"`
	ExternalID string  `json:"external_id"`
	URL        *string `json:"url"`
	CreatedAt  string  `json:"created_at"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	filter := store.TaskFilter{
		Query:  strings.TrimSpace(r.URL.Query().Get("q")),
		Cursor: strings.TrimSpace(r.URL.Query().Get("cursor")),
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("status")); raw != "" {
		for _, status := range strings.Split(raw, ",") {
			status = strings.TrimSpace(status)
			if !taskdomain.ValidStatus(status) {
				s.writeError(w, r, http.StatusBadRequest, "invalid_status", "任务状态不正确")
				return
			}
			filter.Statuses = append(filter.Statuses, status)
		}
	}
	var err error
	if raw := r.URL.Query().Get("project"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_project", "项目筛选不正确")
			return
		}
		filter.ProjectID = &value
	}
	if raw := r.URL.Query().Get("tag"); raw != "" {
		value, parseErr := strconv.ParseInt(raw, 10, 64)
		if parseErr != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_tag", "标签筛选不正确")
			return
		}
		filter.TagID = &value
	}
	if raw := r.URL.Query().Get("agent_ready"); raw != "" {
		value, parseErr := strconv.ParseBool(raw)
		if parseErr != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_agent_ready", "agent_ready 筛选不正确")
			return
		}
		filter.AgentReady = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("updated_since")); raw != "" {
		value, parseErr := time.Parse(time.RFC3339, raw)
		if parseErr != nil {
			s.writeError(w, r, http.StatusBadRequest, "invalid_updated_since", "updated_since 格式不正确")
			return
		}
		filter.UpdatedSince = &value
	}
	if raw := strings.TrimSpace(r.URL.Query().Get("limit")); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value <= 0 {
			s.writeError(w, r, http.StatusBadRequest, "invalid_limit", "limit 不正确")
			return
		}
		filter.Limit = value
	}

	items, nextCursor, err := s.store.ListTasks(r.Context(), filter)
	if err != nil {
		if errors.Is(err, store.ErrInvalidCursor) {
			s.writeError(w, r, http.StatusBadRequest, "invalid_cursor", "分页游标不正确")
			return
		}
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取任务")
		return
	}
	result := make([]taskDTO, 0, len(items))
	for _, item := range items {
		result = append(result, taskDTOFromStore(item))
	}
	var next any
	if nextCursor != "" {
		next = nextCursor
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"tasks":       result,
		"next_cursor": next,
	})
}

func (s *Server) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Title      string   `json:"title"`
		Notes      string   `json:"notes"`
		Status     string   `json:"status"`
		ProjectID  *int64   `json:"project_id"`
		Priority   int      `json:"priority"`
		Position   *float64 `json:"position"`
		DueAt      *string  `json:"due_at"`
		AgentReady bool     `json:"agent_ready"`
		Tags       []string `json:"tags"`
		TagIDs     []int64  `json:"tag_ids"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	title, err := taskdomain.ValidateTitle(request.Title)
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_title", err.Error())
		return
	}
	notes, err := taskdomain.ValidateNotes(request.Notes)
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_notes", err.Error())
		return
	}
	status := request.Status
	if status == "" {
		status = taskdomain.StatusInbox
	}
	if !taskdomain.ValidStatus(status) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_status", "任务状态不正确")
		return
	}
	if !taskdomain.ValidPriority(request.Priority) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_priority", "优先级不正确")
		return
	}
	dueAt, err := parseDueValue(request.DueAt, s.cfg.Timezone)
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_due_at", "截止时间不正确")
		return
	}
	if request.ProjectID != nil {
		if err := s.validateProject(r, *request.ProjectID); err != nil {
			s.writeProjectError(w, r, err)
			return
		}
	}
	tagIDs, err := s.resolveTags(r, request.Tags, request.TagIDs)
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tags", "标签不存在")
		return
	}
	item, err := s.store.CreateTask(r.Context(), store.CreateTaskParams{
		ProjectID:  request.ProjectID,
		Title:      title,
		Notes:      notes,
		Status:     status,
		Priority:   request.Priority,
		Position:   request.Position,
		DueAt:      dueAt,
		AgentReady: request.AgentReady,
		TagIDs:     tagIDs,
		Now:        s.now().UTC(),
	})
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法创建任务")
		return
	}
	s.writeJSON(w, http.StatusCreated, taskDTOFromStore(item))
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	item, err := s.store.TaskByID(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	events, err := s.store.TaskEvents(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取任务时间线")
		return
	}
	links, err := s.store.TaskLinks(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取任务关联")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{
		"task":   taskDTOFromStore(item),
		"events": eventDTOs(events),
		"links":  linkDTOs(links),
	})
}

func (s *Server) handlePatchTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	var request struct {
		Title      *string         `json:"title"`
		Notes      *string         `json:"notes"`
		ProjectID  json.RawMessage `json:"project_id"`
		Priority   *int            `json:"priority"`
		DueAt      json.RawMessage `json:"due_at"`
		AgentReady *bool           `json:"agent_ready"`
		Tags       json.RawMessage `json:"tags"`
		TagIDs     json.RawMessage `json:"tag_ids"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	params := store.UpdateTaskParams{}
	if request.Title != nil {
		title, err := taskdomain.ValidateTitle(*request.Title)
		if err != nil {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_title", err.Error())
			return
		}
		params.Title = &title
	}
	if request.Notes != nil {
		notes, err := taskdomain.ValidateNotes(*request.Notes)
		if err != nil {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_notes", err.Error())
			return
		}
		params.Notes = &notes
	}
	if request.Priority != nil {
		if !taskdomain.ValidPriority(*request.Priority) {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_priority", "优先级不正确")
			return
		}
		params.Priority = request.Priority
	}
	if request.AgentReady != nil {
		params.AgentReady = request.AgentReady
	}
	if len(request.ProjectID) > 0 {
		params.ProjectSet = true
		if isJSONNull(request.ProjectID) {
			params.ProjectID = nil
		} else {
			var projectID int64
			if err := json.Unmarshal(request.ProjectID, &projectID); err != nil {
				s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_project", "项目不正确")
				return
			}
			if err := s.validateProject(r, projectID); err != nil {
				s.writeProjectError(w, r, err)
				return
			}
			params.ProjectID = &projectID
		}
	}
	if len(request.DueAt) > 0 {
		params.DueAtSet = true
		if isJSONNull(request.DueAt) {
			params.DueAt = nil
		} else {
			var raw string
			if err := json.Unmarshal(request.DueAt, &raw); err != nil {
				s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_due_at", "截止时间不正确")
				return
			}
			dueAt, err := parseDueValue(&raw, s.cfg.Timezone)
			if err != nil {
				s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_due_at", "截止时间不正确")
				return
			}
			params.DueAt = dueAt
		}
	}
	if len(request.Tags) > 0 || len(request.TagIDs) > 0 {
		var names []string
		var ids []int64
		if len(request.Tags) > 0 && !isJSONNull(request.Tags) {
			if err := json.Unmarshal(request.Tags, &names); err != nil {
				s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tags", "标签不正确")
				return
			}
		}
		if len(request.TagIDs) > 0 && !isJSONNull(request.TagIDs) {
			if err := json.Unmarshal(request.TagIDs, &ids); err != nil {
				s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tags", "标签不正确")
				return
			}
		}
		tagIDs, err := s.resolveTags(r, names, ids)
		if err != nil {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tags", "标签不存在")
			return
		}
		params.TagsSet = true
		params.TagIDs = tagIDs
	}
	item, err := s.store.UpdateTask(r.Context(), id, params, s.now().UTC())
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, taskDTOFromStore(item))
}

func (s *Server) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	if err := s.store.DeleteTask(r.Context(), id, s.now().UTC()); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleMoveTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	var request struct {
		Status   string   `json:"status"`
		Position *float64 `json:"position"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if !taskdomain.ValidStatus(request.Status) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_status", "任务状态不正确")
		return
	}
	item, err := s.store.MoveTask(
		r.Context(), id, request.Status, request.Position, s.now().UTC(),
	)
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusOK, taskDTOFromStore(item))
}

func (s *Server) handleCommentTask(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	var request struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	request.Text = strings.TrimSpace(request.Text)
	if request.Text == "" || len([]rune(request.Text)) > 10_000 {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_comment", "评论内容不正确")
		return
	}
	event, err := s.store.AddComment(r.Context(), id, request.Text, s.now().UTC())
	if err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	s.writeJSON(w, http.StatusCreated, eventDTOFromStore(event))
}

func (s *Server) handleTaskEvents(w http.ResponseWriter, r *http.Request) {
	id, err := parseID(r)
	if err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "任务 ID 不正确")
		return
	}
	if _, err := s.store.TaskByID(r.Context(), id); err != nil {
		s.writeTaskError(w, r, err)
		return
	}
	events, err := s.store.TaskEvents(r.Context(), id)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取任务时间线")
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]any{"events": eventDTOs(events)})
}

func (s *Server) handleCapture(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Text string `json:"text"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		s.writeError(w, r, http.StatusBadRequest, "bad_request", "请求格式不正确")
		return
	}
	if strings.TrimSpace(request.Text) == "" {
		s.writeError(w, r, http.StatusUnprocessableEntity, "empty_capture", "记一笔不能为空")
		return
	}
	parsed, err := capture.Parse(request.Text, s.now().In(s.cfg.Timezone), s.cfg.Timezone)
	if err != nil {
		s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_capture", err.Error())
		return
	}
	var projectID *int64
	if parsed.Project != "" {
		project, err := s.store.ProjectByName(r.Context(), parsed.Project)
		if errors.Is(err, store.ErrNotFound) {
			s.writeError(w, r, http.StatusUnprocessableEntity, "unknown_project", "项目不存在")
			return
		}
		if err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法读取项目")
			return
		}
		projectID = &project.ID
	}
	tagIDs, err := s.resolveTags(r, parsed.Tags, nil)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法保存标签")
		return
	}
	item, err := s.store.CreateTask(r.Context(), store.CreateTaskParams{
		ProjectID: projectID,
		Title:     parsed.Title,
		Notes:     parsed.Notes,
		Status:    taskdomain.StatusInbox,
		Priority:  parsed.Priority,
		DueAt:     parsed.DueAt,
		TagIDs:    tagIDs,
		Now:       s.now().UTC(),
	})
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法记下这件事")
		return
	}
	s.writeJSON(w, http.StatusCreated, taskDTOFromStore(item))
}

func (s *Server) validateProject(r *http.Request, id int64) error {
	project, err := s.store.ProjectByID(r.Context(), id)
	if err != nil {
		return err
	}
	if project.ArchivedAt != nil {
		return fmt.Errorf("project is archived")
	}
	return nil
}

func (s *Server) resolveTags(r *http.Request, names []string, ids []int64) ([]int64, error) {
	result := make([]int64, 0, len(names)+len(ids))
	seenIDs := make(map[int64]struct{})
	seenNames := make(map[string]struct{})
	for _, name := range names {
		name = strings.TrimSpace(strings.TrimPrefix(name, "#"))
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, ok := seenNames[key]; ok {
			continue
		}
		seenNames[key] = struct{}{}
		tag, _, err := s.store.EnsureTag(
			r.Context(), name, taskdomain.ColorForName(name), s.now().UTC(),
		)
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[tag.ID]; !ok {
			seenIDs[tag.ID] = struct{}{}
			result = append(result, tag.ID)
		}
	}
	for _, id := range ids {
		tag, err := s.store.TagByID(r.Context(), id)
		if err != nil {
			return nil, err
		}
		if _, ok := seenIDs[tag.ID]; !ok {
			seenIDs[tag.ID] = struct{}{}
			result = append(result, tag.ID)
		}
	}
	return result, nil
}

func parseID(r *http.Request) (int64, error) {
	value, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("invalid id")
	}
	return value, nil
}

func parseDueValue(raw *string, location *time.Location) (*time.Time, error) {
	if raw == nil || strings.TrimSpace(*raw) == "" {
		return nil, nil
	}
	if location == nil {
		location = time.Local
	}
	value := strings.TrimSpace(*raw)
	if len(value) == len("2006-01-02") {
		parsed, err := time.ParseInLocation("2006-01-02", value, location)
		if err != nil {
			return nil, err
		}
		parsed = time.Date(parsed.Year(), parsed.Month(), parsed.Day(), 23, 59, 59, 0, location)
		if err := taskdomain.ValidateDueAt(&parsed); err != nil {
			return nil, err
		}
		return &parsed, nil
	}
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return nil, err
	}
	if err := taskdomain.ValidateDueAt(&parsed); err != nil {
		return nil, err
	}
	return &parsed, nil
}

func isJSONNull(raw json.RawMessage) bool {
	return strings.TrimSpace(string(raw)) == "null"
}

func (s *Server) writeTaskError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusNotFound, "task_not_found", "任务不存在")
		return
	}
	s.writeError(w, r, http.StatusInternalServerError, "internal_error", "任务操作失败")
}

func (s *Server) writeProjectError(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, store.ErrNotFound) {
		s.writeError(w, r, http.StatusUnprocessableEntity, "unknown_project", "项目不存在")
		return
	}
	s.writeError(w, r, http.StatusInternalServerError, "internal_error", "项目操作失败")
}

func taskDTOFromStore(item store.Task) taskDTO {
	result := taskDTO{
		ID:         item.ID,
		ProjectID:  item.ProjectID,
		Title:      item.Title,
		Notes:      item.Notes,
		Status:     item.Status,
		Priority:   item.Priority,
		Position:   item.Position,
		AgentReady: item.AgentReady,
		CreatedAt:  item.CreatedAt.Format(time.RFC3339),
		UpdatedAt:  item.UpdatedAt.Format(time.RFC3339),
		Tags:       make([]tagDTO, 0, len(item.Tags)),
	}
	if item.DueAt != nil {
		value := item.DueAt.Format(time.RFC3339)
		result.DueAt = &value
	}
	if item.CompletedAt != nil {
		value := item.CompletedAt.Format(time.RFC3339)
		result.CompletedAt = &value
	}
	if item.Project != nil {
		project := projectDTOFromStore(*item.Project)
		result.Project = &project
	}
	for _, tag := range item.Tags {
		result.Tags = append(result.Tags, tagDTOFromStore(tag))
	}
	return result
}

func projectDTOFromStore(project store.Project) projectDTO {
	result := projectDTO{
		ID:        project.ID,
		Name:      project.Name,
		Color:     project.Color,
		Position:  project.Position,
		CreatedAt: project.CreatedAt.Format(time.RFC3339),
		TaskCount: project.TaskCount,
	}
	if project.ArchivedAt != nil {
		value := project.ArchivedAt.Format(time.RFC3339)
		result.ArchivedAt = &value
	}
	return result
}

func tagDTOFromStore(tag store.Tag) tagDTO {
	return tagDTO{
		ID:        tag.ID,
		Name:      tag.Name,
		Color:     tag.Color,
		CreatedAt: tag.CreatedAt.Format(time.RFC3339),
		TaskCount: tag.TaskCount,
	}
}

func eventDTOs(events []store.TaskEvent) []eventDTO {
	result := make([]eventDTO, 0, len(events))
	for _, event := range events {
		result = append(result, eventDTOFromStore(event))
	}
	return result
}

func eventDTOFromStore(event store.TaskEvent) eventDTO {
	return eventDTO{
		ID:        event.ID,
		Kind:      event.Kind,
		Payload:   event.Payload,
		CreatedAt: event.CreatedAt.Format(time.RFC3339),
	}
}

func linkDTOs(links []store.TaskLink) []linkDTO {
	result := make([]linkDTO, 0, len(links))
	for _, link := range links {
		result = append(result, linkDTO{
			ID:         link.ID,
			Kind:       link.Kind,
			ExternalID: link.ExternalID,
			URL:        link.URL,
			CreatedAt:  link.CreatedAt.Format(time.RFC3339),
		})
	}
	return result
}
