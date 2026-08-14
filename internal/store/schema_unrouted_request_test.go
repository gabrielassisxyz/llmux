package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestUnroutedRequestLogicalRequestIDDisjointFromAttemptLog(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec-unrouted', 'req-unrouted', 1000, 2000,
			'session-a', 400, 'invalid_request'
		)
	`); err != nil {
		t.Fatalf("insert unrouted_request: %v", err)
	}

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES ('attempt-dispatched', 'req-dispatched', 1, 'k1', 'alias', 'upstream', 100, 0, 0)
	`); err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}
	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-dispatched', 'req-dispatched', 'attempt-dispatched', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'complete'
		)
	`); err != nil {
		t.Fatalf("insert dispatch attempt_log: %v", err)
	}

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, selection_wait_us, logical_elapsed_us,
			outcome, retry_disposition, response_committed
		) VALUES (
			'rec-failure', 'req-failed-selection', 1, 1, 'selection_failure',
			'alias', 'base', 'upstream', NULL, NULL, 0,
			100, 200, 50, 200,
			'no_account_available', 'final', 0
		)
	`); err != nil {
		t.Fatalf("insert selection_failure attempt_log: %v", err)
	}

	assertDisjoint := func(t *testing.T, requestID, expectedTable string) {
		t.Helper()
		var inUnrouted, inAttemptLog int
		if err := s.Writer.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM unrouted_request WHERE logical_request_id = ?
		`, requestID).Scan(&inUnrouted); err != nil {
			t.Fatalf("count unrouted_request for %s: %v", requestID, err)
		}
		if err := s.Writer.QueryRowContext(ctx, `
			SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = ?
		`, requestID).Scan(&inAttemptLog); err != nil {
			t.Fatalf("count attempt_log for %s: %v", requestID, err)
		}
		switch expectedTable {
		case "unrouted_request":
			if inUnrouted != 1 || inAttemptLog != 0 {
				t.Fatalf("%s: unrouted=%d, attempt_log=%d; want 1,0", requestID, inUnrouted, inAttemptLog)
			}
		case "attempt_log":
			if inUnrouted != 0 || inAttemptLog != 1 {
				t.Fatalf("%s: unrouted=%d, attempt_log=%d; want 0,1", requestID, inUnrouted, inAttemptLog)
			}
		default:
			t.Fatalf("unexpected expected table %q", expectedTable)
		}
	}

	assertDisjoint(t, "req-unrouted", "unrouted_request")
	assertDisjoint(t, "req-dispatched", "attempt_log")
	assertDisjoint(t, "req-failed-selection", "attempt_log")
}

func TestUnroutedRequestStoreFailureLeavesPriorRowUnchanged(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec-original', 'req-shared', 1000, 2000,
			'session-a', 400, 'invalid_request'
		)
	`); err != nil {
		t.Fatalf("insert original unrouted_request: %v", err)
	}

	if _, err := s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec-conflict', 'req-shared', 3000, 4000,
			'session-b', 500, 'internal_error'
		)
	`); err == nil {
		t.Fatal("expected duplicate logical_request_id insert to fail, got nil")
	}

	var status int
	var code string
	if err := s.Writer.QueryRowContext(ctx, `
		SELECT downstream_status, local_error_code
		FROM unrouted_request
		WHERE logical_request_id = 'req-shared'
	`).Scan(&status, &code); err != nil {
		t.Fatalf("query original row: %v", err)
	}
	if status != 400 {
		t.Errorf("downstream_status = %d, want 400", status)
	}
	if code != "invalid_request" {
		t.Errorf("local_error_code = %q, want invalid_request", code)
	}
}

func TestUnroutedRequestSchemaConstraints(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// 1. Successful insert with valid local error code.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec_1', 'req_1', 1000, 2000,
			'sess_1', 400, 'invalid_request'
		)
	`)
	if err != nil {
		t.Errorf("expected successful insert, got: %v", err)
	}

	// 2. Reject unknown error code.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec_2', 'req_2', 1000, 2000,
			'sess_1', 400, 'made_up_error'
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for unknown local_error_code, got nil")
	}

	// 3. Reject duplicate logical_request_id.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, logical_request_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec_3', 'req_1', 1000, 2000,
			'sess_1', 400, 'invalid_request'
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for duplicate logical_request_id, got nil")
	}

	// 4. Reject null logical_request_id.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO unrouted_request (
			record_id, started_at_us, finished_at_us,
			session_key, downstream_status, local_error_code
		) VALUES (
			'rec_4', 1000, 2000,
			'sess_1', 400, 'invalid_request'
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for missing logical_request_id, got nil")
	}
}
