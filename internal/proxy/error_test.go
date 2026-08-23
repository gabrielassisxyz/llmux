package proxy

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"
)

func TestWriteError(t *testing.T) {
	for code, def := range errorsByCode {
		t.Run(string(code), func(t *testing.T) {
			rec := httptest.NewRecorder()
			reqID := "req-123"
			WriteError(rec, reqID, code)

			if rec.Code != def.Status {
				t.Errorf("expected status %d, got %d", def.Status, rec.Code)
			}
			if rec.Header().Get("X-LLMux-Request-ID") != reqID {
				t.Errorf("expected X-LLMux-Request-ID %s, got %s", reqID, rec.Header().Get("X-LLMux-Request-ID"))
			}

			var env ErrorEnvelope
			if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			if env.Error.Code != def.Code {
				t.Errorf("expected code %s, got %s", def.Code, env.Error.Code)
			}
			if env.Error.Type != def.Type {
				t.Errorf("expected type %s, got %s", def.Type, env.Error.Type)
			}
			if env.Error.Message != def.Message {
				t.Errorf("expected msg %s, got %s", def.Message, env.Error.Message)
			}
		})
	}
}

func TestWriteRateLimitError(t *testing.T) {
	rec := httptest.NewRecorder()
	now := time.Date(2026, 8, 13, 12, 0, 0, 0, time.UTC)
	reopen := now.Add(1500 * time.Millisecond)

	WriteRateLimitError(rec, "req-456", ErrAccountCapacityTimeout, reopen, now)

	if rec.Code != 429 {
		t.Errorf("expected 429, got %d", rec.Code)
	}
	if rec.Header().Get("Retry-After") != "2" {
		t.Errorf("expected Retry-After 2, got %s", rec.Header().Get("Retry-After"))
	}
}
