package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestInsertDispatchAdmissionWritesRow(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	admission := store.DispatchAdmission{
		AttemptID:        "attempt-1",
		LogicalRequestID: "request-1",
		AttemptNo:        1,
		AccountLabel:     "k1",
		RequestedAlias:   "kimi-k2.7",
		UpstreamModel:    "kimi-k2.7-code:cloud",
		ReservedAtUS:     12345,
		LimiterRPMUsed:   2,
		LimiterInFlight:  3,
	}
	if err := s.InsertDispatchAdmission(context.Background(), admission); err != nil {
		t.Fatalf("InsertDispatchAdmission() error = %v", err)
	}

	row := readAdmission(t, s, "attempt-1")
	if row.logicalRequestID != "request-1" {
		t.Errorf("logical_request_id = %q, want request-1", row.logicalRequestID)
	}
	if row.attemptNo != 1 {
		t.Errorf("attempt_no = %d, want 1", row.attemptNo)
	}
	if row.accountLabel != "k1" {
		t.Errorf("account_label = %q, want k1", row.accountLabel)
	}
	if row.requestedAlias != "kimi-k2.7" {
		t.Errorf("requested_alias = %q, want kimi-k2.7", row.requestedAlias)
	}
	if row.upstreamModel != "kimi-k2.7-code:cloud" {
		t.Errorf("upstream_model = %q, want kimi-k2.7-code:cloud", row.upstreamModel)
	}
	if row.reservedAtUS != 12345 {
		t.Errorf("reserved_at_us = %d, want 12345", row.reservedAtUS)
	}
	if row.limiterRPMUsed != 2 {
		t.Errorf("limiter_rpm_used = %d, want 2", row.limiterRPMUsed)
	}
	if row.limiterInFlight != 3 {
		t.Errorf("limiter_in_flight = %d, want 3", row.limiterInFlight)
	}
}

func TestInsertDispatchAdmissionSurvivesReopen(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "llmux.db")

	s, err := store.Open(path)
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := s.InsertDispatchAdmission(context.Background(), store.DispatchAdmission{
		AttemptID:        "attempt-reopen",
		LogicalRequestID: "request-reopen",
		AttemptNo:        1,
		AccountLabel:     "k2",
		RequestedAlias:   "glm-5.2",
		UpstreamModel:    "glm-5.2:cloud",
		ReservedAtUS:     9999,
		LimiterRPMUsed:   1,
		LimiterInFlight:  1,
	}); err != nil {
		t.Fatalf("InsertDispatchAdmission() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	s2, err := store.Open(path)
	if err != nil {
		t.Fatalf("reopen store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s2.Close() })

	row := readAdmission(t, s2, "attempt-reopen")
	if row.logicalRequestID != "request-reopen" {
		t.Errorf("logical_request_id = %q, want request-reopen", row.logicalRequestID)
	}
	if row.accountLabel != "k2" {
		t.Errorf("account_label = %q, want k2", row.accountLabel)
	}
}

func TestInsertDispatchAdmissionFailureRollsBack(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	if err := s.InsertDispatchAdmission(context.Background(), store.DispatchAdmission{
		AttemptID:        "attempt-ok",
		LogicalRequestID: "request-ok",
		AttemptNo:        1,
		AccountLabel:     "k1",
		RequestedAlias:   "a",
		UpstreamModel:    "b",
		ReservedAtUS:     1,
		LimiterRPMUsed:   0,
		LimiterInFlight:  0,
	}); err != nil {
		t.Fatalf("valid InsertDispatchAdmission() error = %v", err)
	}

	// An invalid attempt number must be rejected by the schema and leave no second row.
	if err := s.InsertDispatchAdmission(context.Background(), store.DispatchAdmission{
		AttemptID:        "attempt-bad",
		LogicalRequestID: "request-ok",
		AttemptNo:        0,
		AccountLabel:     "k1",
		RequestedAlias:   "a",
		UpstreamModel:    "b",
		ReservedAtUS:     2,
		LimiterRPMUsed:   0,
		LimiterInFlight:  0,
	}); err == nil {
		t.Fatal("InsertDispatchAdmission() accepted an invalid attempt number")
	}

	count := countAdmissions(t, s, "request-ok")
	if count != 1 {
		t.Fatalf("logical_request_id request-ok has %d rows, want 1", count)
	}
}

func TestInsertDispatchAdmissionDuplicateAttemptIDLeavesNoRow(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	base := store.DispatchAdmission{
		AttemptID:       "attempt-dup",
		AttemptNo:       1,
		AccountLabel:    "k1",
		RequestedAlias:  "a",
		UpstreamModel:   "b",
		ReservedAtUS:    1,
		LimiterRPMUsed:  0,
		LimiterInFlight: 0,
	}
	first := base
	first.LogicalRequestID = "request-first"
	if err := s.InsertDispatchAdmission(context.Background(), first); err != nil {
		t.Fatalf("first InsertDispatchAdmission() error = %v", err)
	}

	second := base
	second.LogicalRequestID = "request-second"
	if err := s.InsertDispatchAdmission(context.Background(), second); err == nil {
		t.Fatal("InsertDispatchAdmission() accepted a duplicate attempt_id")
	}

	if readAdmission(t, s, "attempt-dup").logicalRequestID != "request-first" {
		t.Fatal("duplicate insert changed the existing admission row")
	}
	if countAdmissions(t, s, "request-second") != 0 {
		t.Fatal("duplicate insert created a row for the second logical request")
	}
}

func TestInsertDispatchAdmissionCanceledContextReturnsError(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err = s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID:        "attempt-cancel",
		LogicalRequestID: "request-cancel",
		AttemptNo:        1,
		AccountLabel:     "k1",
		RequestedAlias:   "a",
		UpstreamModel:    "b",
		ReservedAtUS:     1,
		LimiterRPMUsed:   0,
		LimiterInFlight:  0,
	})
	if err == nil {
		t.Fatal("InsertDispatchAdmission() succeeded with a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("InsertDispatchAdmission() error = %v, want context.Canceled", err)
	}

	if countAdmissions(t, s, "request-cancel") != 0 {
		t.Fatal("canceled insert left a row behind")
	}
}

type admissionRow struct {
	attemptID        string
	logicalRequestID string
	attemptNo        int
	accountLabel     string
	requestedAlias   string
	upstreamModel    string
	reservedAtUS     int64
	limiterRPMUsed   int
	limiterInFlight  int
}

func readAdmission(t *testing.T, s *store.Store, attemptID string) admissionRow {
	t.Helper()
	var row admissionRow
	var rpm, inFlight int64
	err := s.Writer.QueryRowContext(context.Background(), `
		SELECT attempt_id, logical_request_id, attempt_no, account_label,
		       requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		FROM dispatch_admission
		WHERE attempt_id = ?
	`, attemptID).Scan(&row.attemptID, &row.logicalRequestID, &row.attemptNo, &row.accountLabel,
		&row.requestedAlias, &row.upstreamModel, &row.reservedAtUS, &rpm, &inFlight)
	if err != nil {
		t.Fatalf("read admission row %q: %v", attemptID, err)
	}
	row.limiterRPMUsed = int(rpm)
	row.limiterInFlight = int(inFlight)
	return row
}

func countAdmissions(t *testing.T, s *store.Store, logicalRequestID string) int {
	t.Helper()
	var count int
	if err := s.Writer.QueryRowContext(context.Background(), `
		SELECT COUNT(*) FROM dispatch_admission WHERE logical_request_id = ?
	`, logicalRequestID).Scan(&count); err != nil {
		t.Fatalf("count admissions: %v", err)
	}
	return count
}
