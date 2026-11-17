package update

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/today-ddr/tackflow/internal/store"
)

const (
	IntervalKey      = "update.interval_hours"
	LastCheckedKey   = "update.last_checked_at"
	LastErrorKey     = "update.last_error"
	LatestVersionKey = "update.latest_version"
	LatestBodyKey    = "update.latest_body"
	LatestURLKey     = "update.latest_url"
	PublishedAtKey   = "update.latest_published_at"
	ETagKey          = "update.etag"
	defaultInterval  = 24
)

var ErrRateLimited = errors.New("update check rate limited")

type Status struct {
	CurrentVersion  string  `json:"current_version"`
	LatestVersion   *string `json:"latest_version"`
	UpdateAvailable bool    `json:"update_available"`
	IntervalHours   int     `json:"interval_hours"`
	LastCheckedAt   *string `json:"last_checked_at"`
	LastError       string  `json:"last_error"`
	ReleaseBody     string  `json:"release_body"`
	ReleaseURL      string  `json:"release_url"`
	PublishedAt     *string `json:"published_at"`
	Configured      bool    `json:"configured"`
}

type Service struct {
	store   *store.Store
	checker *Checker
	current string
	now     func() time.Time
	logger  *slog.Logger

	mu         sync.Mutex
	lastManual time.Time
}

func NewService(
	st *store.Store,
	checker *Checker,
	current string,
	now func() time.Time,
	logger *slog.Logger,
) *Service {
	if now == nil {
		now = time.Now
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		store:   st,
		checker: checker,
		current: current,
		now:     now,
		logger:  logger,
	}
}

func (s *Service) Start(ctx context.Context) {
	if s == nil || s.checker == nil || s.checker.Repo == "" {
		return
	}
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		s.maybeCheck(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.maybeCheck(ctx)
			}
		}
	}()
}

func (s *Service) maybeCheck(ctx context.Context) {
	interval, err := s.interval(ctx)
	if err != nil || interval == 0 {
		return
	}
	last, err := s.store.SettingTime(ctx, LastCheckedKey)
	if err != nil {
		return
	}
	if last != nil && s.now().Sub(*last) < time.Duration(interval)*time.Hour {
		return
	}
	if _, err := s.check(ctx, false); err != nil {
		s.logger.Warn("background update check failed", "error", err)
	}
}

func (s *Service) CheckNow(ctx context.Context) (Status, error) {
	if s == nil || s.checker == nil || s.checker.Repo == "" {
		return Status{}, ErrNotConfigured
	}
	return s.check(ctx, true)
}

func (s *Service) Status(ctx context.Context) (Status, error) {
	current := ""
	configured := false
	if s != nil {
		current = s.current
		configured = s.checker != nil && s.checker.Repo != ""
	}
	status := Status{
		CurrentVersion: current,
		Configured:     configured,
		IntervalHours:  defaultInterval,
	}
	if s == nil {
		return status, nil
	}
	interval, err := s.interval(ctx)
	if err != nil {
		return Status{}, err
	}
	status.IntervalHours = interval

	latest, ok, err := s.store.GetSetting(ctx, LatestVersionKey)
	if err != nil {
		return Status{}, err
	}
	if ok && strings.TrimSpace(latest) != "" {
		latest = strings.TrimSpace(latest)
		status.LatestVersion = &latest
		newer, compareErr := IsNewer(s.current, latest)
		if compareErr != nil {
			return Status{}, compareErr
		}
		status.UpdateAvailable = newer
	}
	lastChecked, err := s.store.SettingTime(ctx, LastCheckedKey)
	if err != nil {
		return Status{}, err
	}
	if lastChecked != nil {
		value := lastChecked.Format(time.RFC3339)
		status.LastCheckedAt = &value
	}
	if value, ok, err := s.store.GetSetting(ctx, LastErrorKey); err != nil {
		return Status{}, err
	} else if ok {
		status.LastError = value
	}
	if value, ok, err := s.store.GetSetting(ctx, LatestBodyKey); err != nil {
		return Status{}, err
	} else if ok {
		status.ReleaseBody = value
	}
	if value, ok, err := s.store.GetSetting(ctx, LatestURLKey); err != nil {
		return Status{}, err
	} else if ok {
		status.ReleaseURL = value
	}
	publishedAt, err := s.store.SettingTime(ctx, PublishedAtKey)
	if err != nil {
		return Status{}, err
	}
	if publishedAt != nil {
		value := publishedAt.Format(time.RFC3339)
		status.PublishedAt = &value
	}
	return status, nil
}

func (s *Service) check(ctx context.Context, manual bool) (Status, error) {
	s.mu.Lock()
	if manual && !s.lastManual.IsZero() && s.now().Sub(s.lastManual) < time.Minute {
		s.mu.Unlock()
		return Status{}, ErrRateLimited
	}
	if manual {
		s.lastManual = s.now()
	}
	s.mu.Unlock()

	etag, _, _ := s.store.GetSetting(ctx, ETagKey)
	release, nextETag, notModified, err := s.checker.Latest(ctx, etag)
	now := s.now().UTC()
	if err != nil {
		_ = s.store.SetSetting(ctx, LastErrorKey, err.Error())
		_ = s.store.SetSetting(ctx, LastCheckedKey, now.Format(time.RFC3339))
		return Status{}, err
	}
	if notModified {
		_ = s.store.SetSetting(ctx, LastCheckedKey, now.Format(time.RFC3339))
		_ = s.store.SetSetting(ctx, LastErrorKey, "")
		return s.Status(ctx)
	}
	_ = s.store.SetSetting(ctx, LatestVersionKey, release.TagName)
	_ = s.store.SetSetting(ctx, LatestBodyKey, release.Body)
	_ = s.store.SetSetting(ctx, LatestURLKey, release.HTMLURL)
	if release.PublishedAt != "" {
		if parsed, parseErr := time.Parse(time.RFC3339, release.PublishedAt); parseErr == nil {
			_ = s.store.SetSetting(ctx, PublishedAtKey, parsed.UTC().Format(time.RFC3339))
		}
	}
	if nextETag != "" {
		_ = s.store.SetSetting(ctx, ETagKey, nextETag)
	}
	_ = s.store.SetSetting(ctx, LastCheckedKey, now.Format(time.RFC3339))
	_ = s.store.SetSetting(ctx, LastErrorKey, "")
	return s.Status(ctx)
}

func (s *Service) interval(ctx context.Context) (int, error) {
	value, ok, err := s.store.GetSetting(ctx, IntervalKey)
	if err != nil {
		return 0, err
	}
	if !ok {
		return defaultInterval, nil
	}
	parsed, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || (parsed != 0 && parsed != 6 && parsed != 12 && parsed != 24 && parsed != 48) {
		return defaultInterval, nil
	}
	return parsed, nil
}

func FormatStatusError(status Status) error {
	if status.LastError == "" {
		return nil
	}
	return fmt.Errorf("%s", status.LastError)
}
