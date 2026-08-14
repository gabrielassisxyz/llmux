package store

import (
	"context"
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
