package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestAttemptLogSchemaAllRecordKinds(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	insertAdmission := func(attemptID, requestID string, attemptNo int, account string) error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO dispatch_admission (
				attempt_id, logical_request_id, attempt_no, account_label,
				requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
			) VALUES (?, ?, ?, ?, 'alias', 'upstream', 100, 0, 0)
		`, attemptID, requestID, attemptNo, account)
		return err
	}
	if err := insertAdmission("attempt-1", "request-1", 1, "k1"); err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}

	insertDispatch := func() error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
				account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
				selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
				outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
				retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
				prompt_tokens, completion_tokens, total_tokens, usage_observation,
				limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
				dropped_header_count
			) VALUES (
				'rec-dispatch', 'request-1', 'attempt-1', 1, 1, 'dispatch',
				'alias', 'base', 'upstream', 'session', 'k1',
				'k1', 1, 0, NULL, 1000, 2000,
				100, 900, 2000, 150,
				'succeeded', 200, NULL, 'final', NULL,
				NULL, NULL, 1, 1,
				10, 20, 30, 'complete',
				NULL, NULL, NULL, NULL,
				0
			)
		`)
		return err
	}
	if err := insertDispatch(); err != nil {
		t.Fatalf("insert dispatch row: %v", err)
	}

	insertSkip := func(recordID string, sequenceNo, selectionNo int) error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
				account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
				selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
				outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
				retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
				prompt_tokens, completion_tokens, total_tokens, usage_observation,
				limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
				dropped_header_count
			) VALUES (
				?, 'request-1', NULL, ?, ?, 'selection_skip',
				'alias', 'base', 'upstream', 'session', 'k1',
				'k2', NULL, 0, NULL, ?, ?,
				NULL, NULL, ?, NULL,
				'selection_skipped', NULL, 'local_capacity', 'not_applicable', NULL,
				NULL, NULL, 0, 1,
				NULL, NULL, NULL, NULL,
				5, 3, 'rpm_limit', 1,
				NULL
			)
		`, recordID, sequenceNo, selectionNo, sequenceNo*100, sequenceNo*100+50, sequenceNo*100+50)
		return err
	}
	if err := insertSkip("rec-skip-1", 2, 1); err != nil {
		t.Fatalf("insert skip row 1: %v", err)
	}
	if err := insertSkip("rec-skip-2", 3, 2); err != nil {
		t.Fatalf("insert skip row 2: %v", err)
	}

	insertFailure := func() error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, session_key, pin_account_at_start,
				account_label, attempt_no, is_spill, spill_from_account, event_at_us, finished_at_us,
				selection_wait_us, attempt_duration_us, logical_elapsed_us, time_to_first_event_us,
				outcome, upstream_status_code, error_class, retry_disposition, retry_delay_ms,
				retry_after_s, upstream_retry_after_s, response_committed, request_streaming,
				prompt_tokens, completion_tokens, total_tokens, usage_observation,
				limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
				dropped_header_count
			) VALUES (
				'rec-failure', 'request-1', NULL, 4, 3, 'selection_failure',
				'alias', 'base', 'upstream', 'session', 'k1',
				NULL, NULL, 0, NULL, 400, 450,
				50, NULL, 450, NULL,
				'no_account_available', NULL, NULL, 'final', NULL,
				NULL, NULL, 0, 0,
				NULL, NULL, NULL, NULL,
				NULL, NULL, NULL, NULL,
				NULL
			)
		`)
		return err
	}
	if err := insertFailure(); err != nil {
		t.Fatalf("insert selection_failure row: %v", err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count attempt_log rows: %v", err)
	}
	if count != 4 {
		t.Fatalf("attempt_log count = %d, want 4", count)
	}

	var dispatchCount int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE record_kind = 'dispatch'`).Scan(&dispatchCount); err != nil {
		t.Fatalf("count dispatch rows: %v", err)
	}
	if dispatchCount != 1 {
		t.Fatalf("dispatch count = %d, want 1", dispatchCount)
	}
}

func TestAttemptLogSchemaSelectionAndAttemptNumbering(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES ('attempt-1', 'request-1', 1, 'k1', 'alias', 'upstream', 100, 0, 0)
	`)
	if err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'complete'
		)
	`)
	if err != nil {
		t.Fatalf("insert dispatch row: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-2', 'request-1', NULL, 1, 2, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', NULL, 0,
			300, 350, 350, 'selection_skipped', 'not_applicable',
			0, 1, 0, 'rpm_limit', 1
		)
	`)
	if err == nil {
		t.Fatal("expected duplicate sequence_no to be rejected")
	}
}

func TestAttemptLogSchemaAggregateSkipCounts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	insertSkip := func(recordID string, sequenceNo int, account string, reason string, count int) error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
				response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
			) VALUES (
				?, 'request-1', ?, 1, 'selection_skip',
				'alias', 'base', 'upstream', ?, 0,
				?, ?, ?, 'selection_skipped', 'not_applicable',
				0, 0, 0, ?, ?
			)
		`, recordID, sequenceNo, account, sequenceNo*100, sequenceNo*100+50, sequenceNo*100+50, reason, count)
		return err
	}

	for _, test := range []struct {
		recordID string
		sequence int
		account  string
		reason   string
		count    int
	}{
		{"rec-skip-1", 1, "k1", "rpm_limit", 3},
		{"rec-skip-2", 2, "k2", "in_flight_limit", 1},
		{"rec-skip-3", 3, "k1", "rpm_limit", 5},
	} {
		if err := insertSkip(test.recordID, test.sequence, test.account, test.reason, test.count); err != nil {
			t.Fatalf("insert %s: %v", test.recordID, err)
		}
	}

	var total int
	if err := s.Writer.QueryRowContext(ctx, `
		SELECT SUM(skip_observation_count) FROM attempt_log
		WHERE record_kind = 'selection_skip' AND account_label = 'k1'
	`).Scan(&total); err != nil {
		t.Fatalf("sum skip observations: %v", err)
	}
	if total != 8 {
		t.Fatalf("k1 skip observation total = %d, want 8", total)
	}
}

func TestAttemptLogSchemaTokenCounts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES ('attempt-1', 'request-1', 1, 'k1', 'alias', 'upstream', 100, 0, 0)
	`)
	if err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation,
			prompt_tokens, completion_tokens, total_tokens
		) VALUES (
			'rec-tokens', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'complete',
			10, 20, 30
		)
	`)
	if err != nil {
		t.Fatalf("insert dispatch with tokens: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-no-tokens', 'request-1', 2, 1, 'selection_skip',
			'alias', 'base', 'upstream', 'k1', 0,
			300, 350, 350, 'selection_skipped', 'not_applicable',
			0, 0, 0, 'rpm_limit', 1
		)
	`)
	if err != nil {
		t.Fatalf("insert skip row: %v", err)
	}

	var dispatchTotal, skipTotal *int64
	if err := s.Writer.QueryRowContext(ctx, `
		SELECT total_tokens FROM attempt_log WHERE record_id = 'rec-tokens'
	`).Scan(&dispatchTotal); err != nil {
		t.Fatalf("read dispatch total_tokens: %v", err)
	}
	if dispatchTotal == nil || *dispatchTotal != 30 {
		t.Fatalf("dispatch total_tokens = %v, want 30", dispatchTotal)
	}

	if err := s.Writer.QueryRowContext(ctx, `
		SELECT total_tokens FROM attempt_log WHERE record_id = 'rec-no-tokens'
	`).Scan(&skipTotal); err != nil {
		t.Fatalf("read skip total_tokens: %v", err)
	}
	if skipTotal != nil {
		t.Fatalf("skip total_tokens = %v, want null", skipTotal)
	}
}

func TestAttemptLogSchemaRejectsImpossibleRows(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES ('attempt-1', 'request-1', 1, 'k1', 'alias', 'upstream', 100, 0, 0)
	`)
	if err != nil {
		t.Fatalf("insert dispatch_admission: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-valid', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'complete'
		)
	`)
	if err != nil {
		t.Fatalf("valid dispatch insert: %v", err)
	}

	for _, test := range []struct {
		name  string
		query string
	}{
		{
			name: "invalid record kind",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed
				) VALUES (
					'rec-bad-kind', 'request-2', 1, 1, 'unknown',
					'alias', 'base', 'upstream', 'k1', 0,
					100, 200, 200, 'succeeded', 'final',
					1
				)
			`,
		},
		{
			name: "dispatch without attempt_id",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-dispatch-no-attempt', 'request-3', NULL, 1, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "dispatch without attempt_no",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-dispatch-no-attempt-no', 'request-4', 'attempt-1', 1, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', NULL, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "dispatch without account",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-dispatch-no-account', 'request-5', 'attempt-1', 1, 1, 'dispatch',
					'alias', 'base', 'upstream', NULL, 1, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "skip with attempt_id",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
				) VALUES (
					'rec-skip-with-attempt', 'request-6', 'attempt-1', 1, 1, 'selection_skip',
					'alias', 'base', 'upstream', 'k1', NULL, 0,
					100, 200, 200, 'selection_skipped', 'not_applicable',
					0, 0, 0, 'rpm_limit', 1
				)
			`,
		},
		{
			name: "skip without skip_reason",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
				) VALUES (
					'rec-skip-no-reason', 'request-7', NULL, 1, 1, 'selection_skip',
					'alias', 'base', 'upstream', 'k1', NULL, 0,
					100, 200, 200, 'selection_skipped', 'not_applicable',
					0, 0, 0, NULL, 1
				)
			`,
		},
		{
			name: "failure with account",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed
				) VALUES (
					'rec-failure-with-account', 'request-8', NULL, 1, 1, 'selection_failure',
					'alias', 'base', 'upstream', 'k1', NULL, 0,
					100, 200, 200, 'no_account_available', 'final',
					0
				)
			`,
		},
		{
			name: "unknown account label",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-unknown-account', 'request-9', 'attempt-1', 1, 1, 'dispatch',
					'alias', 'base', 'upstream', 'unknown', 1, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "zero sequence",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-zero-seq', 'request-10', 'attempt-1', 0, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "zero selection",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-zero-selection', 'request-11', 'attempt-1', 1, 0, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "negative logical_elapsed_us",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed
				) VALUES (
					'rec-negative-elapsed', 'request-12', NULL, 1, 1, 'selection_failure',
					'alias', 'base', 'upstream', NULL, NULL, 0,
					100, 200, -1, 'no_account_available', 'final',
					0
				)
			`,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			if _, err := s.Writer.ExecContext(ctx, test.query); err == nil {
				t.Fatal("expected constraint failure, got nil")
			}
		})
	}
}

func TestAttemptLogSchemaHasNoContentOrCostColumns(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	rows, err := s.Writer.QueryContext(ctx, `PRAGMA table_info(attempt_log)`)
	if err != nil {
		t.Fatalf("PRAGMA table_info: %v", err)
	}
	defer func() { _ = rows.Close() }()

	columns := make(map[string]struct{})
	for rows.Next() {
		var cid int
		var name, typ string
		var notNull, pk int
		var dfltValue any
		if err := rows.Scan(&cid, &name, &typ, &notNull, &dfltValue, &pk); err != nil {
			t.Fatalf("scan table_info row: %v", err)
		}
		columns[name] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate table_info: %v", err)
	}

	for _, forbidden := range []string{
		"request_body",
		"response_body",
		"messages",
		"prompt",
		"completion",
		"cost",
		"price",
		"currency",
		"upstream_id",
		"session_header",
		"credential",
		"api_key",
	} {
		if _, ok := columns[forbidden]; ok {
			t.Fatalf("attempt_log contains forbidden column %q", forbidden)
		}
	}

	required := []string{
		"record_id",
		"logical_request_id",
		"attempt_id",
		"sequence_no",
		"selection_no",
		"record_kind",
		"requested_alias",
		"base_alias",
		"upstream_model",
		"session_key",
		"pin_account_at_start",
		"account_label",
		"attempt_no",
		"is_spill",
		"spill_from_account",
		"event_at_us",
		"finished_at_us",
		"selection_wait_us",
		"attempt_duration_us",
		"logical_elapsed_us",
		"time_to_first_event_us",
		"outcome",
		"upstream_status_code",
		"error_class",
		"retry_disposition",
		"retry_delay_ms",
		"retry_after_s",
		"upstream_retry_after_s",
		"response_committed",
		"request_streaming",
		"prompt_tokens",
		"completion_tokens",
		"total_tokens",
		"usage_observation",
		"limiter_rpm_used",
		"limiter_in_flight",
		"skip_reason",
		"skip_observation_count",
		"dropped_header_count",
	}
	for _, column := range required {
		if _, ok := columns[column]; !ok {
			t.Fatalf("attempt_log missing required column %q", column)
		}
	}
}
