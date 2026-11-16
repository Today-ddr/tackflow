package update

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckerLatest(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/repos/today/tack/releases/latest" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		w.Header().Set("ETag", `"abc"`)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tag_name":"v0.2.0","body":"changes","html_url":"https://github.com/today/tack/releases/tag/v0.2.0","published_at":"2026-11-16T00:00:00Z"}`))
	}))
	defer server.Close()

	checker := &Checker{Repo: "today/tack", BaseURL: server.URL, Client: server.Client()}
	release, etag, notModified, err := checker.Latest(context.Background(), "")
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if notModified || release.TagName != "v0.2.0" || etag != `"abc"` {
		t.Fatalf("release = %+v, etag = %q, notModified = %v", release, etag, notModified)
	}
}

func TestCheckerNotModified(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer server.Close()
	checker := &Checker{Repo: "today/tack", BaseURL: server.URL, Client: server.Client()}
	_, _, notModified, err := checker.Latest(context.Background(), `"abc"`)
	if err != nil {
		t.Fatalf("Latest: %v", err)
	}
	if !notModified {
		t.Fatal("notModified = false")
	}
}
