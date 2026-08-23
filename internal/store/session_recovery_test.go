package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func openStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// insertDispatch writes one dispatch_admission and one attempt_log row with
// the given outcome and session/account values. finishedAt and logicalElapsed
// are the two columns the recovery query derives the arrival instant from.
func insertDispatch(t *testing.T, s *store.Store, ctx context.Context, recordID, requestID, attemptID, sessionKey, accountLabel, outcome string, finishedAt, logicalElapsed int64) {
	t.Helper()
	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES (?, ?, 1, ?, 'alias', 'upstream', ?, 0, 0)
	`, attemptID, requestID, accountLabel, finishedAt); err != nil {
		t.Fatalf("insert admission: %v", err)
	}
	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, session_key, account_label, attempt_no,
			is_spill, event_at_us, finished_at_us, selection_wait_us, attempt_duration_us,
			logical_elapsed_us, outcome, retry_disposition, response_committed, usage_observation
		) VALUES (?, ?, ?, 1, 1, 'dispatch', 'alias', 'base', 'upstream', ?, ?, 1, 0, ?, ?, 0, 0, ?, ?, 'final', 1, 'complete')
	`, recordID, requestID, attemptID, sessionKey, accountLabel, finishedAt, finishedAt, logicalElapsed, outcome); err != nil {
		t.Fatalf("insert attempt: %v", err)
	}
}

func TestRecoverSessionPinsBasic(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	insertDispatch(t, s, ctx, "rec-1", "req-1", "att-1", "s1", "k1", "succeeded", 100, 50)

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k1" {
		t.Errorf("pins[s1].AccountLabel = %q, want k1", got)
	}
	if got := pins["s1"].FinishedAtUS; got != 100 {
		t.Errorf("pins[s1].FinishedAtUS = %d, want 100", got)
	}
}

// TestRecoverSessionPinsOutOfOrderArrival proves the account of the later
// arrival wins even when the later arrival finished first. The live sequence
// guard orders by arrival, so recovery must match it rather than completion
// order.
func TestRecoverSessionPinsOutOfOrderArrival(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// Finished later, arrived earlier: arrival 200-150 = 50.
	insertDispatch(t, s, ctx, "rec-a", "req-a", "att-a", "s1", "k1", "succeeded", 200, 150)
	// Finished earlier, arrived later: arrival 100-10 = 90.
	insertDispatch(t, s, ctx, "rec-b", "req-b", "att-b", "s1", "k2", "succeeded", 100, 10)

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k2" {
		t.Errorf("pins[s1].AccountLabel = %q, want k2 (later arrival)", got)
	}
	if got := pins["s1"].FinishedAtUS; got != 100 {
		t.Errorf("pins[s1].FinishedAtUS = %d, want 100 (the winner's finish, not the later finish)", got)
	}
}

// TestRecoverSessionPinsTieBreakFinishedAt proves equal derived arrivals go to
// the later finished_at_us.
func TestRecoverSessionPinsTieBreakFinishedAt(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// Arrival 200-150 = 50, finished 200.
	insertDispatch(t, s, ctx, "rec-a", "req-a", "att-a", "s1", "k1", "succeeded", 200, 150)
	// Arrival 100-50 = 50, finished 100.
	insertDispatch(t, s, ctx, "rec-b", "req-b", "att-b", "s1", "k2", "succeeded", 100, 50)

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k1" {
		t.Errorf("pins[s1].AccountLabel = %q, want k1 (later finished_at_us)", got)
	}
}

// TestRecoverSessionPinsTieBreakRecordID proves equal derived arrivals and
// equal finished_at_us go to the greater record_id, deterministically rather
// than by the order the rows came back in.
func TestRecoverSessionPinsTieBreakRecordID(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	insertDispatch(t, s, ctx, "rec-a", "req-a", "att-a", "s1", "k1", "succeeded", 100, 50)
	insertDispatch(t, s, ctx, "rec-b", "req-b", "att-b", "s1", "k2", "succeeded", 100, 50)

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k2" {
		t.Errorf("pins[s1].AccountLabel = %q, want k2 (greater record_id)", got)
	}
}

// TestRecoverSessionPinsExpiredNotRecovered proves a completion older than the
// hour bound is not recovered.
func TestRecoverSessionPinsExpiredNotRecovered(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	insertDispatch(t, s, ctx, "rec-1", "req-1", "att-1", "s1", "k1", "succeeded", 50, 10)

	pins, err := s.RecoverSessionPins(ctx, 100)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if _, ok := pins["s1"]; ok {
		t.Errorf("expired session recovered: %v", pins)
	}
}

// TestRecoverSessionPinsFailedSpillIgnored proves a failed attempt is not
// recovered and the newest successful account wins after a spill.
func TestRecoverSessionPinsFailedSpillIgnored(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	// Failed spill on k1, finished later.
	insertDispatch(t, s, ctx, "rec-fail", "req-fail", "att-fail", "s1", "k1", "upstream_http_error", 200, 10)
	// Successful completion on k2, finished earlier.
	insertDispatch(t, s, ctx, "rec-ok", "req-ok", "att-ok", "s1", "k2", "succeeded", 100, 10)

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k2" {
		t.Errorf("pins[s1].AccountLabel = %q, want k2 (successful account)", got)
	}
}

// TestRecoverSessionPinsReadsNoRateState proves the query reads only
// attempt_log: a dispatch_admission seeded with rate state and no terminal row
// changes nothing about the recovered pins.
func TestRecoverSessionPinsReadsNoRateState(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()
	insertDispatch(t, s, ctx, "rec-1", "req-1", "att-1", "s1", "k1", "succeeded", 100, 50)

	// An admission with rate state and no attempt_log row. The recovery query
	// must not read it.
	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES ('att-orphan', 'req-orphan', 1, 'k2', 'alias', 'upstream', 200, 59, 12)
	`); err != nil {
		t.Fatalf("insert orphan admission: %v", err)
	}

	pins, err := s.RecoverSessionPins(ctx, 0)
	if err != nil {
		t.Fatalf("RecoverSessionPins() error = %v", err)
	}
	if got := pins["s1"].AccountLabel; got != "k1" {
		t.Errorf("pins[s1].AccountLabel = %q, want k1", got)
	}
	if len(pins) != 1 {
		t.Errorf("len(pins) = %d, want 1", len(pins))
	}
}
