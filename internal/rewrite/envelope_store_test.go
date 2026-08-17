package rewrite

import (
	"bytes"
	"context"
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
	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// storeEnvelopeFixture builds the real production middleware chain with a
// live SQLite store so that rejection paths leave the expected evidence.
type storeEnvelopeFixture struct {
	t       *testing.T
	store   *store.Store
	clk     *testsupport.FakeClock
	gen     idgen.Generator
	gate    *resource.Gate
	writer  *StoreUnroutedWriter
	handler http.HandlerFunc
}

func newStoreEnvelopeFixture(t *testing.T, next http.HandlerFunc) *storeEnvelopeFixture {
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
	writer := NewStoreUnroutedWriter(s, gen, clk)

	envelope := RequireScannedEnvelope(writer, next)
	bounded := RequireBoundedBody(writer, envelope)
	resourced := resource.RequireResources(gate, nil, clk, bounded)
	handler := proxy.AssignRequestID(gen, proxy.RequireLogicalDeadline(resourced))

	return &storeEnvelopeFixture{
		t:       t,
		store:   s,
		clk:     clk,
		gen:     gen,
		gate:    gate,
		writer:  writer,
		handler: handler,
	}
}

func (f *storeEnvelopeFixture) post(body string) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader(body))
	rec := httptest.NewRecorder()
	f.handler(rec, req)
	return rec
}

func (f *storeEnvelopeFixture) postWithLength(contentLength int64) *httptest.ResponseRecorder {
	f.t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", strings.NewReader("x"))
	req.ContentLength = contentLength
	rec := httptest.NewRecorder()
	f.handler(rec, req)
	return rec
}

func (f *storeEnvelopeFixture) assertOneUnroutedRow(rec *httptest.ResponseRecorder, wantCode proxy.ErrorCode, wantStatus int) string {
	f.t.Helper()

	reqID := rec.Header().Get("X-LLMux-Request-ID")
	if reqID == "" {
		f.t.Fatalf("response missing X-LLMux-Request-ID")
	}

	var status int
	var code string
	if err := f.store.Writer.QueryRowContext(context.Background(), `
		SELECT downstream_status, local_error_code
		FROM unrouted_request
		WHERE logical_request_id = ?
	`, reqID).Scan(&status, &code); err != nil {
		f.t.Fatalf("query unrouted_request for %s: %v", reqID, err)
	}
	if status != wantStatus {
		f.t.Errorf("unrouted downstream_status = %d, want %d", status, wantStatus)
	}
	if code != string(wantCode) {
		f.t.Errorf("unrouted local_error_code = %q, want %q", code, wantCode)
	}

	var unroutedCount int
	if err := f.store.Writer.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM unrouted_request WHERE logical_request_id = ?
	`, reqID).Scan(&unroutedCount); err != nil {
		f.t.Fatalf("count unrouted_request: %v", err)
	}
	if unroutedCount != 1 {
		f.t.Errorf("unrouted_request count = %d, want 1", unroutedCount)
	}

	var attemptCount int
	if err := f.store.Writer.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = ?
	`, reqID).Scan(&attemptCount); err != nil {
		f.t.Fatalf("count attempt_log: %v", err)
	}
	if attemptCount != 0 {
		f.t.Errorf("attempt_log count = %d, want 0", attemptCount)
	}

	return reqID
}

func assertErrorEnvelope(t *testing.T, rec *httptest.ResponseRecorder, wantStatus int, wantCode proxy.ErrorCode) {
	t.Helper()
	if rec.Code != wantStatus {
		t.Errorf("status = %d, want %d", rec.Code, wantStatus)
	}
	var env proxy.ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode error envelope: %v\nbody: %s", err, rec.Body.String())
	}
	if env.Error.Code != wantCode {
		t.Errorf("error code = %q, want %q", env.Error.Code, wantCode)
	}
}

func TestStoreUnroutedWriter_RecordsEveryEnvelopeRejection(t *testing.T) {
	overDepth := `{"model":"kimi-k2.7","x":` + strings.Repeat("[", policy.MaxJSONNestingDepth) + `1` + strings.Repeat("]", policy.MaxJSONNestingDepth) + `}`
	cases := []struct {
		name       string
		body       string
		contentLen int64
		wantStatus int
		wantCode   proxy.ErrorCode
	}{
		{name: "invalid JSON", body: `{"model":"kimi-k2.7"`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "non-object top level", body: `[]`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "missing model", body: `{}`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "non-string model", body: `{"model":1}`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "duplicate model", body: `{"model":"kimi-k2.7","model":"kimi-k2.7"}`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "duplicate stream", body: `{"model":"kimi-k2.7","stream":true,"stream":false}`, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrInvalidRequest},
		{name: "depth exceeded", body: overDepth, wantStatus: http.StatusBadRequest, wantCode: proxy.ErrJSONDepthExceeded},
		{name: "unknown alias", body: `{"model":"unknown"}`, wantStatus: http.StatusNotFound, wantCode: proxy.ErrModelNotFound},
		{name: "body over 64 MiB", contentLen: int64(policy.MaxRequestBodyBytes) + 1, wantStatus: http.StatusRequestEntityTooLarge, wantCode: proxy.ErrRequestTooLarge},
	}

	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			fixture := newStoreEnvelopeFixture(t, func(http.ResponseWriter, *http.Request) {
				t.Fatal("next handler ran for a rejected envelope")
			})

			var rec *httptest.ResponseRecorder
			if test.contentLen != 0 {
				rec = fixture.postWithLength(test.contentLen)
			} else {
				rec = fixture.post(test.body)
			}

			assertErrorEnvelope(t, rec, test.wantStatus, test.wantCode)
			fixture.assertOneUnroutedRow(rec, test.wantCode, test.wantStatus)
		})
	}
}

func TestStoreUnroutedWriter_AcceptedRequestLeavesNoRow(t *testing.T) {
	body := `{"model":"kimi-k2.7","unrecognized":1,"unrecognized":{"nested":[true,false]},"vendor_field":"value"}`
	var gotBody []byte
	var gotOK bool

	fixture := newStoreEnvelopeFixture(t, func(w http.ResponseWriter, r *http.Request) {
		gotBody, gotOK = RequestBody(r.Context())
		w.WriteHeader(http.StatusNoContent)
	})

	rec := fixture.post(body)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNoContent)
	}
	if !gotOK {
		t.Fatal("expected request body in context")
	}
	if string(gotBody) != body {
		t.Errorf("body was modified: got %q, want %q", string(gotBody), body)
	}

	var count int
	if err := fixture.store.Writer.QueryRowContext(context.Background(), `SELECT COUNT(*) FROM unrouted_request`).Scan(&count); err != nil {
		t.Fatalf("count unrouted_request: %v", err)
	}
	if count != 0 {
		t.Errorf("unrouted_request count = %d, want 0", count)
	}
}

func TestStoreUnroutedWriter_WriterFailureDoesNotChangeResponse(t *testing.T) {
	fixture := newStoreEnvelopeFixture(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("next handler ran for a rejected envelope")
	})
	if err := fixture.store.Writer.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	rec := fixture.post(`{}`)
	assertErrorEnvelope(t, rec, http.StatusBadRequest, proxy.ErrInvalidRequest)
}
