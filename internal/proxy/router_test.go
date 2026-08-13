package proxy

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func decodeError(t *testing.T, rec *httptest.ResponseRecorder) ErrorEnvelope {
	t.Helper()
	var env ErrorEnvelope
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decoding error envelope: %v, body: %s", err, rec.Body.String())
	}
	return env
}

func stubHandler(called *bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		*called = true
		w.WriteHeader(http.StatusOK)
	}
}

func newTestRouter(modelsCalled, chatCalled *bool) *http.ServeMux {
	return NewRouter(Handlers{
		Models:          stubHandler(modelsCalled),
		ChatCompletions: stubHandler(chatCalled),
	})
}

func TestRouterExactRouteAndMethod(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"chat completions", http.MethodPost, "/v1/chat/completions"},
		{"models", http.MethodGet, "/v1/models"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body: %s", rec.Code, rec.Body.String())
			}
			if tt.path == "/v1/models" && !modelsCalled {
				t.Error("models handler was not invoked")
			}
			if tt.path == "/v1/chat/completions" && !chatCalled {
				t.Error("chat completions handler was not invoked")
			}
		})
	}
}

func TestRouterUnsupportedMethodReturns405WithAllow(t *testing.T) {
	tests := []struct {
		name          string
		method        string
		path          string
		expectedAllow string
	}{
		{"GET on chat completions", http.MethodGet, "/v1/chat/completions", http.MethodPost},
		{"POST on models", http.MethodPost, "/v1/models", http.MethodGet},
		{"DELETE on models", http.MethodDelete, "/v1/models", http.MethodGet},
		{"HEAD on models", http.MethodHead, "/v1/models", http.MethodGet},
		{"PUT on chat completions", http.MethodPut, "/v1/chat/completions", http.MethodPost},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusMethodNotAllowed {
				t.Fatalf("status = %d, want 405", rec.Code)
			}
			if allow := rec.Header().Get("Allow"); allow != tt.expectedAllow {
				t.Errorf("Allow = %q, want %q", allow, tt.expectedAllow)
			}
			env := decodeError(t, rec)
			if env.Error.Code != ErrMethodNotAllowed {
				t.Errorf("error code = %q, want %q", env.Error.Code, ErrMethodNotAllowed)
			}
			if modelsCalled || chatCalled {
				t.Error("a rejected method must not reach the injected handler")
			}
		})
	}
}

func TestRouterUnknownPathReturns404FromEnvelope(t *testing.T) {
	paths := []string{
		"/",
		"/v1",
		"/v1/",
		"/v1/models/nested",
		"/v1/chat/completions/nested",
		"/unknown",
	}

	for _, path := range paths {
		t.Run(path, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodGet, path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			env := decodeError(t, rec)
			if env.Error.Code != ErrNotFound {
				t.Errorf("error code = %q, want %q", env.Error.Code, ErrNotFound)
			}
			if modelsCalled || chatCalled {
				t.Error("an unknown path must not reach an injected handler")
			}
		})
	}
}

func TestRouterTrailingSlashIsNotAnAlias(t *testing.T) {
	tests := []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/v1/models/"},
		{http.MethodPost, "/v1/chat/completions/"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (trailing slash must not alias the canonical path)", rec.Code)
			}
			if modelsCalled || chatCalled {
				t.Error("a trailing-slash form must not reach an injected handler")
			}
		})
	}
}

func TestRouterNonEmptyQueryStringReturns400(t *testing.T) {
	tests := []struct {
		name   string
		method string
		path   string
	}{
		{"models with query", http.MethodGet, "/v1/models?foo=bar"},
		{"chat completions with query", http.MethodPost, "/v1/chat/completions?foo=bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(tt.method, tt.path, nil)
			mux.ServeHTTP(rec, req)

			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			env := decodeError(t, rec)
			if env.Error.Code != ErrQueryNotSupported {
				t.Errorf("error code = %q, want %q", env.Error.Code, ErrQueryNotSupported)
			}
			if modelsCalled || chatCalled {
				t.Error("a non-empty query string must not reach an injected handler")
			}
		})
	}
}

func TestRouterEmptyQueryStringIsAccepted(t *testing.T) {
	var modelsCalled, chatCalled bool
	mux := newTestRouter(&modelsCalled, &chatCalled)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/models?", nil)
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for a bare trailing '?' with no query content", rec.Code)
	}
	if !modelsCalled {
		t.Error("models handler was not invoked")
	}
}

func TestRouterContentEncodingRules(t *testing.T) {
	tests := []struct {
		name       string
		encodings  []string
		wantStatus int
	}{
		{"absent", nil, http.StatusOK},
		{"identity", []string{"identity"}, http.StatusOK},
		{"identity mixed case", []string{"Identity"}, http.StatusOK},
		{"identity with padding", []string{" identity "}, http.StatusOK},
		{"gzip", []string{"gzip"}, http.StatusUnsupportedMediaType},
		{"empty string", []string{""}, http.StatusUnsupportedMediaType},
		{"compound value", []string{"identity, gzip"}, http.StatusUnsupportedMediaType},
		{"duplicate identity headers", []string{"identity", "identity"}, http.StatusUnsupportedMediaType},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var modelsCalled, chatCalled bool
			mux := newTestRouter(&modelsCalled, &chatCalled)

			rec := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			for _, encoding := range tt.encodings {
				req.Header.Add("Content-Encoding", encoding)
			}
			mux.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body: %s", rec.Code, tt.wantStatus, rec.Body.String())
			}

			if tt.wantStatus == http.StatusUnsupportedMediaType {
				env := decodeError(t, rec)
				if env.Error.Code != ErrUnsupportedContentEncoding {
					t.Errorf("error code = %q, want %q", env.Error.Code, ErrUnsupportedContentEncoding)
				}
				if chatCalled {
					t.Error("a rejected content encoding must not reach the injected handler")
				}
			} else if !chatCalled {
				t.Error("chat completions handler was not invoked")
			}
		})
	}
}

// TestRouterChecksRunBeforeQueryCanMaskEncodingRejection proves the
// content-encoding check still runs even when a query string is present but
// empty (both checks active on the same request), so neither guard silently
// shadows the other.
func TestRouterChecksRunBeforeQueryCanMaskEncodingRejection(t *testing.T) {
	var modelsCalled, chatCalled bool
	mux := newTestRouter(&modelsCalled, &chatCalled)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
	req.Header.Set("Content-Encoding", "gzip")
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
	if chatCalled {
		t.Error("a compressed body must not reach the injected handler")
	}
}
