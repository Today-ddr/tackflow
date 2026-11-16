package api

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type pausedRequestBody struct {
	entered chan struct{}
	release chan struct{}
	once    sync.Once
	reader  io.Reader
}

func (body *pausedRequestBody) Read(buffer []byte) (int, error) {
	body.once.Do(func() { close(body.entered) })
	<-body.release
	return body.reader.Read(buffer)
}

func (body *pausedRequestBody) Close() error { return nil }

func TestSlowCaptureBodyDoesNotBlockSessionWrites(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()
	client := newClient(t)
	closeBody(t, login(t, client, ts.URL, testPassword, ""))
	var created struct {
		Token    string `json:"token"`
		APIToken struct {
			ID int64 `json:"id"`
		} `json:"api_token"`
	}
	decodeResponse(t, webJSON(t, client, http.MethodPost, ts.URL+"/api/v1/tokens", map[string]any{
		"name": "slow", "scopes": []string{"capture"}, "password": testPassword,
	}), &created)
	body := &pausedRequestBody{entered: make(chan struct{}), release: make(chan struct{}), reader: strings.NewReader(`{"text":"slow"}`)}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/capture", body).WithContext(ctx)
	request.Header.Set("Authorization", "Bearer "+created.Token)
	done := make(chan struct{})
	recorder := httptest.NewRecorder()
	go func() { ts.Config.Handler.ServeHTTP(recorder, request); close(done) }()
	<-body.entered
	revoked := make(chan int, 1)
	go func() {
		response := webJSON(t, client, http.MethodDelete, ts.URL+"/api/v1/tokens/"+itoa(created.APIToken.ID), nil)
		revoked <- response.StatusCode
		closeBody(t, response)
	}()
	select {
	case status := <-revoked:
		if status != http.StatusNoContent {
			t.Errorf("revoke while body stalls = %d", status)
		}
	case <-time.After(time.Second):
		t.Error("slow capture blocked token revocation")
	}
	cancel()
	close(body.release)
	<-done
	var listed struct {
		Tasks []taskDTO `json:"tasks"`
	}
	decodeResponse(t, webJSON(t, client, http.MethodGet, ts.URL+"/api/v1/tasks", nil), &listed)
	if len(listed.Tasks) != 0 {
		t.Fatalf("cancelled request wrote task: %+v", listed.Tasks)
	}
}
