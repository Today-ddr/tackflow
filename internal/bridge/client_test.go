package bridge

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestHTTPRejectsRedirectsOversizedAndMalformedResponses(t *testing.T) {
	for _, scenario := range []string{"redirect", "oversized", "malformed", "null", "error", "timeout"} {
		t.Run(scenario, func(t *testing.T) {
			var redirected atomic.Int32
			destination := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				redirected.Add(1)
				_, _ = io.WriteString(writer, `{}`)
			}))
			defer destination.Close()
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				_, _ = io.Copy(io.Discard, request.Body)
				switch scenario {
				case "redirect":
					http.Redirect(writer, request, destination.URL, http.StatusTemporaryRedirect)
				case "oversized":
					_, _ = io.WriteString(writer, strings.Repeat(" ", maxResponseBytes+1))
				case "malformed":
					_, _ = io.WriteString(writer, `{"tk_test_secret":`)
				case "null":
					_, _ = io.WriteString(writer, `null`)
				case "error":
					http.Error(writer, "tk_test_secret codeg_test_secret", 500)
				case "timeout":
					<-request.Context().Done()
				}
			}))
			defer server.Close()
			fixture := newFixture(t)
			fixture.config.CodegURL = server.URL
			_, client := newClients(fixture.config)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			var output map[string]any
			err := client.call(ctx, http.MethodPost, "/api/health", "", struct{}{}, &output)
			if err == nil || strings.Contains(err.Error(), "test_secret") || redirected.Load() != 0 {
				t.Fatalf("unsafe HTTP response handling: %v", err)
			}
			if scenario == "timeout" && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("lost cancellation: %v", err)
			}
		})
	}
}

func TestMalformedClaimCannotClearPendingIdentity(t *testing.T) {
	fixture := newFixture(t)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(writer, `{}`) }))
	defer server.Close()
	fixture.config.TackURL = server.URL
	worker := openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil {
		t.Fatal("missing claim/task keys treated as empty queue")
	}
	if worker.state.ClaimKey == "" || fixture.creates != 0 {
		t.Fatal("unknown claim identity discarded")
	}
}

func TestLostClaimResponseReplaysSameIdentityAfterRestart(t *testing.T) {
	fixture := newFixture(t)
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		keys = append(keys, request.Header.Get("Idempotency-Key"))
		if len(keys) == 1 {
			dropConnection(writer)
			return
		}
		_, _ = io.WriteString(writer, `{"task":null,"claim":null}`)
	}))
	defer server.Close()
	fixture.config.TackURL = server.URL
	worker := openFixture(t, fixture)
	if err := worker.Step(context.Background()); err == nil {
		t.Fatal("lost claim response was hidden")
	}
	_ = worker.Close()
	step(t, openFixture(t, fixture))
	if len(keys) != 2 || keys[0] == "" || keys[0] != keys[1] {
		t.Fatalf("claim replay keys: %v", keys)
	}
}
