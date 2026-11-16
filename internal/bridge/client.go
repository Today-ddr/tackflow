package bridge

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxResponseBytes = 4 << 20

type apiClient struct {
	base, token, name string
	http              *http.Client
}

type httpError struct {
	service, operation string
	status             int
}

func (failure *httpError) Error() string {
	return fmt.Sprintf("%s %s: HTTP %d", failure.service, failure.operation, failure.status)
}

func newClients(config Config) (apiClient, apiClient) {
	client := &http.Client{
		Timeout:       config.RequestTimeout,
		CheckRedirect: func(_ *http.Request, _ []*http.Request) error { return http.ErrUseLastResponse },
	}
	return apiClient{config.TackURL, config.TackToken, "Tack", client}, apiClient{config.CodegURL, config.CodegToken, "Codeg", client}
}

func (client apiClient) call(ctx context.Context, method, route, key string, payload, output any) error {
	var body []byte
	if payload != nil {
		var err error
		body, err = json.Marshal(payload)
		if err != nil || len(body) > 1<<20 {
			return fmt.Errorf("%s %s: invalid or oversized request", client.name, route)
		}
	}
	request, err := http.NewRequestWithContext(ctx, method, client.base+route, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%s: cannot construct request", client.name)
	}
	request.Header.Set("Authorization", "Bearer "+client.token)
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.GetBody = nil
	if key != "" {
		request.Header.Set("Idempotency-Key", key)
	}
	response, err := client.http.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return fmt.Errorf("%s %s: transport failure or timeout; remote outcome may be unknown", client.name, route)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &httpError{client.name, route, response.StatusCode}
	}
	data, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("%s %s: incomplete response", client.name, route)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("%s %s: response exceeds size limit", client.name, route)
	}
	if output == nil {
		return nil
	}
	if len(bytes.TrimSpace(data)) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return fmt.Errorf("%s %s: missing JSON response", client.name, route)
	}
	if err := json.Unmarshal(data, output); err != nil {
		return fmt.Errorf("%s %s: invalid JSON response", client.name, route)
	}
	return nil
}

func Check(ctx context.Context, config Config) error {
	if err := config.validate(); err != nil {
		return err
	}
	tack, codeg := newClients(config)
	var peek struct {
		Task json.RawMessage `json:"task"`
	}
	if err := tack.call(ctx, http.MethodGet, "/api/v1/agent/next", "", nil, &peek); err != nil {
		return err
	}
	if len(peek.Task) == 0 {
		return errors.New("tack agent/next: missing task field")
	}
	var health struct {
		Status string `json:"status"`
	}
	if err := codeg.call(ctx, http.MethodPost, "/api/health", "", struct{}{}, &health); err != nil {
		return err
	}
	if health.Status != "ok" {
		return errors.New("codeg health check did not report ok")
	}
	var folder struct {
		ID       int64  `json:"id"`
		ParentID *int64 `json:"parent_id"`
	}
	if err := codeg.call(ctx, http.MethodPost, "/api/get_folder", "", map[string]int64{"folderId": config.FolderID}, &folder); err != nil {
		return err
	}
	if folder.ID == config.FolderID && folder.ParentID == nil {
		var tasks []codegTask
		return codeg.call(ctx, http.MethodPost, "/api/work_task_list", "", map[string]int64{"folderId": config.FolderID}, &tasks)
	}
	return errors.New("CODEG_PROJECT_ID does not identify a live root project folder")
}

func redact(text string, config Config) string {
	for _, token := range []string{config.TackToken, config.CodegToken} {
		if token != "" {
			text = strings.ReplaceAll(text, token, "[redacted]")
		}
	}
	return strings.Map(func(character rune) rune {
		if character < 32 && character != '\n' && character != '\t' {
			return -1
		}
		return character
	}, text)
}
