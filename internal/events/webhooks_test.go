package events

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/today/tack/internal/store"
)

func deliveryTestStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Migrate(t.Context()); err != nil {
		t.Fatal(err)
	}
	return st
}

func TestWebhookSignatureRetryAndPersistentSuccess(t *testing.T) {
	st := deliveryTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	secret := "known-signing-key"
	type receivedRequest struct {
		body   []byte
		header http.Header
	}
	received := make(chan receivedRequest, 3)
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		body, _ := io.ReadAll(request.Body)
		received <- receivedRequest{body, request.Header.Clone()}
		if calls.Add(1) == 1 {
			writer.WriteHeader(http.StatusInternalServerError)
			_, _ = io.WriteString(writer, secret)
			return
		}
		writer.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	hook, err := st.CreateWebhook(t.Context(), "hook", receiver.URL, []string{"task.*"}, secret, now)
	if err != nil {
		t.Fatal(err)
	}
	task, err := st.CreateTask(t.Context(), store.CreateTaskParams{Title: "private title", Notes: "secret notes", Now: now})
	if err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(st, true, func() time.Time { return now })
	if worked, err := worker.DeliverNext(t.Context()); err != nil || !worked {
		t.Fatalf("delivery worked=%v err=%v", worked, err)
	}
	first := <-received
	stamp := first.header.Get("X-Tack-Timestamp")
	if stamp != "1794830400" {
		t.Fatalf("timestamp = %s", stamp)
	}
	signature := hmac.New(sha256.New, []byte(secret))
	_, _ = signature.Write(append([]byte(stamp+"."), first.body...))
	if got := first.header.Get("X-Tack-Signature"); got != "sha256="+hex.EncodeToString(signature.Sum(nil)) {
		t.Fatalf("invalid signature: %s", got)
	}
	if first.header.Get("X-Tack-Event") != "task.created" || first.header.Get("X-Tack-Delivery") == "" {
		t.Fatalf("headers = %v", first.header)
	}
	var payload struct {
		ID        int64     `json:"id"`
		Event     string    `json:"event"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			Resource   string `json:"resource"`
			ResourceID int64  `json:"resource_id"`
			Kind       string `json:"kind"`
		} `json:"data"`
	}
	if err := json.Unmarshal(first.body, &payload); err != nil || payload.Event != "task.created" || payload.Data.ResourceID != task.ID || payload.Data.Resource != "task" || payload.Data.Kind != "created" || !payload.CreatedAt.Equal(now) {
		t.Fatalf("payload = %s, err=%v", first.body, err)
	}
	if strings.Contains(string(first.body), "private title") || strings.Contains(string(first.body), "secret") {
		t.Fatal("payload exposes task content or credentials")
	}
	deliveries, err := st.WebhookDeliveries(t.Context(), hook.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != "pending" || deliveries[0].Attempts != 1 || deliveries[0].LastError != "HTTP 500" {
		t.Fatalf("failed delivery = %+v, err=%v", deliveries, err)
	}
	if deliveries[0].NextAttemptAt.Sub(now) != 30*time.Second {
		t.Fatal("first retry must wait 30 seconds")
	}
	if worked, err := worker.DeliverNext(t.Context()); err != nil || worked {
		t.Fatalf("early retry worked=%v err=%v", worked, err)
	}
	now = deliveries[0].NextAttemptAt
	restarted := NewWorker(st, true, func() time.Time { return now })
	if worked, err := restarted.DeliverNext(t.Context()); err != nil || !worked {
		t.Fatalf("retry worked=%v err=%v", worked, err)
	}
	second := <-received
	if first.header.Get("X-Tack-Delivery") != second.header.Get("X-Tack-Delivery") || string(first.body) != string(second.body) {
		t.Fatal("retry changed stable delivery id or payload")
	}
	deliveries, err = st.WebhookDeliveries(t.Context(), hook.ID)
	if err != nil || deliveries[0].Status != "delivered" || deliveries[0].Attempts != 2 || deliveries[0].DeliveredAt == nil || deliveries[0].LastError != "" {
		t.Fatalf("completed delivery = %+v, err=%v", deliveries, err)
	}
}

func TestWebhookAttemptsBoundedAndRedirectDenied(t *testing.T) {
	st := deliveryTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	var redirected atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		redirected.Add(1)
		writer.WriteHeader(http.StatusOK)
	}))
	defer target.Close()
	var attempts atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		attempts.Add(1)
		http.Redirect(writer, request, target.URL, http.StatusTemporaryRedirect)
	}))
	defer receiver.Close()
	hook, err := st.CreateWebhook(t.Context(), "hook", receiver.URL, []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTag(t.Context(), "tag", "sage", now); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(st, true, func() time.Time { return now })
	for attempt := 1; attempt <= 5; attempt++ {
		if worked, err := worker.DeliverNext(t.Context()); err != nil || !worked {
			t.Fatalf("attempt %d worked=%v err=%v", attempt, worked, err)
		}
		deliveries, err := st.WebhookDeliveries(t.Context(), hook.ID)
		if err != nil || deliveries[0].Attempts != attempt {
			t.Fatalf("attempts=%+v err=%v", deliveries, err)
		}
		if attempt < 5 && deliveries[0].NextAttemptAt.Sub(now) != time.Duration(30*(1<<(attempt-1)))*time.Second {
			t.Fatal("retry is not exponential")
		}
		now = deliveries[0].NextAttemptAt
	}
	now = now.Add(24 * time.Hour)
	if worked, err := worker.DeliverNext(t.Context()); err != nil || worked {
		t.Fatalf("sixth attempt worked=%v err=%v", worked, err)
	}
	deliveries, err := st.WebhookDeliveries(t.Context(), hook.ID)
	if err != nil || deliveries[0].Status != "failed" || attempts.Load() != 5 || redirected.Load() != 0 {
		t.Fatalf("state=%+v attempts=%d redirected=%d err=%v", deliveries, attempts.Load(), redirected.Load(), err)
	}
}

func TestWebhookURLPolicy(t *testing.T) {
	policy := NewURLPolicy(false)
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}, nil
	}
	for _, raw := range []string{
		"http://example.com", "https://user:pass@example.com", "https://example.com/#fragment", "https://example.com/#",
		"https://127.0.0.1", "https://10.0.0.1", "https://172.16.0.1", "https://192.168.0.1", "https://169.254.169.254",
		"https://[::1]", "https://[::ffff:127.0.0.1]", "https://[fc00::1]", "https://[fe80::1]", "https://0.0.0.0",
		"https://224.0.0.1", "https://100.64.0.1", "https://[64:ff9b::7f00:1]", "https://example.com:0", "https://example.com:65536",
	} {
		t.Run(raw, func(t *testing.T) {
			if _, err := policy.Validate(t.Context(), raw); err == nil {
				t.Fatalf("accepted unsafe URL %s", raw)
			}
		})
	}
	if _, err := policy.Validate(t.Context(), "https://example.com/events?source=tack"); err != nil {
		t.Fatal(err)
	}
	if _, err := NewURLPolicy(true).Validate(t.Context(), "http://127.0.0.1:8080"); err != nil {
		t.Fatalf("explicit development opt-in: %v", err)
	}
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) {
		return []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}, {IP: net.ParseIP("10.0.0.1")}}, nil
	}
	if _, err := policy.Validate(t.Context(), "https://mixed.example"); err == nil {
		t.Fatal("mixed public/private DNS accepted")
	}
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) { return nil, nil }
	if _, err := policy.Validate(t.Context(), "https://empty.example"); err == nil {
		t.Fatal("empty DNS answer accepted")
	}
}

func TestWebhookDNSRevalidationPinsDial(t *testing.T) {
	policy := NewURLPolicy(false)
	answers := []net.IPAddr{{IP: net.ParseIP("93.184.216.34")}}
	policy.lookup = func(context.Context, string) ([]net.IPAddr, error) { return answers, nil }
	if _, err := policy.Validate(t.Context(), "https://receiver.example"); err != nil {
		t.Fatal(err)
	}
	var address string
	policy.dial = func(_ context.Context, _, target string) (net.Conn, error) {
		address = target
		return nil, fmt.Errorf("test dial stopped")
	}
	_, _ = policy.dialContext(t.Context(), "tcp", "receiver.example:443")
	if address != "93.184.216.34:443" {
		t.Fatalf("dial was not pinned: %s", address)
	}
	address = ""
	answers = []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}
	if _, err := policy.dialContext(t.Context(), "tcp", "receiver.example:443"); err == nil || address != "" {
		t.Fatal("DNS rebind reached dial")
	}
}

func TestWebhookDisabledAndPrivateDefaultNeverSends(t *testing.T) {
	st := deliveryTestStore(t)
	now := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		calls.Add(1)
	}))
	defer receiver.Close()
	hook, err := st.CreateWebhook(t.Context(), "hook", receiver.URL, []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTag(t.Context(), "tag", "sage", now); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(st, false, func() time.Time { return now })
	if worked, err := worker.DeliverNext(t.Context()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if calls.Load() != 0 {
		t.Fatal("private default reached receiver")
	}
	if err := st.UpdateWebhook(t.Context(), hook.ID, hook.Name, hook.URL, hook.Events, false); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Hour)
	worker = NewWorker(st, true, func() time.Time { return now })
	if worked, err := worker.DeliverNext(t.Context()); err != nil || worked || calls.Load() != 0 {
		t.Fatalf("disabled hook worked=%v calls=%s err=%v", worked, strconv.Itoa(int(calls.Load())), err)
	}
}

func TestWebhookBoundsResponseBodyAndCancelsRead(t *testing.T) {
	st := deliveryTestStore(t)
	now := time.Now().UTC()
	release := make(chan struct{})
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("x", 8192))
		writer.(http.Flusher).Flush()
		select {
		case <-release:
		case <-request.Context().Done():
		}
	}))
	defer receiver.Close()
	defer close(release)
	hook, err := st.CreateWebhook(t.Context(), "hook", receiver.URL, []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTag(t.Context(), "tag", "sage", now); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if worked, err := NewWorker(st, true, func() time.Time { return now }).DeliverNext(ctx); err != nil || !worked {
		t.Fatalf("bounded read worked=%v err=%v", worked, err)
	}
	deliveries, err := st.WebhookDeliveries(t.Context(), hook.ID)
	if err != nil || deliveries[0].Status != "delivered" {
		t.Fatalf("body read exceeded bound: %+v err=%v", deliveries, err)
	}
}

func TestWebhookDisabledDuringDNSResolutionSkipsSend(t *testing.T) {
	st := deliveryTestStore(t)
	now := time.Now().UTC()
	var calls atomic.Int32
	receiver := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) { calls.Add(1) }))
	defer receiver.Close()
	target := strings.Replace(receiver.URL, "127.0.0.1", "receiver.example", 1)
	hook, err := st.CreateWebhook(t.Context(), "hook", target, []string{"*"}, "secret", now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.CreateTag(t.Context(), "tag", "sage", now); err != nil {
		t.Fatal(err)
	}
	worker := NewWorker(st, true, func() time.Time { return now })
	worker.policy.lookup = func(ctx context.Context, host string) ([]net.IPAddr, error) {
		if err := st.UpdateWebhook(ctx, hook.ID, hook.Name, hook.URL, hook.Events, false); err != nil {
			return nil, err
		}
		return []net.IPAddr{{IP: net.ParseIP("127.0.0.1")}}, nil
	}
	if worked, err := worker.DeliverNext(t.Context()); err != nil || !worked {
		t.Fatalf("worked=%v err=%v", worked, err)
	}
	if calls.Load() != 0 {
		t.Fatal("disabled webhook sent after DNS completed")
	}
}
