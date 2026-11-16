package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

var ErrNotConfigured = errors.New("github repository is not configured")

type Release struct {
	TagName     string `json:"tag_name"`
	Name        string `json:"name"`
	Body        string `json:"body"`
	HTMLURL     string `json:"html_url"`
	PublishedAt string `json:"published_at"`
}

type Checker struct {
	Repo    string
	Token   string
	BaseURL string
	Client  *http.Client
}

func NewChecker(repo, token string) *Checker {
	return &Checker{
		Repo:    strings.TrimSpace(repo),
		Token:   strings.TrimSpace(token),
		BaseURL: "https://api.github.com",
		Client:  &http.Client{Timeout: 15 * time.Second},
	}
}

func (c *Checker) Latest(ctx context.Context, etag string) (Release, string, bool, error) {
	if c.Repo == "" {
		return Release{}, "", false, ErrNotConfigured
	}
	baseURL := c.BaseURL
	if baseURL == "" {
		baseURL = "https://api.github.com"
	}
	client := c.Client
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodGet,
		strings.TrimRight(baseURL, "/")+"/repos/"+c.Repo+"/releases/latest",
		nil,
	)
	if err != nil {
		return Release{}, "", false, err
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("User-Agent", "tack-update-checker")
	if c.Token != "" {
		request.Header.Set("Authorization", "Bearer "+c.Token)
	}
	if etag != "" {
		request.Header.Set("If-None-Match", etag)
	}
	response, err := client.Do(request)
	if err != nil {
		return Release{}, "", false, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode == http.StatusNotModified {
		return Release{}, etag, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return Release{}, "", false, fmt.Errorf("github releases status %d", response.StatusCode)
	}
	var release Release
	if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
		return Release{}, "", false, fmt.Errorf("decode github release: %w", err)
	}
	if !IsReleaseTag(release.TagName) {
		return Release{}, "", false, fmt.Errorf("github release tag is not valid")
	}
	return release, response.Header.Get("ETag"), false, nil
}
