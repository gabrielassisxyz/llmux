package rewrite

import (
	"bytes"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
)

func TestRequireIdentityEncoding_AcceptsIdentityOrAbsent(t *testing.T) {
	tests := []struct {
		name      string
		encodings []string
	}{
		{"absent", nil},
		{"identity", []string{"identity"}},
		{"identity mixed case", []string{"Identity"}},
		{"identity with padding", []string{" identity "}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := RequireIdentityEncoding(nil, func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for _, encoding := range tt.encodings {
				req.Header.Add("Content-Encoding", encoding)
			}
			handler(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200", rec.Code)
			}
			if !called {
				t.Error("handler was not invoked")
			}
		})
	}
}

func TestRequireIdentityEncoding_RejectsCompressed(t *testing.T) {
	tests := []struct {
		name      string
		encodings []string
	}{
		{"gzip", []string{"gzip"}},
		{"empty string", []string{""}},
		{"compound value", []string{"identity, gzip"}},
		{"duplicate identity headers", []string{"identity", "identity"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			handler := RequireIdentityEncoding(nil, func(w http.ResponseWriter, r *http.Request) {
				called = true
				w.WriteHeader(http.StatusOK)
			})

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for _, encoding := range tt.encodings {
				req.Header.Add("Content-Encoding", encoding)
			}
			handler(rec, req)

			if rec.Code != http.StatusUnsupportedMediaType {
				t.Fatalf("status = %d, want 415", rec.Code)
			}
			if called {
				t.Error("a compressed body must not reach the handler")
			}
		})
	}
}

func TestRequireIdentityEncoding_RejectionCarriesIDAndRow(t *testing.T) {
	entropy := bytes.Repeat([]byte{0xbb}, 16)
	gen := idgen.NewGenerator(bytes.NewReader(entropy))
	expectedID := hex.EncodeToString(entropy)

	writer := &fakeUnroutedRequestWriter{}
	handler := proxy.AssignRequestID(gen, RequireIdentityEncoding(writer, func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("handler ran for a compressed body")
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Content-Encoding", "gzip")
	handler(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	if reqID := rec.Header().Get("X-LLMux-Request-ID"); reqID != expectedID {
		t.Errorf("X-LLMux-Request-ID = %q, want %q", reqID, expectedID)
	}
	codes := writer.recordedCodes()
	if len(codes) != 1 || codes[0] != proxy.ErrUnsupportedContentEncoding {
		t.Errorf("recorded codes = %v, want [%s]", codes, proxy.ErrUnsupportedContentEncoding)
	}
}
