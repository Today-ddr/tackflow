package bridge

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"syscall"
	"time"
)

const maxStateBytes = 16 << 20

type tackTask struct {
	ID         int64  `json:"id"`
	Title      string `json:"title"`
	Notes      string `json:"notes"`
	Status     string `json:"status"`
	AgentReady bool   `json:"agent_ready"`
}

type claim struct {
	ID        string    `json:"id"`
	ExpiresAt time.Time `json:"expires_at"`
}

type resultLink struct {
	Kind       string `json:"kind"`
	ExternalID string `json:"external_id"`
	URL        string `json:"url"`
}

type taskResult struct {
	ClaimID string       `json:"claim_id"`
	Status  string       `json:"status"`
	Comment string       `json:"comment"`
	Links   []resultLink `json:"links"`
}

type mapping struct {
	Task      tackTask    `json:"task"`
	Claim     claim       `json:"claim"`
	Marker    string      `json:"marker"`
	Phase     string      `json:"phase"`
	CodegID   int64       `json:"codeg_id,omitempty"`
	Result    *taskResult `json:"result,omitempty"`
	ResultKey string      `json:"result_key,omitempty"`
}

type diskState struct {
	Version        int              `json:"version"`
	TackURL        string           `json:"tack_url"`
	CodegURL       string           `json:"codeg_url"`
	FolderID       int64            `json:"folder_id"`
	Worker         string           `json:"worker"`
	ClaimKey       string           `json:"claim_key,omitempty"`
	ClaimStartedAt time.Time        `json:"claim_started_at,omitempty"`
	Active         *mapping         `json:"active,omitempty"`
	Completed      map[string]int64 `json:"completed"`
}

type stateFile struct {
	filename string
	lock     *os.File
}

func markerFor(base string, taskID int64) string {
	digest := sha256.Sum256([]byte(base + "\n" + strconv.FormatInt(taskID, 10)))
	return "tack-bridge-v1-" + hex.EncodeToString(digest[:])
}

func randomKey() (string, error) {
	var buffer [24]byte
	if _, err := rand.Read(buffer[:]); err != nil {
		return "", errors.New("cannot generate request identity")
	}
	return "tack-bridge-" + hex.EncodeToString(buffer[:]), nil
}

func openState(config Config) (*stateFile, diskState, error) {
	state := diskState{Version: 1, TackURL: config.TackURL, CodegURL: config.CodegURL, FolderID: config.FolderID, Worker: config.Worker, Completed: map[string]int64{}}
	if err := os.MkdirAll(filepath.Dir(config.StatePath), 0700); err != nil {
		return nil, state, errors.New("cannot create bridge state directory")
	}
	if err := regularOrMissing(config.StatePath); err != nil {
		return nil, state, err
	}
	if err := regularOrMissing(config.StatePath + ".lock"); err != nil {
		return nil, state, err
	}
	lock, err := os.OpenFile(config.StatePath+".lock", os.O_RDWR|os.O_CREATE, 0600)
	if err != nil {
		return nil, state, errors.New("cannot open bridge state lock")
	}
	file := &stateFile{filename: config.StatePath, lock: lock}
	if err := lock.Chmod(0600); err != nil {
		_ = file.close()
		return nil, state, errors.New("cannot secure bridge state lock")
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.close()
		return nil, state, errors.New("bridge state is locked by another worker")
	}
	err = file.load(&state)
	if err == nil && (state.Version != 1 || state.TackURL != config.TackURL || state.CodegURL != config.CodegURL || state.FolderID != config.FolderID || state.Worker != config.Worker) {
		err = errors.New("bridge state version or destination/worker binding differs; preserve the state and reconcile before changing configuration")
	}
	if err == nil {
		err = state.validate()
	}
	if err != nil {
		_ = file.close()
		return nil, state, err
	}
	return file, state, nil
}

func regularOrMissing(filename string) error {
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil || !info.Mode().IsRegular() {
		return errors.New("bridge state and lock must be regular files, not symlinks")
	}
	return nil
}

func (file *stateFile) load(state *diskState) error {
	handle, err := os.Open(file.filename)
	if os.IsNotExist(err) {
		return file.save(*state)
	}
	if err != nil {
		return errors.New("cannot open bridge state")
	}
	defer func() { _ = handle.Close() }()
	if err := handle.Chmod(0600); err != nil {
		return errors.New("cannot secure bridge state")
	}
	data, err := io.ReadAll(io.LimitReader(handle, maxStateBytes+1))
	if err != nil || len(data) > maxStateBytes {
		return errors.New("cannot read bridge state or state exceeds limit")
	}
	var loaded diskState
	if err := json.Unmarshal(data, &loaded); err != nil {
		return errors.New("bridge state is corrupt; refusing to discard mappings")
	}
	*state = loaded
	return nil
}

func (state diskState) validate() error {
	if state.Completed == nil {
		return errors.New("bridge state is missing completed mappings")
	}
	if state.ClaimKey != "" && (state.ClaimStartedAt.IsZero() || state.Active != nil) {
		return errors.New("bridge state has inconsistent claim intent")
	}
	active := state.Active
	if active == nil {
		return nil
	}
	if active.Task.ID <= 0 || !active.Task.AgentReady || active.Claim.ID == "" || active.Claim.ExpiresAt.IsZero() || active.Marker != markerFor(state.TackURL, active.Task.ID) {
		return errors.New("bridge state has invalid active mapping")
	}
	if state.Completed[strconv.FormatInt(active.Task.ID, 10)] > 0 {
		return errors.New("active task also has a completed mapping; preserve state and reconcile requeue or rollback manually")
	}
	switch active.Phase {
	case "claimed", "creating":
		if active.CodegID != 0 {
			return errors.New("bridge create intent has inconsistent Codeg ID")
		}
	case "created", "starting", "monitoring", "result_pending":
		if active.CodegID <= 0 {
			return errors.New("bridge mapping is missing Codeg ID")
		}
	default:
		return errors.New("bridge state has unknown phase")
	}
	if active.Phase == "result_pending" && (active.Result == nil || active.ResultKey == "" || active.Result.ClaimID != active.Claim.ID) {
		return errors.New("bridge result intent is incomplete")
	}
	return nil
}

func (file *stateFile) save(state diskState) error {
	data, err := json.MarshalIndent(state, "", "  ")
	if err != nil || len(data) > maxStateBytes {
		return errors.New("bridge state cannot be encoded or exceeds limit")
	}
	temporary, err := os.CreateTemp(filepath.Dir(file.filename), ".tack-bridge-*")
	if err != nil {
		return errors.New("cannot create bridge state snapshot")
	}
	defer func() { _ = temporary.Close(); _ = os.Remove(temporary.Name()) }()
	if err := temporary.Chmod(0600); err != nil {
		return errors.New("cannot secure bridge state snapshot")
	}
	if _, err := temporary.Write(data); err != nil {
		return errors.New("cannot write bridge state snapshot")
	}
	if err := temporary.Sync(); err != nil {
		return errors.New("cannot sync bridge state snapshot")
	}
	if err := temporary.Close(); err != nil {
		return errors.New("cannot close bridge state snapshot")
	}
	if err := os.Rename(temporary.Name(), file.filename); err != nil {
		return errors.New("cannot replace bridge state snapshot")
	}
	directory, err := os.Open(filepath.Dir(file.filename))
	if err != nil {
		return errors.New("cannot open bridge state directory for sync")
	}
	defer func() { _ = directory.Close() }()
	if err := directory.Sync(); err != nil {
		return errors.New("cannot sync bridge state directory")
	}
	return nil
}

func (file *stateFile) close() error {
	if file.lock == nil {
		return nil
	}
	err := file.lock.Close()
	file.lock = nil
	if err != nil {
		return fmt.Errorf("cannot close bridge state lock")
	}
	return nil
}
