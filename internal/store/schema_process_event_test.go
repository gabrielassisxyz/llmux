package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

func TestProcessEventSchemaConstraints(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "llmux.db"))
	if err != nil {
		t.Fatalf("store.Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	ctx := context.Background()

	// 1. Successful start event.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_1', 'inst_1', 'process_start',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err != nil {
		t.Errorf("expected successful process_start insert, got: %v", err)
	}

	// 2. Successful stop event for the same instance.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_1', 'inst_1', 'process_stop',
			2000, 1000, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err != nil {
		t.Errorf("expected successful process_stop insert, got: %v", err)
	}

	// 3. Reject duplicate process_start for the same instance.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_2', 'inst_1', 'process_start',
			1500, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for duplicate process_start, got nil")
	}

	// 4. Reject start event with non-null process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_start_3', 'inst_2', 'process_start',
			1000, 500, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_start with elapsed time, got nil")
	}

	// 5. Reject stop event with null process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_2', 'inst_3', 'process_stop',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_stop with null elapsed time, got nil")
	}

	// 6. Reject stop event with negative process_elapsed_us.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_stop_3', 'inst_4', 'process_stop',
			1000, -1, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for process_stop with negative elapsed time, got nil")
	}

	// 7. Reject invalid event_kind.
	_, err = s.Writer.ExecContext(ctx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (
			'rec_invalid_1', 'inst_5', 'process_crash',
			1000, NULL, 'v1.0.0', 'abcdef', 3
		)
	`)
	if err == nil {
		t.Errorf("expected constraint failure for invalid event_kind, got nil")
	}
}
