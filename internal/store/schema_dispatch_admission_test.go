package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestDispatchAdmissionSchemaRejectsImpossibleRows(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	insert := func(attemptID, requestID string, attemptNo int, account string, rpm, inFlight int) error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO dispatch_admission (
				attempt_id, logical_request_id, attempt_no, account_label,
				requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
			) VALUES (?, ?, ?, ?, 'alias', 'upstream', 100, ?, ?)
		`, attemptID, requestID, attemptNo, account, rpm, inFlight)
		return err
	}
	if err := insert("attempt-1", "request-1", 1, "k1", 1, 1); err != nil {
		t.Fatalf("valid insert: %v", err)
	}
	for _, test := range []struct {
		name                          string
		attemptID, requestID, account string
		attemptNo, rpm, inFlight      int
	}{
		{"duplicate attempt ID", "attempt-1", "request-2", "k1", 1, 1, 1},
		{"duplicate logical attempt", "attempt-2", "request-1", "k2", 1, 1, 1},
		{"zero attempt number", "attempt-3", "request-3", "k1", 0, 1, 1},
		{"unknown account", "attempt-4", "request-4", "unknown", 1, 1, 1},
		{"negative RPM", "attempt-5", "request-5", "k1", 1, -1, 1},
		{"negative in flight", "attempt-6", "request-6", "k1", 1, 1, -1},
	} {
		t.Run(test.name, func(t *testing.T) {
			if err := insert(test.attemptID, test.requestID, test.attemptNo, test.account, test.rpm, test.inFlight); err == nil {
				t.Fatal("impossible dispatch admission row was accepted")
			}
		})
	}
}
