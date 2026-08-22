package proxy

import (
	"bytes"
	"context"
	"crypto/sha256"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// recordingUnroutedWriter captures the codes a rejected request was answered
// with, so the auth tests can prove a rejected credential leaves exactly one
// unrouted_request row and an accepted one leaves none.
type recordingUnroutedWriter struct {
	codes []ErrorCode
}

func (w *recordingUnroutedWriter) RecordUnroutedRequest(_ context.Context, code ErrorCode) error {
	w.codes = append(w.codes, code)
	return nil
}

func TestRequireBearerAuth(t *testing.T) {
	expectedKey := "12345678901234567890123456789012"
	expectedDigest := sha256.Sum256([]byte(expectedKey))

	handlerCalled := false
	var bodyRead []byte
	nextHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		handlerCalled = true
		b, _ := io.ReadAll(r.Body)
		bodyRead = b
		w.WriteHeader(http.StatusOK)
	})

	writer := &recordingUnroutedWriter{}
	middleware := RequireBearerAuth(writer, expectedDigest, nextHandler)

	tests := []struct {
		name           string
		setupHeaders   func(req *http.Request)
		expectedStatus int
	}{
		{
			name: "correct bearer key",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+expectedKey)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name: "case-insensitive bearer scheme",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "bearer "+expectedKey)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:           "missing header",
			setupHeaders:   func(req *http.Request) {},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong scheme",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Basic "+expectedKey)
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "empty bearer value",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer ")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "several credentials in one header field",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+expectedKey+" "+expectedKey)
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "several Authorization header fields",
			setupHeaders: func(req *http.Request) {
				req.Header.Add("Authorization", "Bearer wrongkey")
				req.Header.Add("Authorization", "Bearer "+expectedKey)
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong key of equal length",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+"22345678901234567890123456789012")
			},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name: "wrong key of different length",
			setupHeaders: func(req *http.Request) {
				req.Header.Set("Authorization", "Bearer "+"short")
			},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handlerCalled = false
			bodyRead = nil
			writer.codes = nil

			body := []byte("request body")
			req := httptest.NewRequest(http.MethodPost, "/", bytes.NewReader(body))
			tt.setupHeaders(req)

			rec := httptest.NewRecorder()
			middleware.ServeHTTP(rec, req)

			if rec.Code != tt.expectedStatus {
				t.Errorf("expected status %d, got %d", tt.expectedStatus, rec.Code)
			}

			if tt.expectedStatus == http.StatusOK {
				if !handlerCalled {
					t.Error("expected next handler to be called")
				}
				if string(bodyRead) != string(body) {
					t.Errorf("expected body %q, got %q", string(body), string(bodyRead))
				}
				if len(writer.codes) != 0 {
					t.Errorf("valid credential recorded %d rejections, want 0", len(writer.codes))
				}
			} else {
				if handlerCalled {
					t.Error("expected next handler NOT to be called")
				}

				// Verify WWW-Authenticate header
				if www := rec.Header().Get("WWW-Authenticate"); www != "Bearer" {
					t.Errorf("expected WWW-Authenticate: Bearer, got %q", www)
				}

				// Verify JSON error envelope
				out := rec.Body.String()
				if !strings.Contains(out, "invalid_api_key") || !strings.Contains(out, "authentication_error") {
					t.Errorf("expected authentication error envelope, got %q", out)
				}
				if len(writer.codes) != 1 || writer.codes[0] != ErrInvalidAPIKey {
					t.Errorf("recorded codes = %v, want exactly [%s]", writer.codes, ErrInvalidAPIKey)
				}
			}
		})
	}
}

func TestRequireBearerAuth_NoBodyRead(t *testing.T) {
	// "The before-body-read case is the one that needs care: assert it by sending a body the server would
	// otherwise have to read, and confirming the 401 arrives without it being consumed"

	expectedKey := "12345678901234567890123456789012"
	expectedDigest := sha256.Sum256([]byte(expectedKey))

	writer := &recordingUnroutedWriter{}
	middleware := RequireBearerAuth(writer, expectedDigest, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("next handler should not be called")
	}))

	bodyStr := "a very large body that should not be read"
	bodyReader := strings.NewReader(bodyStr)
	req := httptest.NewRequest(http.MethodPost, "/", bodyReader)
	// no auth header to trigger 401

	rec := httptest.NewRecorder()
	middleware.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", rec.Code)
	}

	if len(writer.codes) != 1 || writer.codes[0] != ErrInvalidAPIKey {
		t.Errorf("recorded codes = %v, want exactly [%s]", writer.codes, ErrInvalidAPIKey)
	}

	// Assert body was not consumed
	unread := bodyReader.Len()
	if unread != len(bodyStr) {
		t.Errorf("expected body to be unread (length %d), but %d bytes remain", len(bodyStr), unread)
	}
}
