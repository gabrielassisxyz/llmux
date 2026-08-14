package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestAttemptLogSchemaMissingConstraints(t *testing.T) {
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

	insertValidDispatch := func(recordID, attemptID string, sequenceNo int) error {
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
				event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
				time_to_first_event_us, outcome, upstream_status_code, error_class, retry_disposition,
				response_committed, request_streaming, prompt_tokens, completion_tokens, total_tokens,
				usage_observation, dropped_header_count
			) VALUES (
				?, 'request-1', ?, ?, 1, 'dispatch',
				'alias', 'base', 'upstream', 'k1', 1, 0,
				100, 200, 50, 100, 200,
				150, 'succeeded', 200, NULL, 'final',
				1, 1, 10, 20, 30,
				'complete', 0
			)
		`, recordID, attemptID, sequenceNo)
		return err
	}
	if err := insertValidDispatch("rec-valid", "attempt-1", 1); err != nil {
		t.Fatalf("valid dispatch insert: %v", err)
	}

	for _, test := range []struct {
		name  string
		query string
	}{
		{
			name: "dispatch without selection_wait_us",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed, usage_observation
				) VALUES (
					'rec-no-wait', 'request-1', 'attempt-1', 2, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, NULL, 100, 200,
					'succeeded', 'final', 1, 'complete'
				)
			`,
		},
		{
			name: "selection_failure without selection_wait_us",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed
				) VALUES (
					'rec-failure-no-wait', 'request-1', 3, 2, 'selection_failure',
					'alias', 'base', 'upstream', NULL, NULL, 0,
					100, 200, NULL, 200,
					'no_account_available', 'final', 0
				)
			`,
		},
		{
			name: "retry_after_s on non-capacity outcome",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, retry_after_s, response_committed, usage_observation
				) VALUES (
					'rec-retry-wrong-outcome', 'request-1', 'attempt-1', 4, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'succeeded', 'final', 5, 1, 'complete'
				)
			`,
		},
		{
			name: "negative retry_after_s",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, retry_after_s, response_committed, usage_observation
				) VALUES (
					'rec-retry-negative', 'request-1', 'attempt-1', 5, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'capacity_timeout', 'final', -1, 1, 'complete'
				)
			`,
		},
		{
			name: "upstream_retry_after_s on non-retry status",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, upstream_status_code, retry_disposition, upstream_retry_after_s,
					response_committed, usage_observation
				) VALUES (
					'rec-upstream-retry-wrong-status', 'request-1', 'attempt-1', 6, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'upstream_http_error', 400, 'final', 10,
					1, 'complete'
				)
			`,
		},
		{
			name: "upstream_retry_after_s on selection_skip",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
					upstream_status_code, upstream_retry_after_s, response_committed,
					limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
				) VALUES (
					'rec-upstream-retry-skip', 'request-1', 7, 1, 'selection_skip',
					'alias', 'base', 'upstream', 'k2', NULL, 0,
					100, 200, 200, 'selection_skipped', 'not_applicable',
					429, 10, 0,
					0, 0, 'rpm_limit', 1
				)
			`,
		},
		{
			name: "negative upstream_retry_after_s",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, upstream_status_code, retry_disposition, upstream_retry_after_s,
					response_committed, usage_observation
				) VALUES (
					'rec-upstream-retry-negative', 'request-1', 'attempt-1', 8, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'upstream_http_error', 429, 'final', -5,
					1, 'complete'
				)
			`,
		},
		{
			name: "negative prompt_tokens",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed, usage_observation,
					prompt_tokens, completion_tokens, total_tokens
				) VALUES (
					'rec-negative-prompt', 'request-1', 'attempt-1', 9, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'succeeded', 'final', 1, 'complete',
					-1, 20, 30
				)
			`,
		},
		{
			name: "negative completion_tokens",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed, usage_observation,
					prompt_tokens, completion_tokens, total_tokens
				) VALUES (
					'rec-negative-completion', 'request-1', 'attempt-1', 10, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'succeeded', 'final', 1, 'complete',
					10, -20, 30
				)
			`,
		},
		{
			name: "negative total_tokens",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed, usage_observation,
					prompt_tokens, completion_tokens, total_tokens
				) VALUES (
					'rec-negative-total', 'request-1', 'attempt-1', 11, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'succeeded', 'final', 1, 'complete',
					10, 20, -30
				)
			`,
		},
		{
			name: "spill without source",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					spill_from_account, event_at_us, finished_at_us, selection_wait_us,
					attempt_duration_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-spill-no-source', 'request-1', 'attempt-1', 12, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 1,
					NULL, 100, 200, 50,
					100, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "spill with invalid source",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					spill_from_account, event_at_us, finished_at_us, selection_wait_us,
					attempt_duration_us, logical_elapsed_us, outcome, retry_disposition,
					response_committed, usage_observation
				) VALUES (
					'rec-spill-bad-source', 'request-1', 'attempt-1', 13, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 1,
					'unknown', 100, 200, 50,
					100, 200, 'succeeded', 'final',
					1, 'complete'
				)
			`,
		},
		{
			name: "orphan attempt_id",
			query: `
				INSERT INTO attempt_log (
					record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
					requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
					event_at_us, finished_at_us, selection_wait_us, attempt_duration_us, logical_elapsed_us,
					outcome, retry_disposition, response_committed, usage_observation
				) VALUES (
					'rec-orphan', 'request-1', 'no-such-attempt', 14, 1, 'dispatch',
					'alias', 'base', 'upstream', 'k1', 1, 0,
					100, 200, 50, 100, 200,
					'succeeded', 'final', 1, 'complete'
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
