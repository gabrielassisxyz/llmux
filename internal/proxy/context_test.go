package proxy

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/idgen"
)

func newTestGenerator() (idgen.Generator, string) {
	// A 16-byte fixed payload so generator.newID() succeeds and returns its hex
	entropy := bytes.Repeat([]byte{0xaa}, 16)
	gen := idgen.NewGenerator(bytes.NewReader(entropy))
	return gen, hex.EncodeToString(entropy)
}

func TestAssignRequestID_Success(t *testing.T) {
	gen, expectedID := newTestGenerator()

	handler := func(w http.ResponseWriter, r *http.Request) {
		id, ok := RequestID(r.Context())
		if !ok || id != expectedID {
			t.Errorf("expected request ID %q in context, got %q (ok=%v)", expectedID, id, ok)
		}
		w.WriteHeader(http.StatusOK)
	}

	wrapped := AssignRequestID(gen, handler)

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	wrapped(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}
}

func TestRequireLogicalDeadline_DeadlineExceeded(t *testing.T) {
	gen, expectedID := newTestGenerator()

	handler := func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}

	wrapped := AssignRequestID(gen, RequireLogicalDeadline(handler))

	// Create a parent context that times out immediately
	parentCtx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()

	<-parentCtx.Done() // wait for it to expire

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(parentCtx)

	w := httptest.NewRecorder()
	wrapped(w, req)

	res := w.Result()
	if res.StatusCode != 504 {
		t.Errorf("expected status 504, got %d", res.StatusCode)
	}

	if reqID := res.Header.Get("X-LLMux-Request-ID"); reqID != expectedID {
		t.Errorf("expected request ID %q, got %q", expectedID, reqID)
	}

	var env ErrorEnvelope
	if err := json.NewDecoder(res.Body).Decode(&env); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	if env.Error.Code != ErrDeadlineExceeded {
		t.Errorf("expected error code %q, got %q", ErrDeadlineExceeded, env.Error.Code)
	}
}

func TestRequireLogicalDeadline_ClientCancel(t *testing.T) {
	gen, _ := newTestGenerator()

	handler := func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}

	wrapped := AssignRequestID(gen, RequireLogicalDeadline(handler))

	// Create a parent context that is canceled, not timed out
	parentCtx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req = req.WithContext(parentCtx)

	w := httptest.NewRecorder()
	wrapped(w, req)

	res := w.Result()
	// Should not write any response because it was a cancellation, not a deadline exceeded
	if res.StatusCode != 200 { // httptest.NewRecorder defaults to 200 if nothing is written
		t.Errorf("expected no error status, got %d", res.StatusCode)
	}
	if w.Body.Len() > 0 {
		t.Errorf("expected empty body, got %q", w.Body.String())
	}
}

func TestRequireLogicalDeadline_Success(t *testing.T) {
	gen, _ := newTestGenerator()

	handler := func(w http.ResponseWriter, r *http.Request) {
		// check deadline is set
		dl, ok := r.Context().Deadline()
		if !ok {
			t.Errorf("expected deadline to be set in context")
		} else {
			// should be ~10 minutes from now
			diff := time.Until(dl)
			if diff < 9*time.Minute || diff > 10*time.Minute {
				t.Errorf("expected deadline ~10m, got %v", diff)
			}
		}

		w.WriteHeader(200)
		_, _ = w.Write([]byte("ok"))
	}

	wrapped := AssignRequestID(gen, RequireLogicalDeadline(handler))

	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	w := httptest.NewRecorder()
	wrapped(w, req)

	res := w.Result()
	if res.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", res.StatusCode)
	}
}
