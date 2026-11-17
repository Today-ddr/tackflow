package api

import (
	"net/http"
	"slices"

	"github.com/today-ddr/tackflow/internal/store"
)

func (s *Server) taskFilter(r *http.Request) store.TaskFilter {
	filter := store.TaskFilter{}
	if principal, ok := s.principal(r); ok && principal.APIToken != nil {
		filter.AllowedProjectIDs = principal.APIToken.ProjectIDs
		filter.AllowedTagIDs = principal.APIToken.TagIDs
	}
	return filter
}

func (s *Server) resourceLimited(r *http.Request) bool {
	filter := s.taskFilter(r)
	return len(filter.AllowedProjectIDs) > 0 || len(filter.AllowedTagIDs) > 0
}

func (s *Server) taskAllowed(r *http.Request, task store.Task) bool {
	filter := s.taskFilter(r)
	if len(filter.AllowedProjectIDs) > 0 && (task.ProjectID == nil || !slices.Contains(filter.AllowedProjectIDs, *task.ProjectID)) {
		return false
	}
	if len(filter.AllowedTagIDs) > 0 {
		for _, tag := range task.Tags {
			if slices.Contains(filter.AllowedTagIDs, tag.ID) {
				return true
			}
		}
		return false
	}
	return true
}

func (s *Server) requireTaskAccess(w http.ResponseWriter, r *http.Request, id int64) bool {
	task, err := s.store.TaskByID(r.Context(), id)
	if err != nil {
		s.writeTaskError(w, r, err)
		return false
	}
	if !s.taskAllowed(r, task) {
		s.writeTaskError(w, r, store.ErrNotFound)
		return false
	}
	return true
}

func (s *Server) resolveTaskMembership(w http.ResponseWriter, r *http.Request, project *int64, names []string, ids []int64) ([]int64, bool) {
	tagIDs, err := s.resolveTags(r, names, ids)
	if err != nil {
		if s.resourceLimited(r) {
			s.writeError(w, r, http.StatusForbidden, "resource_forbidden", "受限令牌只能使用已有标签")
		} else {
			s.writeError(w, r, http.StatusUnprocessableEntity, "invalid_tags", "标签不存在")
		}
		return nil, false
	}
	if !s.requireTaskMembership(w, r, project, tagIDs) {
		return nil, false
	}
	return tagIDs, true
}

func (s *Server) requireTaskMembership(w http.ResponseWriter, r *http.Request, project *int64, ids []int64) bool {
	if !s.resourceLimited(r) {
		return true
	}
	candidate := store.Task{ProjectID: project}
	for _, id := range ids {
		candidate.Tags = append(candidate.Tags, store.Tag{ID: id})
	}
	if !s.taskAllowed(r, candidate) {
		s.writeError(w, r, http.StatusForbidden, "resource_forbidden", "任务不在令牌允许的项目和标签范围内")
		return false
	}
	return true
}

func (s *Server) visibleProjects(r *http.Request, projects []store.Project) ([]store.Project, error) {
	if !s.resourceLimited(r) {
		return projects, nil
	}
	tasks, err := s.allTasks(r)
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int)
	for _, task := range tasks {
		if task.ProjectID != nil {
			counts[*task.ProjectID]++
		}
	}
	filter := s.taskFilter(r)
	result := make([]store.Project, 0)
	for _, project := range projects {
		if counts[project.ID] > 0 || slices.Contains(filter.AllowedProjectIDs, project.ID) {
			project.TaskCount = counts[project.ID]
			result = append(result, project)
		}
	}
	return result, nil
}

func (s *Server) visibleTags(r *http.Request, tags []store.Tag) ([]store.Tag, error) {
	if !s.resourceLimited(r) {
		return tags, nil
	}
	tasks, err := s.allTasks(r)
	if err != nil {
		return nil, err
	}
	counts := make(map[int64]int)
	for _, task := range tasks {
		for _, tag := range task.Tags {
			counts[tag.ID]++
		}
	}
	filter := s.taskFilter(r)
	result := make([]store.Tag, 0)
	for _, tag := range tags {
		if counts[tag.ID] > 0 || slices.Contains(filter.AllowedTagIDs, tag.ID) {
			tag.TaskCount = counts[tag.ID]
			result = append(result, tag)
		}
	}
	return result, nil
}
