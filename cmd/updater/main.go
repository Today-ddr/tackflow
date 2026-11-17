package main

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/today-ddr/tackflow/internal/update"
)

type config struct {
	Addr        string
	Token       string
	ComposeFile string
	EnvFile     string
	Service     string
	Repo        string
	GitHubToken string
	DataDir     string
	ProjectName string
}

type updater struct {
	cfg   config
	log   *slog.Logger
	mu    sync.Mutex
	state update.UpdaterState
}

func main() {
	cfg := loadConfig()
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	state, ok, err := update.ReadUpdaterState(cfg.DataDir)
	if err != nil {
		logger.Warn("read updater state", "error", err)
	}
	if !ok {
		state.CurrentTag = readEnvValue(cfg.EnvFile, "TACK_IMAGE_TAG")
	}
	app := &updater{cfg: cfg, log: logger, state: state}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", app.requireToken(app.handleHealth))
	mux.HandleFunc("GET /state", app.requireToken(app.handleState))
	mux.HandleFunc("POST /apply", app.requireToken(app.handleApply))
	mux.HandleFunc("POST /rollback", app.requireToken(app.handleRollback))

	server := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}
	logger.Info("updater listening", "addr", cfg.Addr)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("updater stopped", "error", err)
		os.Exit(1)
	}
}

func loadConfig() config {
	return config{
		Addr:        envOr("TACK_UPDATER_ADDR", ":8081"),
		Token:       os.Getenv("TACK_UPDATER_TOKEN"),
		ComposeFile: envOr("TACK_COMPOSE_FILE", "/compose/docker-compose.yml"),
		EnvFile:     envOr("TACK_ENV_FILE", "/compose/.env"),
		Service:     envOr("TACK_SERVICE", "app"),
		Repo:        os.Getenv("TACK_GITHUB_REPO"),
		GitHubToken: os.Getenv("TACK_GITHUB_TOKEN"),
		DataDir:     envOr("TACK_DATA_DIR", "/data"),
		ProjectName: envOr("COMPOSE_PROJECT_NAME", "tack"),
	}
}

func (u *updater) requireToken(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		provided := r.Header.Get("X-Updater-Token")
		if u.cfg.Token == "" || subtle.ConstantTimeCompare([]byte(provided), []byte(u.cfg.Token)) != 1 {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
			return
		}
		next(w, r)
	}
}

func (u *updater) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (u *updater) handleState(w http.ResponseWriter, _ *http.Request) {
	u.mu.Lock()
	state := u.state
	u.mu.Unlock()
	writeJSON(w, http.StatusOK, state)
}

func (u *updater) handleApply(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Version string `json:"version"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&request); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "bad_request"})
		return
	}
	request.Version = strings.TrimSpace(request.Version)
	if !update.IsReleaseTag(request.Version) || !releaseRegexp.MatchString(request.Version) {
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"error": "invalid_version"})
		return
	}
	if !u.begin() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "busy"})
		return
	}
	state := u.startState("updating", request.Version)
	go u.apply(request.Version)
	writeJSON(w, http.StatusAccepted, state)
}

func (u *updater) handleRollback(w http.ResponseWriter, _ *http.Request) {
	if !u.begin() {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "busy"})
		return
	}
	state := u.markState("rolling_back")
	go u.rollback()
	writeJSON(w, http.StatusAccepted, state)
}

func (u *updater) begin() bool {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.state.Status == "updating" || u.state.Status == "rolling_back" {
		return false
	}
	return true
}

func (u *updater) startState(status, nextTag string) update.UpdaterState {
	u.mu.Lock()
	previous := u.state.CurrentTag
	u.state.PreviousTag = previous
	u.state.Status = status
	u.state.Message = ""
	state := u.state
	u.mu.Unlock()
	_ = update.WriteUpdaterState(u.cfg.DataDir, state)
	return state
}

func (u *updater) markState(status string) update.UpdaterState {
	u.mu.Lock()
	u.state.Status = status
	u.state.Message = ""
	state := u.state
	u.mu.Unlock()
	_ = update.WriteUpdaterState(u.cfg.DataDir, state)
	return state
}

func (u *updater) apply(version string) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	u.mu.Lock()
	current := u.state.CurrentTag
	u.mu.Unlock()

	exists, err := update.ReleaseExists(ctx, u.cfg.Repo, u.cfg.GitHubToken, version)
	if err != nil {
		u.finish("failed", fmt.Sprintf("release check failed: %v", err), current)
		return
	}
	if !exists {
		u.finish("failed", "release not found: "+version, current)
		return
	}
	backup, err := u.backup(ctx)
	if err != nil {
		u.finish("failed", "database backup failed: "+err.Error(), current)
		return
	}
	if err := rewriteEnvValue(u.cfg.EnvFile, "TACK_IMAGE_TAG", version); err != nil {
		u.finish("failed", "env update failed: "+err.Error(), current)
		return
	}
	if err := u.run(ctx, "pull", u.cfg.Service); err != nil {
		_ = rewriteEnvValue(u.cfg.EnvFile, "TACK_IMAGE_TAG", current)
		u.finish("failed", "image pull failed: "+err.Error(), current)
		return
	}
	if err := u.run(ctx, "up", "-d", "--wait", "--wait-timeout", "90", u.cfg.Service); err != nil {
		_ = u.run(ctx, "stop", u.cfg.Service)
		restoreErr := restoreBackup(backup, u.cfg.DataDir)
		envErr := rewriteEnvValue(u.cfg.EnvFile, "TACK_IMAGE_TAG", current)
		rollbackErr := u.run(ctx, "up", "-d", "--wait", "--wait-timeout", "90", u.cfg.Service)
		message := "health check failed: " + err.Error()
		if restoreErr != nil {
			message += "; backup restore failed: " + restoreErr.Error()
		}
		if envErr != nil {
			message += "; env restore failed: " + envErr.Error()
		}
		if rollbackErr != nil {
			message += "; rollback failed: " + rollbackErr.Error()
		}
		u.finish("rolled_back", message, current)
		return
	}
	u.mu.Lock()
	u.state.CurrentTag = version
	u.state.PreviousTag = current
	u.state.BackupPath = backup
	u.mu.Unlock()
	u.finish("success", "updated to "+version, version)
}

func (u *updater) rollback() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	u.mu.Lock()
	previous := u.state.PreviousTag
	backup := u.state.BackupPath
	current := u.state.CurrentTag
	u.mu.Unlock()
	if previous == "" || backup == "" {
		u.finish("failed", "no previous release or backup", current)
		return
	}
	if err := u.run(ctx, "stop", u.cfg.Service); err != nil {
		u.finish("failed", "stop app failed: "+err.Error(), current)
		return
	}
	if err := restoreBackup(backup, u.cfg.DataDir); err != nil {
		u.finish("failed", "restore backup failed: "+err.Error(), current)
		return
	}
	if err := rewriteEnvValue(u.cfg.EnvFile, "TACK_IMAGE_TAG", previous); err != nil {
		u.finish("failed", "env update failed: "+err.Error(), current)
		return
	}
	if err := u.run(ctx, "up", "-d", "--wait", "--wait-timeout", "90", u.cfg.Service); err != nil {
		u.finish("failed", "rollback health check failed: "+err.Error(), current)
		return
	}
	u.mu.Lock()
	u.state.CurrentTag = previous
	u.state.PreviousTag = current
	u.mu.Unlock()
	u.finish("rolled_back", "rolled back to "+previous, previous)
}

func (u *updater) backup(ctx context.Context) (string, error) {
	output, err := u.composeOutput(ctx, "exec", "-T", u.cfg.Service, "/tack", "backup")
	if err != nil {
		return "", err
	}
	lines := strings.Split(strings.TrimSpace(output), "\n")
	path := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(path, "/data/") {
		return "", fmt.Errorf("unexpected backup path %q", path)
	}
	return path, nil
}

func (u *updater) run(ctx context.Context, args ...string) error {
	_, err := u.composeOutput(ctx, args...)
	return err
}

func (u *updater) composeOutput(ctx context.Context, args ...string) (string, error) {
	commandArgs := []string{
		"compose",
		"--env-file", u.cfg.EnvFile,
		"-f", u.cfg.ComposeFile,
	}
	commandArgs = append(commandArgs, args...)
	command := exec.CommandContext(ctx, "docker", commandArgs...)
	command.Env = append(os.Environ(), "COMPOSE_PROJECT_NAME="+u.cfg.ProjectName)
	output, err := command.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%s: %s", err, strings.TrimSpace(string(output)))
	}
	return string(output), nil
}

func (u *updater) finish(status, message, currentTag string) {
	u.mu.Lock()
	u.state.Status = status
	u.state.Message = message
	if currentTag != "" {
		u.state.CurrentTag = currentTag
	}
	state := u.state
	u.mu.Unlock()
	if err := update.WriteUpdaterState(u.cfg.DataDir, state); err != nil {
		u.log.Error("write updater state", "error", err)
	}
	u.log.Info("updater finished", "status", status, "message", message)
}

func restoreBackup(backup, dataDir string) error {
	source, err := os.ReadFile(backup)
	if err != nil {
		return err
	}
	target := filepath.Join(dataDir, "tack.db")
	mode := os.FileMode(0o600)
	uid, gid := -1, -1
	if info, statErr := os.Stat(target); statErr == nil {
		mode = info.Mode().Perm()
		if owner, ok := fileOwner(info); ok {
			uid, gid = owner.uid, owner.gid
		}
	} else if info, dirErr := os.Stat(dataDir); dirErr == nil {
		if owner, ok := fileOwner(info); ok {
			uid, gid = owner.uid, owner.gid
		}
	}
	if err := os.WriteFile(target, source, mode); err != nil {
		return err
	}
	if uid >= 0 {
		_ = os.Chown(target, uid, gid)
	}
	if err := os.Chmod(target, mode); err != nil {
		return err
	}
	_ = os.Remove(target + "-wal")
	_ = os.Remove(target + "-shm")
	return nil
}

var releaseRegexp = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func rewriteEnvValue(path, key, value string) error {
	body, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	lines := strings.Split(string(body), "\n")
	prefix := key + "="
	found := false
	for i, line := range lines {
		if strings.HasPrefix(line, prefix) {
			lines[i] = prefix + value
			found = true
		}
	}
	if !found {
		lines = append(lines, prefix+value)
	}
	mode := os.FileMode(0o600)
	uid, gid := -1, -1
	if info, statErr := os.Stat(path); statErr == nil {
		mode = info.Mode().Perm()
		if owner, ok := fileOwner(info); ok {
			uid, gid = owner.uid, owner.gid
		}
	}
	temp, err := os.CreateTemp(filepath.Dir(path), ".env-*.tmp")
	if err != nil {
		return err
	}
	tempName := temp.Name()
	defer func() { _ = os.Remove(tempName) }()
	if err := temp.Chmod(mode); err != nil {
		_ = temp.Close()
		return err
	}
	if _, err := temp.WriteString(strings.Join(lines, "\n")); err != nil {
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
	if uid >= 0 {
		_ = os.Chown(tempName, uid, gid)
	}
	return os.Rename(tempName, path)
}

type ownership struct {
	uid int
	gid int
}

func fileOwner(info os.FileInfo) (ownership, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return ownership{}, false
	}
	return ownership{uid: int(stat.Uid), gid: int(stat.Gid)}, true
}

func readEnvValue(path, key string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	prefix := key + "="
	for _, line := range strings.Split(string(body), "\n") {
		if strings.HasPrefix(line, prefix) {
			return strings.TrimSpace(strings.TrimPrefix(line, prefix))
		}
	}
	return ""
}

func envOr(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
