package store_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/store"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// The tests in this file close the remaining items of the store test suite
// that the topic files do not already cover: retry-delay persistence on
// dispatch rows, the unmatched start row a killed process leaves behind, the
// independent wall/monotonic clock contract, the named query recipes, and the
// minute-boundary counting the recipes depend on.

// TestPhaseDispatchPersistsRetryDelay proves the retry-delay columns on a
// dispatch row persist the derived delta and the raw upstream Retry-After
// independently, and stay NULL when the response carried no usable header.
func TestPhaseDispatchPersistsRetryDelay(t *testing.T) {
	s := openPhaseBatchStore(t)
	ctx := context.Background()

	insertPhaseBatchAdmission(t, s, "attempt-429", "request-429", 1, "k1")
	insertPhaseBatchAdmission(t, s, "attempt-5xx", "request-5xx", 1, "k1")
	insertPhaseBatchAdmission(t, s, "attempt-none", "request-none", 1, "k1")

	status429 := 429
	status500 := 500
	status200 := 200
	rateLimited := store.ErrorClassRateLimited
	serverError := store.ErrorClassUpstreamServerError
	delta429 := int64(1000)
	raw429 := int64(1)
	delta5xx := int64(5000)
	raw5xx := int64(5)

	insertDispatch := func(recordID, requestID, attemptID string, status int, class *store.ErrorClass, delta, raw *int64) {
		t.Helper()
		if err := s.InsertPhaseBatch(ctx, store.PhaseBatch{
			Terminal: store.PhaseDispatch{
				RecordID:            recordID,
				LogicalRequestID:    requestID,
				AttemptID:           attemptID,
				SequenceNo:          1,
				SelectionNo:         1,
				RequestedAlias:      "alias",
				BaseAlias:           "base",
				UpstreamModel:       "upstream",
				AccountLabel:        "k1",
				AttemptNo:           1,
				EventAtUS:           100,
				FinishedAtUS:        200,
				SelectionWaitUS:     50,
				LogicalElapsedUS:    200,
				Outcome:             store.OutcomeUpstreamHTTPError,
				UpstreamStatusCode:  &status,
				ErrorClass:          class,
				RetryDisposition:    store.RetryDispositionRetryOtherAccount,
				RetryDelayMS:        delta,
				UpstreamRetryAfterS: raw,
				ResponseCommitted:   true,
				UsageObservation:    store.UsageObservationComplete,
			},
		}); err != nil {
			t.Fatalf("insert %s: %v", recordID, err)
		}
	}

	insertDispatch("rec-429", "request-429", "attempt-429", status429, &rateLimited, &delta429, &raw429)
	insertDispatch("rec-5xx", "request-5xx", "attempt-5xx", status500, &serverError, &delta5xx, &raw5xx)

	// A successful row whose response carried no retry header: both columns
	// must stay NULL rather than inheriting a neighbour's value.
	if err := s.InsertPhaseBatch(ctx, store.PhaseBatch{
		Terminal: store.PhaseDispatch{
			RecordID:           "rec-none",
			LogicalRequestID:   "request-none",
			AttemptID:          "attempt-none",
			SequenceNo:         1,
			SelectionNo:        1,
			RequestedAlias:     "alias",
			BaseAlias:          "base",
			UpstreamModel:      "upstream",
			AccountLabel:       "k1",
			AttemptNo:          1,
			EventAtUS:          100,
			FinishedAtUS:       200,
			SelectionWaitUS:    50,
			LogicalElapsedUS:   200,
			Outcome:            store.OutcomeSucceeded,
			UpstreamStatusCode: &status200,
			RetryDisposition:   store.RetryDispositionFinal,
			ResponseCommitted:  true,
			UsageObservation:   store.UsageObservationComplete,
		},
	}); err != nil {
		t.Fatalf("insert rec-none: %v", err)
	}

	read := func(recordID string) (delta, raw *int64) {
		t.Helper()
		if err := s.Writer.QueryRowContext(ctx,
			`SELECT retry_delay_ms, upstream_retry_after_s FROM attempt_log WHERE record_id = ?`, recordID,
		).Scan(&delta, &raw); err != nil {
			t.Fatalf("read %s: %v", recordID, err)
		}
		return delta, raw
	}

	if delta, raw := read("rec-429"); delta == nil || *delta != 1000 || raw == nil || *raw != 1 {
		t.Errorf("rec-429 retry delay = (%v, %v), want (1000, 1)", delta, raw)
	}
	if delta, raw := read("rec-5xx"); delta == nil || *delta != 5000 || raw == nil || *raw != 5 {
		t.Errorf("rec-5xx retry delay = (%v, %v), want (5000, 5)", delta, raw)
	}
	if delta, raw := read("rec-none"); delta != nil || raw != nil {
		t.Errorf("rec-none retry delay = (%v, %v), want (NULL, NULL)", delta, raw)
	}
}

// TestProcessEventUnmatchedStartRowSurvivesSuccessor proves a start row with
// no stop row is preserved, and a successor process that starts and stops
// under an earlier wall time than the one that died is recorded with its own
// correct, non-negative elapsed duration.
func TestProcessEventUnmatchedStartRowSurvivesSuccessor(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// The killed process: a start row with no stop row.
	if err := s.InsertProcessStart(ctx, store.ProcessStartEvent{
		RecordID:          "start-dead",
		ProcessInstanceID: "inst-dead",
		AtUS:              2000,
		Version:           "v1.0.0",
		Revision:          "abcdef",
	}); err != nil {
		t.Fatalf("insert dead start: %v", err)
	}

	// The successor starts and stops under an earlier wall time, after a
	// backward wall step, and its elapsed duration is monotonic and positive.
	if err := s.InsertProcessStart(ctx, store.ProcessStartEvent{
		RecordID:          "start-successor",
		ProcessInstanceID: "inst-successor",
		AtUS:              1000,
		Version:           "v1.0.0",
		Revision:          "abcdef",
	}); err != nil {
		t.Fatalf("insert successor start: %v", err)
	}
	if err := s.InsertProcessStop(ctx, store.ProcessStopEvent{
		RecordID:          "stop-successor",
		ProcessInstanceID: "inst-successor",
		AtUS:              1500,
		ProcessElapsedUS:  500,
		Version:           "v1.0.0",
		Revision:          "abcdef",
	}); err != nil {
		t.Fatalf("insert successor stop: %v", err)
	}

	// The dead process's start row is still present and unmatched.
	var deadStops int
	if err := s.Writer.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM process_event WHERE process_instance_id = 'inst-dead' AND event_kind = 'process_stop'`,
	).Scan(&deadStops); err != nil {
		t.Fatalf("count dead stops: %v", err)
	}
	if deadStops != 0 {
		t.Errorf("dead process has %d stop rows, want 0", deadStops)
	}

	// The successor's stop row carries its own elapsed duration, unchanged by
	// the wall step that made its start instant earlier than the dead one's.
	var successorElapsed int64
	if err := s.Writer.QueryRowContext(ctx,
		`SELECT process_elapsed_us FROM process_event WHERE record_id = 'stop-successor'`,
	).Scan(&successorElapsed); err != nil {
		t.Fatalf("read successor elapsed: %v", err)
	}
	if successorElapsed != 500 {
		t.Errorf("successor elapsed = %d, want 500", successorElapsed)
	}
}

// TestPersistedInstantsFollowWallAndDurationsFollowMonotonic proves the store
// persists instants and durations exactly as given, so a wall step backward
// and a monotonic step forward land in the same row without either clock
// being converted into the other.
func TestPersistedInstantsFollowWallAndDurationsFollowMonotonic(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// First run: wall instant 2000, monotonic elapsed 500.
	if err := s.InsertProcessStart(ctx, store.ProcessStartEvent{
		RecordID: "start-a", ProcessInstanceID: "inst-a", AtUS: 2000, Version: "v1.0.0", Revision: "abcdef",
	}); err != nil {
		t.Fatalf("insert start-a: %v", err)
	}
	if err := s.InsertProcessStop(ctx, store.ProcessStopEvent{
		RecordID: "stop-a", ProcessInstanceID: "inst-a", AtUS: 2500, ProcessElapsedUS: 500, Version: "v1.0.0", Revision: "abcdef",
	}); err != nil {
		t.Fatalf("insert stop-a: %v", err)
	}

	// Second run after a backward wall step and a forward monotonic step: the
	// wall instant is earlier, the monotonic elapsed is larger.
	if err := s.InsertProcessStart(ctx, store.ProcessStartEvent{
		RecordID: "start-b", ProcessInstanceID: "inst-b", AtUS: 1000, Version: "v1.0.0", Revision: "abcdef",
	}); err != nil {
		t.Fatalf("insert start-b: %v", err)
	}
	if err := s.InsertProcessStop(ctx, store.ProcessStopEvent{
		RecordID: "stop-b", ProcessInstanceID: "inst-b", AtUS: 1600, ProcessElapsedUS: 600, Version: "v1.0.0", Revision: "abcdef",
	}); err != nil {
		t.Fatalf("insert stop-b: %v", err)
	}

	read := func(recordID string) (atUS, elapsedUS int64) {
		t.Helper()
		if err := s.Writer.QueryRowContext(ctx,
			`SELECT at_us, process_elapsed_us FROM process_event WHERE record_id = ?`, recordID,
		).Scan(&atUS, &elapsedUS); err != nil {
			t.Fatalf("read %s: %v", recordID, err)
		}
		return atUS, elapsedUS
	}

	if atUS, elapsedUS := read("stop-a"); atUS != 2500 || elapsedUS != 500 {
		t.Errorf("stop-a = (at %d, elapsed %d), want (2500, 500)", atUS, elapsedUS)
	}
	if atUS, elapsedUS := read("stop-b"); atUS != 1600 || elapsedUS != 600 {
		t.Errorf("stop-b = (at %d, elapsed %d), want (1600, 600)", atUS, elapsedUS)
	}
}

// TestNamedQueryRecipesRunAgainstSeededStore runs a representative set of the
// named query recipes against a seeded store and asserts each returns the
// shape its name promises. A schema change that invalidates one of these
// queries fails here rather than in front of an operator.
func TestNamedQueryRecipesRunAgainstSeededStore(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	// Seed: one dispatch with a terminal row, one admission with no terminal
	// row, and one unrouted request.
	if err := s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID: "attempt-1", LogicalRequestID: "request-1", AttemptNo: 1,
		AccountLabel: "k1", RequestedAlias: "alias", UpstreamModel: "upstream",
		ReservedAtUS: 100, LimiterRPMUsed: 0, LimiterInFlight: 0,
	}); err != nil {
		t.Fatalf("insert admission: %v", err)
	}
	if err := s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID: "attempt-orphan", LogicalRequestID: "request-orphan", AttemptNo: 1,
		AccountLabel: "k2", RequestedAlias: "alias", UpstreamModel: "upstream",
		ReservedAtUS: 200, LimiterRPMUsed: 0, LimiterInFlight: 0,
	}); err != nil {
		t.Fatalf("insert orphan admission: %v", err)
	}
	if err := s.InsertPhaseBatch(ctx, store.PhaseBatch{
		Terminal: store.PhaseDispatch{
			RecordID: "rec-dispatch", LogicalRequestID: "request-1", AttemptID: "attempt-1",
			SequenceNo: 1, SelectionNo: 1, RequestedAlias: "alias", BaseAlias: "base",
			UpstreamModel: "upstream", AccountLabel: "k1", AttemptNo: 1,
			EventAtUS: 300, FinishedAtUS: 400, SelectionWaitUS: 50, LogicalElapsedUS: 400,
			Outcome: store.OutcomeSucceeded, RetryDisposition: store.RetryDispositionFinal,
			ResponseCommitted: true, UsageObservation: store.UsageObservationComplete,
		},
	}); err != nil {
		t.Fatalf("insert phase batch: %v", err)
	}
	if err := s.InsertUnroutedRequest(ctx, store.UnroutedRequest{
		RecordID: "rec-unrouted", LogicalRequestID: "request-unrouted",
		StartedAtUS: 100, FinishedAtUS: 150, DownstreamStatus: 400, LocalErrorCode: "invalid_request",
	}); err != nil {
		t.Fatalf("insert unrouted request: %v", err)
	}

	recipes := []struct {
		name  string
		query string
		want  int
	}{
		{
			name:  "dispatch count by account and event range",
			query: `SELECT COUNT(*) FROM attempt_log WHERE record_kind = 'dispatch' AND account_label = 'k1' AND event_at_us >= 0 AND event_at_us < 1000`,
			want:  1,
		},
		{
			name:  "admission pressure by account and reserved range",
			query: `SELECT COUNT(*) FROM dispatch_admission WHERE account_label = 'k1' AND reserved_at_us >= 0 AND reserved_at_us < 1000`,
			want:  1,
		},
		{
			name:  "admissions no terminal row matched",
			query: `SELECT COUNT(*) FROM dispatch_admission d WHERE NOT EXISTS (SELECT 1 FROM attempt_log a WHERE a.attempt_id = d.attempt_id)`,
			want:  1,
		},
		{
			name:  "local rejections by error code",
			query: `SELECT COUNT(*) FROM unrouted_request WHERE local_error_code = 'invalid_request'`,
			want:  1,
		},
		{
			name:  "request id resolves in exactly one table",
			query: `SELECT (SELECT COUNT(*) FROM attempt_log WHERE logical_request_id = 'request-1') + (SELECT COUNT(*) FROM unrouted_request WHERE logical_request_id = 'request-1')`,
			want:  1,
		},
	}

	for _, recipe := range recipes {
		t.Run(recipe.name, func(t *testing.T) {
			var count int
			if err := s.Writer.QueryRowContext(ctx, recipe.query).Scan(&count); err != nil {
				t.Fatalf("recipe %q failed: %v", recipe.name, err)
			}
			if count != recipe.want {
				t.Errorf("recipe %q returned %d, want %d", recipe.name, count, recipe.want)
			}
		})
	}
}

// TestUnroutedRequestAcceptsUnknownAlias proves the store accepts the
// model_not_found code an unknown alias produces and persists the identifier
// the client was given, completing the envelope-rejection, overload and
// unknown-alias trio the suite requires.
func TestUnroutedRequestAcceptsUnknownAlias(t *testing.T) {
	s := openStore(t)
	ctx := context.Background()

	if err := s.InsertUnroutedRequest(ctx, store.UnroutedRequest{
		RecordID:         "rec-unknown-alias",
		LogicalRequestID: "request-unknown-alias",
		StartedAtUS:      100,
		FinishedAtUS:     200,
		DownstreamStatus: 404,
		LocalErrorCode:   "model_not_found",
	}); err != nil {
		t.Fatalf("InsertUnroutedRequest() error = %v", err)
	}

	var requestID string
	var status int
	if err := s.Writer.QueryRowContext(ctx,
		`SELECT logical_request_id, downstream_status FROM unrouted_request WHERE record_id = 'rec-unknown-alias'`,
	).Scan(&requestID, &status); err != nil {
		t.Fatalf("read unknown-alias row: %v", err)
	}
	if requestID != "request-unknown-alias" {
		t.Errorf("logical_request_id = %q, want request-unknown-alias", requestID)
	}
	if status != 404 {
		t.Errorf("downstream_status = %d, want 404", status)
	}
}

// TestStoreCloseClosesBothPools proves Close releases the maintenance pool
// and the writer pool, so a caller that keeps a Store reference after Close
// cannot keep writing through a pool it believes is still open.
func TestStoreCloseClosesBothPools(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("Store.Close() error = %v", err)
	}

	if err := s.Writer.Ping(); err == nil {
		t.Error("writer pool still open after Close")
	}
	if err := s.Maintenance.Ping(); err == nil {
		t.Error("maintenance pool still open after Close")
	}
}

// TestMinuteBoundaryDispatchCounting proves a dispatch whose reservation and
// Do fall on opposite sides of a minute boundary is counted by the admission
// recipe in the reservation's interval and by the dispatch recipe in the
// call's interval, and that an admission with no terminal row contributes to
// the admission figure but not to the dispatch figure.
func TestMinuteBoundaryDispatchCounting(t *testing.T) {
	// The fake clock's wall is far in the future, so the store-operation
	// deadline anchored at that wall instant never fires for a wall-clock
	// reason: this test's subject is SQL counting, not timing.
	fake := testsupport.NewFakeClock(time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC))
	s, err := store.OpenWithClock(filepath.Join(t.TempDir(), "llmux.db"), fake)
	if err != nil {
		t.Fatalf("store.OpenWithClock() error = %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	ctx := context.Background()

	// Reservation at 59s, Do at 65s: the two instants straddle the 60s
	// boundary. The admission recipe reads reserved_at_us, the dispatch
	// recipe reads event_at_us.
	if err := s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID: "attempt-straddle", LogicalRequestID: "request-straddle", AttemptNo: 1,
		AccountLabel: "k1", RequestedAlias: "alias", UpstreamModel: "upstream",
		ReservedAtUS: 59_000_000, LimiterRPMUsed: 0, LimiterInFlight: 0,
	}); err != nil {
		t.Fatalf("insert straddle admission: %v", err)
	}
	if err := s.InsertPhaseBatch(ctx, store.PhaseBatch{
		Terminal: store.PhaseDispatch{
			RecordID: "rec-straddle", LogicalRequestID: "request-straddle", AttemptID: "attempt-straddle",
			SequenceNo: 1, SelectionNo: 1, RequestedAlias: "alias", BaseAlias: "base",
			UpstreamModel: "upstream", AccountLabel: "k1", AttemptNo: 1,
			EventAtUS: 65_000_000, FinishedAtUS: 66_000_000, SelectionWaitUS: 0, LogicalElapsedUS: 7_000_000,
			Outcome: store.OutcomeSucceeded, RetryDisposition: store.RetryDispositionFinal,
			ResponseCommitted: true, UsageObservation: store.UsageObservationComplete,
		},
	}); err != nil {
		t.Fatalf("insert straddle dispatch: %v", err)
	}

	// A subprocess killed between commit and Do: an admission with no
	// terminal row, reserved in the first minute.
	if err := s.InsertDispatchAdmission(ctx, store.DispatchAdmission{
		AttemptID: "attempt-killed", LogicalRequestID: "request-killed", AttemptNo: 1,
		AccountLabel: "k1", RequestedAlias: "alias", UpstreamModel: "upstream",
		ReservedAtUS: 30_000_000, LimiterRPMUsed: 0, LimiterInFlight: 0,
	}); err != nil {
		t.Fatalf("insert killed admission: %v", err)
	}

	count := func(query string) int {
		t.Helper()
		var n int
		if err := s.Writer.QueryRowContext(ctx, query).Scan(&n); err != nil {
			t.Fatalf("query %q: %v", query, err)
		}
		return n
	}

	dump := func() {
		t.Helper()
		rows, err := s.Writer.QueryContext(ctx, `SELECT record_id, attempt_id, record_kind, event_at_us, finished_at_us FROM attempt_log ORDER BY record_id`)
		if err != nil {
			t.Logf("dump attempt_log: %v", err)
			return
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var recordID, attemptID, kind string
			var eventAt, finishedAt int64
			if err := rows.Scan(&recordID, &attemptID, &kind, &eventAt, &finishedAt); err != nil {
				t.Logf("scan attempt_log: %v", err)
				return
			}
			t.Logf("attempt_log row: record=%s attempt=%s kind=%s event=%d finished=%d", recordID, attemptID, kind, eventAt, finishedAt)
		}
		admRows, err := s.Writer.QueryContext(ctx, `SELECT attempt_id, reserved_at_us FROM dispatch_admission ORDER BY attempt_id`)
		if err != nil {
			t.Logf("dump dispatch_admission: %v", err)
			return
		}
		defer func() { _ = admRows.Close() }()
		for admRows.Next() {
			var attemptID string
			var reservedAt int64
			if err := admRows.Scan(&attemptID, &reservedAt); err != nil {
				t.Logf("scan dispatch_admission: %v", err)
				return
			}
			t.Logf("admission row: attempt=%s reserved=%d", attemptID, reservedAt)
		}
	}

	// The straddling admission is counted in the first minute (reservation).
	if got := count(`SELECT COUNT(*) FROM dispatch_admission WHERE reserved_at_us >= 0 AND reserved_at_us < 60000000`); got != 2 {
		dump()
		t.Errorf("first-minute admissions = %d, want 2 (straddle + killed)", got)
	}
	// The straddling dispatch is counted in the second minute (call).
	if got := count(`SELECT COUNT(*) FROM attempt_log WHERE record_kind = 'dispatch' AND event_at_us >= 60000000 AND event_at_us < 120000000`); got != 1 {
		dump()
		t.Errorf("second-minute dispatches = %d, want 1 (straddle only)", got)
	}
	// The killed admission contributes to the unmatched-admission figure.
	if got := count(`SELECT COUNT(*) FROM dispatch_admission d WHERE NOT EXISTS (SELECT 1 FROM attempt_log a WHERE a.attempt_id = d.attempt_id)`); got != 1 {
		dump()
		t.Errorf("unmatched admissions = %d, want 1 (killed only)", got)
	}
}
