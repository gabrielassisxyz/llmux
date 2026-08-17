package store_test

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

// TestSchemaIndexesPresent proves every index named by the bead exists after
// the initial migration. The index list is the contract: each one exists for
// a named query recipe, and a missing index silently degrades that recipe to
// a scan of a table that grows for months.
func TestSchemaIndexesPresent(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	indexes := []string{
		"idx_dispatch_admission_account_reserved",
		"idx_attempt_log_attempt_id",
		"idx_attempt_log_event_at",
		"idx_attempt_log_account_event",
		"idx_attempt_log_session_recovery",
		"idx_attempt_log_requested_alias_event",
		"idx_attempt_log_outcome_event",
		"idx_attempt_log_error_class_event",
		"idx_unrouted_request_finished_at",
	}
	for _, name := range indexes {
		var count int
		if err := s.Writer.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM sqlite_master WHERE type = 'index' AND name = ?", name).Scan(&count); err != nil {
			t.Fatalf("query index %s: %v", name, err)
		}
		if count != 1 {
			t.Errorf("index %s missing", name)
		}
	}
}

// TestSessionRecoveryQueryUsesPartialIndex proves the session recovery query
// does not scan the table, and that the partial successful-completion index is
// usable for it. This is the assertion that protects startup latency: recovery
// runs before the listener binds, so a scan here delays every process start.
//
// The un-hinted query is answered through an index rather than a full table
// scan. SQLite's planner prefers the (outcome, event_at_us) index for the
// outcome equality, so the partial index is asserted separately through an
// INDEXED BY plan, which is the shape the session recovery query uses to pin
// the ordering index.
func TestSessionRecoveryQueryUsesPartialIndex(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	const query = `
		SELECT session_key, account_label, finished_at_us, logical_elapsed_us, record_id
		FROM attempt_log
		WHERE outcome = 'succeeded' AND session_key IS NOT NULL AND finished_at_us >= 0
		ORDER BY session_key, finished_at_us DESC
	`

	// The un-hinted query must not degrade to a full table scan.
	for _, d := range explainQueryPlan(t, ctx, s, query) {
		if strings.Contains(d, "SCAN attempt_log") && !strings.Contains(d, "USING INDEX") {
			t.Errorf("full table scan in plan: %s", d)
		}
	}

	// The partial index is the intended ordering index and must be usable.
	indexed := explainQueryPlan(t, ctx, s, strings.Replace(query,
		"FROM attempt_log", "FROM attempt_log INDEXED BY idx_attempt_log_session_recovery", 1))
	found := false
	for _, d := range indexed {
		if strings.Contains(d, "idx_attempt_log_session_recovery") {
			found = true
		}
	}
	if !found {
		t.Errorf("partial index not usable; plan details: %v", indexed)
	}
}

// explainQueryPlan runs EXPLAIN QUERY PLAN and returns the detail column of
// every plan row.
func explainQueryPlan(t *testing.T, ctx context.Context, s *store.Store, query string) []string {
	t.Helper()
	rows, err := s.Writer.QueryContext(ctx, "EXPLAIN QUERY PLAN "+query)
	if err != nil {
		t.Fatalf("EXPLAIN QUERY PLAN: %v", err)
	}
	defer func() { _ = rows.Close() }()

	var details []string
	for rows.Next() {
		var id, parent, notused int
		var detail string
		if err := rows.Scan(&id, &parent, &notused, &detail); err != nil {
			t.Fatalf("scan plan row: %v", err)
		}
		details = append(details, detail)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate plan rows: %v", err)
	}
	return details
}

// insertManyAttempts seeds the store with n successful dispatch rows spread
// across a bounded set of session keys, so the session recovery query has a
// realistic population to read. It is benchmark setup, not a test assertion.
func insertManyAttempts(tb testing.TB, s *store.Store, ctx context.Context, n int) {
	tb.Helper()
	tx, err := s.Writer.BeginTx(ctx, nil)
	if err != nil {
		tb.Fatalf("begin transaction: %v", err)
	}
	defer func() { _ = tx.Rollback() }()

	admissionStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO dispatch_admission (
			attempt_id, logical_request_id, attempt_no, account_label,
			requested_alias, upstream_model, reserved_at_us, limiter_rpm_used, limiter_in_flight
		) VALUES (?, ?, 1, 'k1', 'alias', 'upstream', ?, 0, 0)
	`)
	if err != nil {
		tb.Fatalf("prepare admission: %v", err)
	}
	defer func() { _ = admissionStmt.Close() }()

	attemptStmt, err := tx.PrepareContext(ctx, `
		INSERT INTO attempt_log (
			record_id, logical_request_id, attempt_id, sequence_no, selection_no, record_kind,
			requested_alias, base_alias, upstream_model, session_key, account_label, attempt_no,
			is_spill, event_at_us, finished_at_us, selection_wait_us, attempt_duration_us,
			logical_elapsed_us, outcome, retry_disposition, response_committed, usage_observation
		) VALUES (?, ?, ?, 1, 1, 'dispatch', 'alias', 'base', 'upstream', ?, 'k1', 1, 0, ?, ?, 0, 0, ?, 'succeeded', 'final', 1, 'complete')
	`)
	if err != nil {
		tb.Fatalf("prepare attempt: %v", err)
	}
	defer func() { _ = attemptStmt.Close() }()

	for i := 0; i < n; i++ {
		attemptID := fmt.Sprintf("attempt-%d", i)
		requestID := fmt.Sprintf("request-%d", i)
		sessionKey := fmt.Sprintf("session-%d", i%1000)
		at := int64(i)
		if _, err := admissionStmt.ExecContext(ctx, attemptID, requestID, at); err != nil {
			tb.Fatalf("insert admission %d: %v", i, err)
		}
		if _, err := attemptStmt.ExecContext(ctx, fmt.Sprintf("record-%d", i), requestID, attemptID, sessionKey, at, at, int64(100)); err != nil {
			tb.Fatalf("insert attempt %d: %v", i, err)
		}
	}

	if err := tx.Commit(); err != nil {
		tb.Fatalf("commit: %v", err)
	}
}

// BenchmarkSessionRecoveryQueryManyRows measures the session recovery query
// against a store seeded with tens of thousands of successful completions.
// Run with -benchmem and compare across runs with benchstat; the figure is
// the evidence that recovery does not materially delay startup. The query is
// pinned to the partial index with INDEXED BY, which is the shape the session
// recovery query uses to avoid the sort the planner would otherwise add.
func BenchmarkSessionRecoveryQueryManyRows(b *testing.B) {
	s, err := store.Open(filepath.Join(b.TempDir(), "llmux.db"))
	if err != nil {
		b.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()
	ctx := context.Background()

	insertManyAttempts(b, s, ctx, 50000)

	const query = `
		SELECT session_key, account_label, finished_at_us, logical_elapsed_us, record_id
		FROM attempt_log INDEXED BY idx_attempt_log_session_recovery
		WHERE outcome = 'succeeded' AND session_key IS NOT NULL AND finished_at_us >= 0
		ORDER BY session_key, finished_at_us DESC
	`

	b.ResetTimer()
	for b.Loop() {
		rows, err := s.Writer.QueryContext(ctx, query)
		if err != nil {
			b.Fatalf("query: %v", err)
		}
		for rows.Next() {
			var sessionKey, accountLabel, recordID string
			var finishedAt, logicalElapsed int64
			if err := rows.Scan(&sessionKey, &accountLabel, &finishedAt, &logicalElapsed, &recordID); err != nil {
				b.Fatalf("scan: %v", err)
			}
		}
		if err := rows.Err(); err != nil {
			b.Fatalf("iterate: %v", err)
		}
		_ = rows.Close()
	}
}
