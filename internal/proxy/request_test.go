package proxy

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestLogger(t *testing.T) (*slog.Logger, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logger, &buf
}

func newClientRequest(t *testing.T, headers map[string]string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(`{"model":"kimi-k2.7"}`))
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return req
}

// buildRequest builds an upstream request from a replayed body and returns
// the request and the dropped-header count. The body is wrapped in an
// io.MultiReader so http.NewRequest does not attach a GetBody, matching
// what rewrite.Plan.Reader returns.
func buildRequest(t *testing.T, clientReq *http.Request, body string, accountKey string, logger *slog.Logger) (*http.Request, int64) {
	t.Helper()
	var dropped int64
	req, err := BuildUpstreamRequest(clientReq, io.MultiReader(strings.NewReader(body)), int64(len(body)), accountKey, logger, &dropped)
	if err != nil {
		t.Fatalf("BuildUpstreamRequest: %v", err)
	}
	return req, dropped
}

func TestBuildUpstreamRequestAccountAuthorizationReplacesClientAuthorization(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"Authorization": "Bearer client-secret",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	if got := req.Header.Get("Authorization"); got != "Bearer account-key" {
		t.Errorf("Authorization = %q, want %q", got, "Bearer account-key")
	}
	if dropped != 1 {
		t.Errorf("dropped count = %d, want 1 (client Authorization)", dropped)
	}
}

func TestBuildUpstreamRequestDropsSessionHeader(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"X-Session-ID": "session-123",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	if got := req.Header.Get("X-Session-Id"); got != "" {
		t.Errorf("X-Session-Id = %q, want empty (dropped)", got)
	}
	if dropped != 1 {
		t.Errorf("dropped count = %d, want 1", dropped)
	}
}

func TestBuildUpstreamRequestPreservesAllowlistedHeaders(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"Content-Type":    "application/json",
		"Accept":          "application/json",
		"Accept-Encoding": "gzip",
		"User-Agent":      "pi/1.0",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	for _, name := range []string{"Content-Type", "Accept", "Accept-Encoding", "User-Agent"} {
		if got, want := req.Header.Get(name), clientReq.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if dropped != 0 {
		t.Errorf("dropped count = %d, want 0", dropped)
	}
}

func TestBuildUpstreamRequestOmitsAllowlistedHeadersWhenAbsent(t *testing.T) {
	clientReq := newClientRequest(t, nil)
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	for _, name := range []string{"Content-Type", "Accept", "Accept-Encoding"} {
		if got := req.Header.Get(name); got != "" {
			t.Errorf("%s = %q, want empty (not sent)", name, got)
		}
	}
	if dropped != 0 {
		t.Errorf("dropped count = %d, want 0", dropped)
	}
}

func TestBuildUpstreamRequestStripsNonAllowlistedHeaders(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"Cookie":          "session=abc",
		"X-Forwarded-For": "1.2.3.4",
		"X-Trace-Id":      "trace-123",
		"X-Custom":        "custom-value",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	for _, name := range []string{"Cookie", "X-Forwarded-For", "X-Trace-Id", "X-Custom"} {
		if got := req.Header.Get(name); got != "" {
			t.Errorf("%s = %q, want empty (stripped)", name, got)
		}
	}
	if dropped != 4 {
		t.Errorf("dropped count = %d, want 4", dropped)
	}
}

func TestBuildUpstreamRequestCountsAndNamesDroppedHeaders(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"X-Secret": "super-secret-value",
	})
	logger, buf := newTestLogger(t)

	_, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	if dropped != 1 {
		t.Errorf("dropped count = %d, want 1", dropped)
	}
	logOutput := buf.String()
	if !strings.Contains(logOutput, "X-Secret") {
		t.Errorf("debug event does not name the dropped header: %q", logOutput)
	}
	if strings.Contains(logOutput, "super-secret-value") {
		t.Errorf("debug event contains the dropped header value: %q", logOutput)
	}
}

func TestBuildUpstreamRequestEmptyUserAgentWhenClientSendsNone(t *testing.T) {
	clientReq := newClientRequest(t, nil)
	logger, _ := newTestLogger(t)

	req, _ := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	if got := req.Header.Get("User-Agent"); got != "" {
		t.Errorf("User-Agent = %q, want empty (no synthesized default)", got)
	}
}

func TestBuildUpstreamRequestHasNoGetBodyOrIdempotencyHeader(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"Idempotency-Key": "idem-123",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	if req.GetBody != nil {
		t.Error("GetBody must be nil so the transport cannot replay the POST")
	}
	for name := range req.Header {
		if strings.Contains(strings.ToLower(name), "idempotency") {
			t.Errorf("outbound request carries idempotency header %q", name)
		}
	}
	if dropped != 1 {
		t.Errorf("dropped count = %d, want 1 (client idempotency header)", dropped)
	}
}

func TestBuildUpstreamRequestSetsContentLengthURLAndBody(t *testing.T) {
	body := `{"model":"kimi-k2.7","messages":[]}`
	clientReq := newClientRequest(t, nil)
	logger, _ := newTestLogger(t)

	req, _ := buildRequest(t, clientReq, body, "account-key", logger)

	if req.Method != http.MethodPost {
		t.Errorf("Method = %q, want %q", req.Method, http.MethodPost)
	}
	if got := req.URL.String(); got != upstreamChatCompletionsURL {
		t.Errorf("URL = %q, want %q", got, upstreamChatCompletionsURL)
	}
	if req.ContentLength != int64(len(body)) {
		t.Errorf("ContentLength = %d, want %d", req.ContentLength, len(body))
	}
	bodyBytes, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("reading body: %v", err)
	}
	if string(bodyBytes) != body {
		t.Errorf("body = %q, want %q", bodyBytes, body)
	}
}

func TestBuildUpstreamRequestHeadersAreFullyAccounted(t *testing.T) {
	clientReq := newClientRequest(t, map[string]string{
		"Content-Type": "application/json",
		"X-Custom":     "custom-value",
	})
	logger, _ := newTestLogger(t)

	req, dropped := buildRequest(t, clientReq, `{"model":"kimi-k2.7"}`, "account-key", logger)

	want := map[string]bool{
		"Content-Type":  true,
		"Authorization": true,
		"User-Agent":    true,
	}
	for name := range req.Header {
		if !want[name] {
			t.Errorf("outbound request carries unaccounted header %q", name)
		}
	}
	for name := range want {
		if _, ok := req.Header[name]; !ok {
			t.Errorf("outbound request missing expected header %q", name)
		}
	}
	if dropped != 1 {
		t.Errorf("dropped count = %d, want 1 (X-Custom)", dropped)
	}
}
