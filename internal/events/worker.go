package events

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"

	"github.com/today/tack/internal/store"
)

type Worker struct {
	store  *store.Store
	policy *URLPolicy
	client *http.Client
	now    func() time.Time
}

func NewWorker(st *store.Store, allowPrivate bool, now func() time.Time) *Worker {
	if now == nil {
		now = time.Now
	}
	policy := NewURLPolicy(allowPrivate)
	transport := &http.Transport{
		DialContext:            policy.dialContext,
		DisableKeepAlives:      true,
		TLSHandshakeTimeout:    5 * time.Second,
		ResponseHeaderTimeout:  5 * time.Second,
		MaxResponseHeaderBytes: 16 << 10,
	}
	return &Worker{
		store: st, policy: policy, now: now,
		client: &http.Client{
			Transport:     transport,
			Timeout:       5 * time.Second,
			CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		},
	}
}

func (worker *Worker) Run(ctx context.Context) {
	defer worker.client.CloseIdleConnections()
	for ctx.Err() == nil {
		worked, err := worker.DeliverNext(ctx)
		if err == nil && worked {
			continue
		}
		timer := time.NewTimer(time.Second)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (worker *Worker) DeliverNext(ctx context.Context) (bool, error) {
	claimed, err := worker.store.ClaimWebhookDelivery(ctx, worker.now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	success, message := worker.send(ctx, claimed)
	err = worker.store.FinishWebhookDelivery(ctx, claimed, success, message, worker.now().UTC())
	if errors.Is(err, store.ErrNotFound) {
		err = nil
	}
	return true, err
}

func (worker *Worker) send(ctx context.Context, claimed store.ClaimedWebhookDelivery) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if _, err := worker.policy.Validate(ctx, claimed.URL); err != nil {
		return false, "destination not allowed or unavailable"
	}
	active, err := worker.store.WebhookDeliveryActive(ctx, claimed)
	if err != nil || !active {
		return false, "delivery cancelled"
	}
	eventName := claimed.Event.Resource + "." + claimed.Event.Kind
	payload := struct {
		ID        int64     `json:"id"`
		Event     string    `json:"event"`
		CreatedAt time.Time `json:"created_at"`
		Data      struct {
			Resource   string `json:"resource"`
			ResourceID int64  `json:"resource_id"`
			Kind       string `json:"kind"`
		} `json:"data"`
	}{ID: claimed.EventID, Event: eventName, CreatedAt: claimed.Event.CreatedAt}
	payload.Data.Resource = claimed.Event.Resource
	payload.Data.ResourceID = claimed.Event.ResourceID
	payload.Data.Kind = claimed.Event.Kind
	body, err := json.Marshal(payload)
	if err != nil {
		return false, "payload encoding failed"
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, claimed.URL, bytes.NewReader(body))
	if err != nil {
		return false, "invalid destination"
	}
	timestamp := strconv.FormatInt(worker.now().UTC().Unix(), 10)
	signature := hmac.New(sha256.New, []byte(claimed.Secret))
	_, _ = signature.Write([]byte(timestamp + "."))
	_, _ = signature.Write(body)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-Tack-Event", eventName)
	request.Header.Set("X-Tack-Delivery", strconv.FormatInt(claimed.ID, 10))
	request.Header.Set("X-Tack-Timestamp", timestamp)
	request.Header.Set("X-Tack-Signature", "sha256="+hex.EncodeToString(signature.Sum(nil)))
	response, err := worker.client.Do(request)
	if err != nil {
		return false, "request failed or timed out"
	}
	defer func() { _ = response.Body.Close() }()
	if _, err := io.Copy(io.Discard, io.LimitReader(response.Body, 4096)); err != nil {
		return false, "response read failed or timed out"
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return false, fmt.Sprintf("HTTP %d", response.StatusCode)
	}
	return true, ""
}
