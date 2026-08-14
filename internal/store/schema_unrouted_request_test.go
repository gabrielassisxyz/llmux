package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

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
