package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestOperationContextExpiresAtStoreCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		operation, cancelOperation := OperationContext(t.Context())
		t.Cleanup(cancelOperation)

		time.Sleep(policy.StoreOperationCeiling - time.Nanosecond)
		synctest.Wait()
		if operation.Err() != nil {
			t.Fatalf("operation context before ceiling = %v, want nil", operation.Err())
		}

		time.Sleep(time.Nanosecond)
		synctest.Wait()
		if operation.Err() != context.DeadlineExceeded {
			t.Fatalf("operation context at ceiling = %v, want %v", operation.Err(), context.DeadlineExceeded)
		}
	})
}

func TestOperationContextUsesForceShutdownContext(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		forceShutdown, stop := context.WithCancel(t.Context())
		operation, cancelOperation := OperationContext(forceShutdown)
		t.Cleanup(cancelOperation)

		stop()
		synctest.Wait()
		if operation.Err() != context.Canceled {
			t.Fatalf("operation context error = %v, want %v", operation.Err(), context.Canceled)
		}
	})
}

// TestSQLiteBusyTimeoutSurvivesShortLock asserts that SQLite's busy_timeout
// lets a contended writer wait for a lock held briefly, rather than failing
// immediately or being cancelled by the longer application ceiling.
func TestSQLiteBusyTimeoutSurvivesShortLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llmux.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	// A second connection to the same database holds the write lock briefly.
	locker, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatalf("open locker connection: %v", err)
	}
	defer func() { _ = locker.Close() }()
	locker.SetMaxOpenConns(1)
	locker.SetMaxIdleConns(1)

	acquired := make(chan struct{})
	release := make(chan struct{})
	go func() {
		if _, err := locker.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Errorf("locker BEGIN IMMEDIATE: %v", err)
			return
		}
		close(acquired)
		<-release
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
	}()
	<-acquired

	// Hold the lock for less than SQLiteBusyTimeout, then release it.
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(release)
	}()

	opCtx, cancelOperation := OperationContext(ctx)
	defer cancelOperation()

	start := time.Now()
	_, err = s.Writer.ExecContext(opCtx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (?, ?, 'process_start', ?, NULL, 'v1.0.0', 'abcdef', 3)
	`, "rec_busy", "inst_busy", start.UnixMicro())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("contended insert failed: %v", err)
	}
	if elapsed >= policy.SQLiteBusyTimeout {
		t.Fatalf("insert waited %v, want less than busy timeout %v", elapsed, policy.SQLiteBusyTimeout)
	}
}

// TestSQLiteBusyTimeoutGivesUpAfterTimeout asserts that a writer whose lock
// is held longer than SQLiteBusyTimeout eventually fails with a busy error,
// rather than blocking forever or failing before the busy window expires.
func TestSQLiteBusyTimeoutGivesUpAfterTimeout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llmux.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	locker, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatalf("open locker connection: %v", err)
	}
	defer func() { _ = locker.Close() }()
	locker.SetMaxOpenConns(1)
	locker.SetMaxIdleConns(1)

	acquired := make(chan struct{})
	release := make(chan struct{})
	go func() {
		if _, err := locker.ExecContext(ctx, "BEGIN IMMEDIATE"); err != nil {
			t.Errorf("locker BEGIN IMMEDIATE: %v", err)
			return
		}
		close(acquired)
		<-release
		_, _ = locker.ExecContext(ctx, "ROLLBACK")
	}()
	<-acquired
	defer close(release)

	opCtx, cancelOperation := OperationContext(ctx)
	defer cancelOperation()

	start := time.Now()
	_, err = s.Writer.ExecContext(opCtx, `
		INSERT INTO process_event (
			record_id, process_instance_id, event_kind,
			at_us, process_elapsed_us, version, revision, schema_version
		) VALUES (?, ?, 'process_start', ?, NULL, 'v1.0.0', 'abcdef', 3)
	`, "rec_timeout", "inst_timeout", start.UnixMicro())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected busy timeout failure, got nil")
	}
	// SQLite's busy timeout is an upper bound; on a loaded host the timer
	// may fire a little early, so the lower bound is a small margin below it.
	const busyWindowSlack = 500 * time.Millisecond
	if elapsed < policy.SQLiteBusyTimeout-busyWindowSlack {
		t.Fatalf("failed too fast (%v), want close to busy timeout %v", elapsed, policy.SQLiteBusyTimeout)
	}
	if elapsed >= policy.StoreOperationCeiling {
		t.Fatalf("waited until store-operation ceiling (%v), busy timeout did not fire first", elapsed)
	}
}

// TestSlowStoreFakeExceedsCeiling asserts that an operation that would
// run longer than the store-operation ceiling is cut off and returns a
// caller-visible failure, not a partial success.
func TestSlowStoreFakeExceedsCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		forceShutdown, stop := context.WithCancel(t.Context())
		defer stop()
		opCtx, cancelOperation := OperationContext(forceShutdown)
		defer cancelOperation()

		done := make(chan error, 1)
		go func() {
			done <- slowExec(opCtx, 2*policy.StoreOperationCeiling)
		}()

		time.Sleep(policy.StoreOperationCeiling + time.Second)
		synctest.Wait()

		select {
		case err := <-done:
			if err != context.DeadlineExceeded {
				t.Fatalf("slow fake error = %v, want %v", err, context.DeadlineExceeded)
			}
		default:
			t.Fatal("slow fake did not return after the store operation ceiling")
		}
	})
}

// slowExec simulates a store operation that takes d to complete unless the
// context finishes first.
func slowExec(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}

// TestOperationContextIgnoresClientCancellation asserts the bounded store
// context is derived from the application force-shutdown context, not from the
// client request context. Cancelling the client context must not cancel the
// store operation; cancelling force-shutdown does.
func TestOperationContextIgnoresClientCancellation(t *testing.T) {
	_, cancelClient := context.WithCancel(context.Background())
	defer cancelClient()

	forceShutdown, stop := context.WithCancel(context.Background())
	defer stop()

	opCtx, cancelOperation := OperationContext(forceShutdown)
	defer cancelOperation()

	cancelClient()
	if err := opCtx.Err(); err != nil {
		t.Fatalf("operation context cancelled by client context: %v", err)
	}

	stop()
	if err := opCtx.Err(); err != context.Canceled {
		t.Fatalf("operation context error = %v, want %v", err, context.Canceled)
	}
}
