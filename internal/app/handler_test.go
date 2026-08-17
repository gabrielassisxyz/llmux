package app

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
	"github.com/gabrielassisxyz/llmux/internal/rewrite"
	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

const testBearerKey = "12345678901234567890123456789012"

func testAuthDigest() [32]byte {
	return sha256.Sum256([]byte(testBearerKey))
}

type handlerFixture struct {
	t       *testing.T
	handler http.Handler
	store   *store.Store
	gate    *resource.Gate
	clk     *testsupport.FakeClock
}

func newHandlerFixture(t *testing.T, chat http.HandlerFunc) *handlerFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("Store.Close() error = %v", err)
		}
	})

	entropy := bytes.Repeat([]byte{0xcd}, 1024)
	gen := idgen.NewGenerator(bytes.NewReader(entropy))
	clk := testsupport.NewFakeClock(time.Date(2026, time.August, 14, 12, 0, 0, 0, time.UTC))
	gate := resource.NewGateWithClock(clk)
	writer := rewrite.NewStoreUnroutedWriter(s, gen, clk)

	deps := HandlerDeps{
		Generator:      gen,
		AuthDigest:     testAuthDigest(),
		Gate:           gate,
		Clock:          clk,
		AdmissionStore: nil,
		UnroutedWriter: writer,
	}

	handler := BuildHandler(deps, proxy.Handlers{
		Models:          proxy.ServeModels,
		ChatCompletions: chat,
	})

	return &handlerFixture{t: t, handler: handler, store: s, gate: gate, clk: clk}
}

func (f *handlerFixture) do(method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	f.handler.ServeHTTP(rec, req)
	return rec
}

func assertEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode proxy.ErrorCode) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d; body: %s", rec.Code, wantStatus, rec.Body.String())
	}
	var env proxy.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", env.Error.Code, wantCode)
	}
}

func TestBuildHandler_StageRejections(t *testing.T) {
	fixture := newHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat handler ran for a rejected request")
	})

	tests := []struct {
		name       string
		method     string
		path       string
		body       string
		headers    map[string]string
		wantStatus int
		wantCode   proxy.ErrorCode
		wantID     bool
	}{
		{
			name:       "unknown path is anonymous",
			method:     http.MethodGet,
			path:       "/unknown",
			wantStatus: http.StatusNotFound,
			wantCode:   proxy.ErrNotFound,
			wantID:     false,
		},
		{
			name:       "wrong method is anonymous",
			method:     http.MethodGet,
			path:       "/v1/chat/completions",
			wantStatus: http.StatusMethodNotAllowed,
			wantCode:   proxy.ErrMethodNotAllowed,
			wantID:     false,
		},
		{
			name:       "query string is anonymous",
			method:     http.MethodPost,
			path:       "/v1/chat/completions?foo=bar",
			wantStatus: http.StatusBadRequest,
			wantCode:   proxy.ErrQueryNotSupported,
			wantID:     false,
		},
		{
			name:       "bad credential carries an identifier",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			body:       `{"model":"kimi-k2.7"}`,
			headers:    map[string]string{"Authorization": "Bearer wrong"},
			wantStatus: http.StatusUnauthorized,
			wantCode:   proxy.ErrInvalidAPIKey,
			wantID:     true,
		},
		{
			name:       "compressed body carries an identifier",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			body:       `{"model":"kimi-k2.7"}`,
			headers:    map[string]string{"Authorization": "Bearer " + testBearerKey, "Content-Encoding": "gzip"},
			wantStatus: http.StatusUnsupportedMediaType,
			wantCode:   proxy.ErrUnsupportedContentEncoding,
			wantID:     true,
		},
		{
			name:       "invalid envelope carries an identifier",
			method:     http.MethodPost,
			path:       "/v1/chat/completions",
			body:       `{"model":`,
			headers:    map[string]string{"Authorization": "Bearer " + testBearerKey},
			wantStatus: http.StatusBadRequest,
			wantCode:   proxy.ErrInvalidRequest,
			wantID:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := fixture.do(tt.method, tt.path, tt.body, tt.headers)
			assertEnvelope(t, rec, tt.wantStatus, tt.wantCode)

			reqID := rec.Header().Get("X-LLMux-Request-ID")
			if tt.wantID && reqID == "" {
				t.Error("expected X-LLMux-Request-ID, got empty")
			}
			if !tt.wantID && reqID != "" {
				t.Errorf("expected no X-LLMux-Request-ID, got %q", reqID)
			}
		})
	}
}

func TestBuildHandler_CompressedBodyLeavesOneRow(t *testing.T) {
	fixture := newHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat handler ran for a compressed body")
	})

	rec := fixture.do(http.MethodPost, "/v1/chat/completions", `{"model":"kimi-k2.7"}`, map[string]string{
		"Authorization":    "Bearer " + testBearerKey,
		"Content-Encoding": "gzip",
	})

	assertEnvelope(t, rec, http.StatusUnsupportedMediaType, proxy.ErrUnsupportedContentEncoding)

	reqID := rec.Header().Get("X-LLMux-Request-ID")
	if reqID == "" {
		t.Fatal("missing X-LLMux-Request-ID")
	}

	var count int
	if err := fixture.store.Writer.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM unrouted_request WHERE logical_request_id = ?
	`, reqID).Scan(&count); err != nil {
		t.Fatalf("count unrouted_request: %v", err)
	}
	if count != 1 {
		t.Errorf("unrouted_request count = %d, want 1", count)
	}
}

func TestBuildHandler_OversizeBodyCarriesID(t *testing.T) {
	fixture := newHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat handler ran for an oversize body")
	})

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("x"))
	req.ContentLength = policy.MaxRequestBodyBytes + 1
	req.Header.Set("Authorization", "Bearer "+testBearerKey)

	rec := httptest.NewRecorder()
	fixture.handler.ServeHTTP(rec, req)

	assertEnvelope(t, rec, http.StatusRequestEntityTooLarge, proxy.ErrRequestTooLarge)
	if rec.Header().Get("X-LLMux-Request-ID") == "" {
		t.Error("expected X-LLMux-Request-ID on oversize rejection")
	}
}

func TestBuildHandler_OverloadCarriesID(t *testing.T) {
	fixture := newHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat handler ran for an overloaded request")
	})

	// Exhaust the memory budget so the body charge fails with overload.
	if err := fixture.gate.AcquireMemory(policy.AggregateMemoryBudgetBytes); err != nil {
		t.Fatalf("exhaust memory: %v", err)
	}

	rec := fixture.do(http.MethodPost, "/v1/chat/completions", `{"model":"kimi-k2.7"}`, map[string]string{
		"Authorization": "Bearer " + testBearerKey,
	})

	assertEnvelope(t, rec, http.StatusTooManyRequests, proxy.ErrProxyOverloaded)
	if rec.Header().Get("X-LLMux-Request-ID") == "" {
		t.Error("expected X-LLMux-Request-ID on overload rejection")
	}
}

func TestBuildHandler_ModelsRoute(t *testing.T) {
	fixture := newHandlerFixture(t, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("chat handler must not run for the models route")
	})

	rec := fixture.do(http.MethodGet, "/v1/models", "", map[string]string{
		"Authorization": "Bearer " + testBearerKey,
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
	}

	// A successful models response is not a chat response, so it carries no
	// request ID; the identifier is only written on rejections and chat
	// responses.
	if rec.Header().Get("X-LLMux-Request-ID") != "" {
		t.Errorf("expected no X-LLMux-Request-ID on a successful models response, got %q", rec.Header().Get("X-LLMux-Request-ID"))
	}

	// A rejected credential on the models route is on the identity side and
	// therefore carries the identifier.
	bad := fixture.do(http.MethodGet, "/v1/models", "", map[string]string{
		"Authorization": "Bearer wrong",
	})
	assertEnvelope(t, bad, http.StatusUnauthorized, proxy.ErrInvalidAPIKey)
	if bad.Header().Get("X-LLMux-Request-ID") == "" {
		t.Error("expected X-LLMux-Request-ID on a rejected models credential")
	}
}
