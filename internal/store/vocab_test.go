package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestVocabConstantsAreValid(t *testing.T) {
	for _, v := range store.AllOutcomes() {
		if !v.Valid() {
			t.Errorf("Outcome(%q).Valid() = false, want true", v)
		}
	}
	for _, v := range store.AllErrorClasses() {
		if !v.Valid() {
			t.Errorf("ErrorClass(%q).Valid() = false, want true", v)
		}
	}
	for _, v := range store.AllSkipReasons() {
		if !v.Valid() {
			t.Errorf("SkipReason(%q).Valid() = false, want true", v)
		}
	}
	for _, v := range store.AllRetryDispositions() {
		if !v.Valid() {
			t.Errorf("RetryDisposition(%q).Valid() = false, want true", v)
		}
	}
	for _, v := range store.AllUsageObservations() {
		if !v.Valid() {
			t.Errorf("UsageObservation(%q).Valid() = false, want true", v)
		}
	}
}

func TestVocabZeroValuesAreInvalid(t *testing.T) {
	if (store.Outcome("")).Valid() {
		t.Error("empty Outcome should be invalid")
	}
	if (store.ErrorClass("")).Valid() {
		t.Error("empty ErrorClass should be invalid")
	}
	if (store.SkipReason("")).Valid() {
		t.Error("empty SkipReason should be invalid")
	}
	if (store.RetryDisposition("")).Valid() {
		t.Error("empty RetryDisposition should be invalid")
	}
	if (store.UsageObservation("")).Valid() {
		t.Error("empty UsageObservation should be invalid")
	}
}

func TestVocabUnknownValuesAreInvalid(t *testing.T) {
	if (store.Outcome("unknown")).Valid() {
		t.Error("unknown Outcome should be invalid")
	}
	if (store.ErrorClass("unknown")).Valid() {
		t.Error("unknown ErrorClass should be invalid")
	}
	if (store.SkipReason("unknown")).Valid() {
		t.Error("unknown SkipReason should be invalid")
	}
	if (store.RetryDisposition("unknown")).Valid() {
		t.Error("unknown RetryDisposition should be invalid")
	}
	if (store.UsageObservation("unknown")).Valid() {
		t.Error("unknown UsageObservation should be invalid")
	}
}

func TestAttemptLogRejectsInvalidOutcome(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'unknown_outcome', 'final',
			1, 'complete'
		)
	`)
	if err == nil {
		t.Fatal("expected invalid outcome to be rejected")
	}
}

func TestAttemptLogRejectsInvalidErrorClass(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, error_class, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'upstream_http_error', 'unknown_class', 'final',
			1, 'complete'
		)
	`)
	if err == nil {
		t.Fatal("expected invalid error_class to be rejected")
	}
}

func TestAttemptLogRejectsInvalidRetryDisposition(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'unknown_disposition',
			1, 'complete'
		)
	`)
	if err == nil {
		t.Fatal("expected invalid retry_disposition to be rejected")
	}
}

func TestAttemptLogRejectsInvalidUsageObservation(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'unknown_observation'
		)
	`)
	if err == nil {
		t.Fatal("expected invalid usage_observation to be rejected")
	}
}

func TestAttemptLogRejectsInvalidSkipReason(t *testing.T) {
	s := openTestStore(t)

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-1', 'request-1', 1, 1, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', 0,
			100, 200, 200, 'selection_skipped', 'not_applicable',
			0, 0, 0, 'unknown_reason', 1
		)
	`)
	if err == nil {
		t.Fatal("expected invalid skip_reason to be rejected")
	}
}

func TestAttemptLogAcceptsEveryOutcome(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	dispatchOutcomes := []store.Outcome{
		store.OutcomeSucceeded,
		store.OutcomeUpstreamHTTPError,
		store.OutcomeTransportError,
		store.OutcomeDeadlineExceeded,
		store.OutcomeClientCanceled,
		store.OutcomeResponseReadError,
		store.OutcomeResponseWriteError,
		store.OutcomeCapacityTimeout,
		store.OutcomeInternalError,
	}
	for i, outcome := range dispatchOutcomes {
		recordID := fmt.Sprintf("rec-dispatch-%d", i)
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
				response_committed, usage_observation
			) VALUES (
				?, 'request-1', 'attempt-1', ?, 1, 'dispatch',
				'alias', 'base', 'upstream', 'k1', 1, 0,
				100, 200, 200, ?, 'final',
				1, 'complete'
			)
		`, recordID, i+1, string(outcome))
		if err != nil {
			t.Fatalf("insert dispatch outcome %q: %v", outcome, err)
		}
	}

	_, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-skip', 'request-1', ?, 2, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', 0,
			300, 350, 350, ?, 'not_applicable',
			0, 0, 0, 'rpm_limit', 1
		)
	`, len(dispatchOutcomes)+1, string(store.OutcomeSelectionSkipped))
	if err != nil {
		t.Fatalf("insert skip outcome %q: %v", store.OutcomeSelectionSkipped, err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed
		) VALUES (
			'rec-failure', 'request-1', ?, 3, 'selection_failure',
			'alias', 'base', 'upstream', NULL, NULL, 0,
			400, 450, 450, ?, 'final',
			0
		)
	`, len(dispatchOutcomes)+2, string(store.OutcomeNoAccountAvailable))
	if err != nil {
		t.Fatalf("insert failure outcome %q: %v", store.OutcomeNoAccountAvailable, err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	want := len(dispatchOutcomes) + 2
	if count != want {
		t.Fatalf("attempt_log count = %d, want %d", count, want)
	}
}

func TestAttemptLogAcceptsEveryErrorClass(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i, class := range store.AllErrorClasses() {
		attemptID := fmt.Sprintf("attempt-%d", i)
		recordID := fmt.Sprintf("rec-%d", i)
		insertDispatchAdmission(t, s, attemptID, "request-1", i+1, "k1")
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, error_class, retry_disposition,
				response_committed, usage_observation
			) VALUES (
				?, 'request-1', ?, ?, 1, 'dispatch',
				'alias', 'base', 'upstream', 'k1', ?, 0,
				100, 200, 200, 'upstream_http_error', ?, 'final',
				1, 'complete'
			)
		`, recordID, attemptID, i+1, i+1, string(class))
		if err != nil {
			t.Fatalf("insert error_class %q: %v", class, err)
		}
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != len(store.AllErrorClasses()) {
		t.Fatalf("attempt_log count = %d, want %d", count, len(store.AllErrorClasses()))
	}
}

func TestAttemptLogAcceptsEverySkipReason(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i, reason := range store.AllSkipReasons() {
		recordID := fmt.Sprintf("rec-%d", i)
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
				response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
			) VALUES (
				?, 'request-1', ?, 1, 'selection_skip',
				'alias', 'base', 'upstream', 'k2', 0,
				?, ?, ?, 'selection_skipped', 'not_applicable',
				0, 0, 0, ?, 1
			)
		`, recordID, i+1, i*100, i*100+50, i*100+50, string(reason))
		if err != nil {
			t.Fatalf("insert skip_reason %q: %v", reason, err)
		}
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != len(store.AllSkipReasons()) {
		t.Fatalf("attempt_log count = %d, want %d", count, len(store.AllSkipReasons()))
	}
}

func TestAttemptLogAcceptsEveryRetryDisposition(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	nonApplicable := []store.RetryDisposition{
		store.RetryDispositionFinal,
		store.RetryDispositionRetrySameAccount,
		store.RetryDispositionRetryOtherAccount,
		store.RetryDispositionRetryNamedAccount,
		store.RetryDispositionSuppressedClassBudget,
		store.RetryDispositionSuppressedGlobalBudget,
		store.RetryDispositionSuppressedDeadline,
	}
	for i, disposition := range nonApplicable {
		attemptID := fmt.Sprintf("attempt-%d", i)
		recordID := fmt.Sprintf("rec-dispatch-%d", i)
		insertDispatchAdmission(t, s, attemptID, "request-1", i+1, "k1")
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
				response_committed, usage_observation
			) VALUES (
				?, 'request-1', ?, ?, 1, 'dispatch',
				'alias', 'base', 'upstream', 'k1', ?, 0,
				100, 200, 200, 'upstream_http_error', ?,
				1, 'complete'
			)
		`, recordID, attemptID, i+1, i+1, string(disposition))
		if err != nil {
			t.Fatalf("insert retry_disposition %q on dispatch: %v", disposition, err)
		}
	}

	_, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-skip', 'request-1', ?, 2, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', 0,
			300, 350, 350, 'selection_skipped', ?,
			0, 0, 0, 'rpm_limit', 1
		)
	`, len(nonApplicable)+1, string(store.RetryDispositionNotApplicable))
	if err != nil {
		t.Fatalf("insert retry_disposition %q on skip: %v", store.RetryDispositionNotApplicable, err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	want := len(nonApplicable) + 1
	if count != want {
		t.Fatalf("attempt_log count = %d, want %d", count, want)
	}
}

func TestAttemptLogAcceptsEveryUsageObservation(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	for i, observation := range store.AllUsageObservations() {
		attemptID := fmt.Sprintf("attempt-%d", i)
		recordID := fmt.Sprintf("rec-%d", i)
		insertDispatchAdmission(t, s, attemptID, "request-1", i+1, "k1")
		_, err := s.Writer.ExecContext(ctx, `
			INSERT INTO attempt_log (
				record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
				requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
				event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
				response_committed, usage_observation
			) VALUES (
				?, 'request-1', ?, ?, 1, 'dispatch',
				'alias', 'base', 'upstream', 'k1', ?, 0,
				100, 200, 200, 'succeeded', 'final',
				1, ?
			)
		`, recordID, attemptID, i+1, i+1, string(observation))
		if err != nil {
			t.Fatalf("insert usage_observation %q: %v", observation, err)
		}
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1'`).Scan(&count); err != nil {
		t.Fatalf("count rows: %v", err)
	}
	if count != len(store.AllUsageObservations()) {
		t.Fatalf("attempt_log count = %d, want %d", count, len(store.AllUsageObservations()))
	}
}

func TestAttemptLogUsageObservationNullOnNonDispatch(t *testing.T) {
	s := openTestStore(t)
	ctx := context.Background()

	_, err := s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count
		) VALUES (
			'rec-skip', 'request-1', 1, 1, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', 0,
			100, 200, 200, 'selection_skipped', 'not_applicable',
			0, 0, 0, 'rpm_limit', 1
		)
	`)
	if err != nil {
		t.Fatalf("insert skip row: %v", err)
	}

	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed
		) VALUES (
			'rec-failure', 'request-1', 2, 1, 'selection_failure',
			'alias', 'base', 'upstream', NULL, NULL, 0,
			300, 350, 350, 'no_account_available', 'final',
			0
		)
	`)
	if err != nil {
		t.Fatalf("insert failure row: %v", err)
	}

	var count int
	if err := s.Writer.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM attempt_log
		WHERE logical_request_id = 'request-1' AND usage_observation IS NULL
	`).Scan(&count); err != nil {
		t.Fatalf("count null usage_observation rows: %v", err)
	}
	if count != 2 {
		t.Fatalf("null usage_observation count = %d, want 2", count)
	}
}

func TestAttemptLogRejectsUsageObservationOnNonDispatch(t *testing.T) {
	s := openTestStore(t)

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, limiter_rpm_used, limiter_in_flight, skip_reason, skip_observation_count,
			usage_observation
		) VALUES (
			'rec-skip', 'request-1', 1, 1, 'selection_skip',
			'alias', 'base', 'upstream', 'k2', 0,
			100, 200, 200, 'selection_skipped', 'not_applicable',
			0, 0, 0, 'rpm_limit', 1, 'complete'
		)
	`)
	if err == nil {
		t.Fatal("expected usage_observation on selection_skip to be rejected")
	}
}

func TestAttemptLogRejectsRetryDispositionNotApplicableOnDispatch(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'not_applicable',
			1, 'complete'
		)
	`)
	if err == nil {
		t.Fatal("expected retry_disposition 'not_applicable' on dispatch to be rejected")
	}
}

func TestAttemptLogRejectsSkipReasonOnNonSkip(t *testing.T) {
	s := openTestStore(t)
	insertDispatchAdmission(t, s, "attempt-1", "request-1", 1, "k1")

	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, account_label, attempt_no, is_spill,
			event_at_us, finished_at_us, logical_elapsed_us, outcome, retry_disposition,
			response_committed, usage_observation, skip_reason
		) VALUES (
			'rec-1', 'request-1', 'attempt-1', 1, 1, 'dispatch',
			'alias', 'base', 'upstream', 'k1', 1, 0,
			100, 200, 200, 'succeeded', 'final',
			1, 'complete', 'rpm_limit'
		)
	`)
	if err == nil {
		t.Fatal("expected skip_reason on dispatch to be rejected")
	}
}

func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func insertDispatchAdmission(t *testing.T, s *store.Store, attemptID, requestID string, attemptNo int, account string) {
	t.Helper()
	_, err := s.Writer.ExecContext(context.Background(), `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES (?, ?, ?, ?, 'alias', 'upstream', 100, 0, 0)
	`, attemptID, requestID, attemptNo, account)
	if err != nil {
		t.Fatalf("insert dispatch_admission for %s: %v", attemptID, err)
	}
}
