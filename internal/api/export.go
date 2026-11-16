package api

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/today/tack/internal/store"
)

type exportDocument struct {
	Version    string       `json:"version"`
	ExportedAt string       `json:"exported_at"`
	Projects   []projectDTO `json:"projects"`
	Tags       []tagDTO     `json:"tags"`
	Tasks      []taskDTO    `json:"tasks"`
}

func (s *Server) handleExport(w http.ResponseWriter, r *http.Request) {
	projects, err := s.store.ListProjects(r.Context(), true)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法导出项目")
		return
	}
	tags, err := s.store.ListTags(r.Context())
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法导出标签")
		return
	}
	tasks, err := s.allTasks(r)
	if err != nil {
		s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法导出任务")
		return
	}

	format := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("format")))
	if format == "" {
		format = "json"
	}
	filename := "tack-" + s.now().In(s.cfg.Timezone).Format("20060102-150405")
	switch format {
	case "json":
		document := exportDocument{
			Version:    s.build.Version,
			ExportedAt: s.now().UTC().Format(time.RFC3339),
			Projects:   make([]projectDTO, 0, len(projects)),
			Tags:       make([]tagDTO, 0, len(tags)),
			Tasks:      make([]taskDTO, 0, len(tasks)),
		}
		for _, project := range projects {
			document.Projects = append(document.Projects, projectDTOFromStore(project))
		}
		for _, tag := range tags {
			document.Tags = append(document.Tags, tagDTOFromStore(tag))
		}
		for _, task := range tasks {
			document.Tasks = append(document.Tasks, taskDTOFromStore(task))
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.json"`, filename))
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		encoder := json.NewEncoder(w)
		encoder.SetIndent("", "  ")
		_ = encoder.Encode(document)
	case "csv":
		var buffer bytes.Buffer
		writer := csv.NewWriter(&buffer)
		_ = writer.Write([]string{
			"id", "title", "notes", "status", "priority", "project", "tags",
			"due_at", "agent_ready", "created_at", "updated_at", "completed_at",
		})
		for _, task := range tasks {
			projectName := ""
			if task.Project != nil {
				projectName = task.Project.Name
			}
			tagNames := make([]string, 0, len(task.Tags))
			for _, tag := range task.Tags {
				tagNames = append(tagNames, tag.Name)
			}
			_ = writer.Write([]string{
				fmt.Sprintf("%d", task.ID),
				task.Title,
				task.Notes,
				task.Status,
				fmt.Sprintf("%d", task.Priority),
				projectName,
				strings.Join(tagNames, ","),
				formatOptionalTime(task.DueAt),
				fmt.Sprintf("%t", task.AgentReady),
				task.CreatedAt.Format(time.RFC3339),
				task.UpdatedAt.Format(time.RFC3339),
				formatOptionalTime(task.CompletedAt),
			})
		}
		writer.Flush()
		if err := writer.Error(); err != nil {
			s.writeError(w, r, http.StatusInternalServerError, "internal_error", "无法生成 CSV")
			return
		}
		w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s.csv"`, filename))
		w.Header().Set("Content-Type", "text/csv; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(buffer.Bytes())
	default:
		s.writeError(w, r, http.StatusBadRequest, "invalid_format", "导出格式不支持")
	}
}

func (s *Server) allTasks(r *http.Request) ([]store.Task, error) {
	result := make([]store.Task, 0)
	cursor := ""
	for {
		items, next, err := s.store.ListTasks(r.Context(), store.TaskFilter{
			Limit:  500,
			Cursor: cursor,
		})
		if err != nil {
			return nil, err
		}
		result = append(result, items...)
		if next == "" {
			return result, nil
		}
		cursor = next
	}
}

func formatOptionalTime(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format(time.RFC3339)
}
