package update

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

var ErrUpdaterNotConfigured = errors.New("updater is not configured")

type UpdaterState struct {
	CurrentTag  string `json:"current_tag"`
	PreviousTag string `json:"previous_tag"`
	Status      string `json:"status"`
	Message     string `json:"message"`
	UpdatedAt   string `json:"updated_at"`
	BackupPath  string `json:"backup_path"`
}

func ReadUpdaterState(dataDir string) (UpdaterState, bool, error) {
	path := filepath.Join(dataDir, "updater", "state.json")
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return UpdaterState{}, false, nil
	}
	if err != nil {
		return UpdaterState{}, false, fmt.Errorf("read updater state: %w", err)
	}
	var state UpdaterState
	if err := json.Unmarshal(body, &state); err != nil {
		return UpdaterState{}, false, fmt.Errorf("decode updater state: %w", err)
	}
	return state, true, nil
}

func WriteUpdaterState(dataDir string, state UpdaterState) error {
	dir := filepath.Join(dataDir, "updater")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := os.Chmod(dir, 0o755); err != nil {
		return err
	}
	state.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	body, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return err
	}
	temp, err := os.CreateTemp(dir, "state-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(0o644); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.Write(body); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Sync(); err != nil {
		_ = temp.Close()
		return err
	}
	if err := temp.Close(); err != nil {
		return err
	}
	return os.Rename(tempName, filepath.Join(dir, "state.json"))
}

type UpdaterClient struct {
	BaseURL string
	Token   string
	Client  *http.Client
}

func NewUpdaterClient(baseURL, token string) *UpdaterClient {
	return &UpdaterClient{
		BaseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		Token:   strings.TrimSpace(token),
		Client:  &http.Client{Timeout: 20 * time.Second},
	}
}

func (c *UpdaterClient) Apply(ctx context.Context, version string) (UpdaterState, error) {
	return c.call(ctx, "/apply", version)
}

func (c *UpdaterClient) Rollback(ctx context.Context) (UpdaterState, error) {
	return c.call(ctx, "/rollback", "")
}

func (c *UpdaterClient) Status(ctx context.Context) (UpdaterState, error) {
	if c == nil || c.BaseURL == "" || c.Token == "" {
		return UpdaterState{}, ErrUpdaterNotConfigured
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, c.BaseURL+"/state", nil)
	if err != nil {
		return UpdaterState{}, err
	}
	request.Header.Set("X-Updater-Token", c.Token)
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return UpdaterState{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return UpdaterState{}, fmt.Errorf("updater status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var state UpdaterState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		return UpdaterState{}, err
	}
	return state, nil
}

func (c *UpdaterClient) call(ctx context.Context, path, version string) (UpdaterState, error) {
	if c == nil || c.BaseURL == "" || c.Token == "" {
		return UpdaterState{}, ErrUpdaterNotConfigured
	}
	var body io.Reader
	if version != "" {
		encoded, err := json.Marshal(map[string]string{"version": version})
		if err != nil {
			return UpdaterState{}, err
		}
		body = bytes.NewReader(encoded)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+path, body)
	if err != nil {
		return UpdaterState{}, err
	}
	request.Header.Set("X-Updater-Token", c.Token)
	if version != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 20 * time.Second}
	}
	response, err := client.Do(request)
	if err != nil {
		return UpdaterState{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusAccepted && response.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(response.Body, 4096))
		return UpdaterState{}, fmt.Errorf("updater status %d: %s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var state UpdaterState
	if err := json.NewDecoder(response.Body).Decode(&state); err != nil {
		return UpdaterState{}, err
	}
	return state, nil
}

func ReleaseExists(ctx context.Context, repo, token, tag string) (bool, error) {
	if repo == "" {
		return true, nil
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		"https://api.github.com/repos/"+repo+"/releases/tags/"+tag,
		nil,
	)
	if err != nil {
		return false, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "tack-updater")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	client := &http.Client{Timeout: 15 * time.Second}
	response, err := client.Do(request)
	if err != nil {
		return false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotFound {
		return false, nil
	}
	if response.StatusCode != http.StatusOK {
		return false, fmt.Errorf("github release status %d", response.StatusCode)
	}
	return true, nil
}
