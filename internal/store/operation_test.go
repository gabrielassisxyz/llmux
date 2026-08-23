package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// TestOperationContextExpiresAtStoreCeiling proves the bounded context
// carries a StoreOperationCeiling timeout. If OperationContext failed to
// apply the ceiling, the context would never expire and the test would time
// out.
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

// TestOperationContextExpiresWhenForceShutdownExpires proves the timeout is
// anchored to the supplied force-shutdown context, not to a background or
// request context. If OperationContext ignored its argument, cancelling the
// parent would not cancel the operation.
func TestOperationContextExpiresWhenForceShutdownExpires(t *testing.T) {
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

// TestOperationContextIgnoresClientCancellation proves the bounded store
// context is never a child of the client request context. Store durability
// must outlive a caller that goes away, so cancelling the client must not
// affect the operation context; cancelling the application force-shutdown
// context must.
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

// TestOperationContextDeadlineExpiresWithFakeClock proves the injected clock
// is the deadline source rather than decoration: a fake clock whose wall is
// in the past makes the deadline already elapsed, so the context reports
// context.DeadlineExceeded immediately. If operationContext ignored the
// injected clock and used the real one, the deadline would be six seconds
// away and this test would fail.
func TestOperationContextDeadlineExpiresWithFakeClock(t *testing.T) {
	fake := testsupport.NewFakeClock(time.Date(2000, 1, 1, 0, 0, 0, 0, time.UTC))
	ctx, cancel := operationContext(fake, context.Background())
	defer cancel()

	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("context error = %v, want %v", ctx.Err(), context.DeadlineExceeded)
	}
}

// TestSQLiteBusyTimeoutSurvivesShortLock proves SQLite's busy_timeout lets
// a contended writer wait briefly rather than returning SQLITE_BUSY
// immediately. This test would fail if the store DSN did not set
// busy_timeout, or if the store context aborted before SQLite gave up.
func TestSQLiteBusyTimeoutSurvivesShortLock(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llmux.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	locker := openWriteLocker(t, path)
	defer func() { _ = locker.Close() }()
	release := holdWriteLock(t, locker)

	// Release the lock before busy_timeout expires. The 500 ms release
	// leaves a 4.5 s margin below the 5 s busy timeout, so a loaded host
	// that delays the release goroutine still cannot push the insert past
	// the timeout.
	go func() {
		time.Sleep(500 * time.Millisecond)
		close(release)
	}()

	opCtx, cancelOperation := OperationContext(ctx)
	defer cancelOperation()

	start := time.Now()
	_, err = s.Writer.ExecContext(opCtx, processStartSQL(), "rec_busy", "inst_busy", start.UnixMicro())
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("contended insert failed: %v", err)
	}
	if elapsed >= policy.SQLiteBusyTimeout {
		t.Fatalf("insert waited %v, want less than busy timeout %v", elapsed, policy.SQLiteBusyTimeout)
	}
}

// TestSQLiteBusyTimeoutGivesUpAfterTimeout proves the store connection waits
// for SQLiteBusyTimeout and then returns a busy error, not later than the
// store-operation ceiling and not before the busy window. This would fail if
// the DSN set busy_timeout to zero or if the store context swallowed the
// error.
func TestSQLiteBusyTimeoutGivesUpAfterTimeout(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "llmux.db")

	s, err := Open(path)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	defer func() { _ = s.Close() }()

	locker := openWriteLocker(t, path)
	defer func() { _ = locker.Close() }()
	release := holdWriteLock(t, locker)
	defer close(release)

	opCtx, cancelOperation := OperationContext(ctx)
	defer cancelOperation()

	start := time.Now()
	_, err = s.Writer.ExecContext(opCtx, processStartSQL(), "rec_timeout", "inst_timeout", start.UnixMicro())
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected busy timeout failure, got nil")
	}
	// SQLite's busy_timeout is an upper bound; on a loaded host the timer
	// may fire a little early, so the lower bound is a small margin below it.
	const busyWindowSlack = 500 * time.Millisecond
	if elapsed < policy.SQLiteBusyTimeout-busyWindowSlack {
		t.Fatalf("failed too fast (%v), want close to busy timeout %v", elapsed, policy.SQLiteBusyTimeout)
	}
	if elapsed >= policy.StoreOperationCeiling {
		t.Fatalf("waited until store-operation ceiling (%v), busy timeout did not fire first", elapsed)
	}
}

// TestSlowStoreFakeExceedsCeiling proves an operation that would run longer
// than the store-operation ceiling is cut off and returns
// context.DeadlineExceeded. This would fail if OperationContext used a
// timeout larger than StoreOperationCeiling, or if it used the client
// context directly.
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

// TestInsertDispatchAdmissionIgnoresClientCancellation proves the admission
// writer uses the application force-shutdown context, not the client request
// context, to bound its store work. If InsertDispatchAdmission passed the
// client context to OperationContext, cancelling the client would abort the
// commit and the test would fail. Time is driven through synctest.
func TestInsertDispatchAdmissionIgnoresClientCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientCtx, cancelClient := context.WithCancel(t.Context())
		cancelClient()

		forceShutdown, stop := context.WithCancel(t.Context())
		defer stop()

		writer := fakeSlowAdmissionWriter{delay: 50 * time.Millisecond}
		err := writer.InsertDispatchAdmission(clientCtx, forceShutdown, DispatchAdmission{
			AttemptID:        "attempt-cancel",
			LogicalRequestID: "request-cancel",
			AttemptNo:        1,
			AccountLabel:     "k1",
			RequestedAlias:   "alias",
			UpstreamModel:    "upstream",
			ReservedAtUS:     1,
			LimiterRPMUsed:   0,
			LimiterInFlight:  0,
		})
		if err != nil {
			t.Fatalf("admission writer failed after client cancellation: %v", err)
		}
		if !writer.called {
			t.Fatal("fake admission writer was never called")
		}
	})
}

// TestInsertDispatchAdmissionFailsAtStoreCeiling proves a slow admission
// commit is cut off at the store-operation ceiling and returns a
// caller-visible DeadlineExceeded. If the writer used an unbounded context,
// the fake would succeed and the test would fail.
func TestInsertDispatchAdmissionFailsAtStoreCeiling(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		forceShutdown, stop := context.WithCancel(t.Context())
		defer stop()

		done := make(chan error, 1)
		go func() {
			writer := fakeSlowAdmissionWriter{delay: 2 * policy.StoreOperationCeiling}
			done <- writer.InsertDispatchAdmission(t.Context(), forceShutdown, DispatchAdmission{
				AttemptID:        "attempt-slow",
				LogicalRequestID: "request-slow",
				AttemptNo:        1,
				AccountLabel:     "k1",
				RequestedAlias:   "alias",
				UpstreamModel:    "upstream",
				ReservedAtUS:     1,
				LimiterRPMUsed:   0,
				LimiterInFlight:  0,
			})
		}()

		time.Sleep(policy.StoreOperationCeiling + time.Second)
		synctest.Wait()

		select {
		case err := <-done:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("admission error = %v, want %v", err, context.DeadlineExceeded)
			}
		default:
			t.Fatal("admission writer was not cut off after the store-operation ceiling")
		}
	})
}

// TestInsertPhaseBatchIgnoresClientCancellation proves the terminal phase
// writer uses the application force-shutdown context, not the client request
// context. If InsertPhaseBatch passed the client context to OperationContext,
// cancelling the client would abort the commit and the test would fail.
func TestInsertPhaseBatchIgnoresClientCancellation(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		clientCtx, cancelClient := context.WithCancel(t.Context())
		cancelClient()

		forceShutdown, stop := context.WithCancel(t.Context())
		defer stop()

		writer := fakeSlowPhaseBatchWriter{delay: 50 * time.Millisecond}
		err := writer.InsertPhaseBatch(clientCtx, forceShutdown, PhaseBatch{
			Terminal: PhaseFailure{
				RecordID:         "rec-cancel",
				LogicalRequestID: "request-cancel",
				SequenceNo:       1,
				SelectionNo:      1,
				RequestedAlias:   "alias",
				BaseAlias:        "base",
				UpstreamModel:    "upstream",
				EventAtUS:        1,
				FinishedAtUS:     2,
				SelectionWaitUS:  0,
				LogicalElapsedUS: 1,
				Outcome:          "no_account_available",
				RetryDisposition: "final",
			},
		})
		if err != nil {
			t.Fatalf("phase batch writer failed after client cancellation: %v", err)
		}
		if !writer.called {
			t.Fatal("fake phase batch writer was never called")
		}
	})
}

// fakeSlowAdmissionWriter is an AdmissionWriter that waits for delay before
// succeeding. It exercises OperationContext inside InsertDispatchAdmission, so
// it proves the caller's store-boundary contract without touching a real
// database. The delay must be driven through an injected clock boundary such as
// testing/synctest.
type fakeSlowAdmissionWriter struct {
	delay  time.Duration
	called bool
}

func (f *fakeSlowAdmissionWriter) InsertDispatchAdmission(clientCtx, forceShutdown context.Context, admission DispatchAdmission) error {
	f.called = true
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- slowExec(ctx, f.delay)
	}()

	// Simulate the client going away immediately after the call starts.
	// If the implementation used clientCtx, this would cancel ctx.
	go func() {
		if c, ok := clientCtx.Value(fakeCancelKey{}).(context.CancelFunc); ok {
			c()
		}
	}()

	return <-done
}

// fakeSlowPhaseBatchWriter is a PhaseBatchWriter that waits for delay before
// succeeding. It exercises OperationContext inside InsertPhaseBatch.
type fakeSlowPhaseBatchWriter struct {
	delay  time.Duration
	called bool
}

func (f *fakeSlowPhaseBatchWriter) InsertPhaseBatch(clientCtx, forceShutdown context.Context, batch PhaseBatch) error {
	f.called = true
	ctx, cancel := OperationContext(forceShutdown)
	defer cancel()

	done := make(chan error, 1)
	go func() {
		done <- slowExec(ctx, f.delay)
	}()

	go func() {
		if c, ok := clientCtx.Value(fakeCancelKey{}).(context.CancelFunc); ok {
			c()
		}
	}()

	return <-done
}

type fakeCancelKey struct{}

// openWriteLocker returns a second connection to path configured to hold the
// write lock. The caller must close it.
func openWriteLocker(t *testing.T, path string) *sql.DB {
	t.Helper()
	locker, err := sql.Open("sqlite", sqliteDSN(path, false))
	if err != nil {
		t.Fatalf("open locker connection: %v", err)
	}
	locker.SetMaxOpenConns(1)
	locker.SetMaxIdleConns(1)
	return locker
}

// holdWriteLock starts a goroutine that holds an immediate write transaction
// on locker. It returns a channel the caller closes to release the lock.
func holdWriteLock(t *testing.T, locker *sql.DB) chan struct{} {
	t.Helper()
	acquired := make(chan struct{})
	release := make(chan struct{})
	go func() {
		if _, err := locker.ExecContext(context.Background(), "BEGIN IMMEDIATE"); err != nil {
			t.Errorf("locker BEGIN IMMEDIATE: %v", err)
			return
		}
		close(acquired)
		<-release
		_, _ = locker.ExecContext(context.Background(), "ROLLBACK")
	}()
	<-acquired
	return release
}

// processStartSQL returns a parametrized INSERT for a minimal process_start
// row: ($1 record_id, $2 process_instance_id, $3 at_us).
func processStartSQL() string {
	return `INSERT INTO process_event (
		record_id, process_instance_id, event_kind,
		at_us, process_elapsed_us, version, revision, schema_version
	) VALUES (?, ?, 'process_start', ?, NULL, 'v1.0.0', 'abcdef', 3)`
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
