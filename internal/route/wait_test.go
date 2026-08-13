package route

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// TestWaitReturnsNotifiedWhenTokenFires proves the basic wake path: a
// waiter captures a token, another goroutine mutates state through a real
// coordinator method that notifies, and Wait returns WaitNotified rather
// than timing out or being canceled.
func TestWaitReturnsNotifiedWhenTokenFires(t *testing.T) {
	c := testCoordinator()
	c.IncrementInFlight(catalog.AccountK1)

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	outcomeCh := make(chan WaitOutcome, 1)
	go func() {
		outcomeCh <- c.Wait(context.Background(), token)
	}()

	// Give the waiter a moment to actually block on the token before the
	// release fires, so a bug that returned immediately regardless of the
	// token would not accidentally look correct.
	time.Sleep(20 * time.Millisecond)
	c.DecrementInFlight(catalog.AccountK1)

	select {
	case outcome := <-outcomeCh:
		if outcome != WaitNotified {
			t.Fatalf("outcome = %v, want WaitNotified", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after the release notified it")
	}
}

// TestWaitReturnsCanceledPromptly proves a canceled waiter exits without
// waiting for the account-acquisition ceiling.
func TestWaitReturnsCanceledPromptly(t *testing.T) {
	c := testCoordinator()

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	outcomeCh := make(chan WaitOutcome, 1)
	start := time.Now()
	go func() {
		outcomeCh <- c.Wait(ctx, token)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case outcome := <-outcomeCh:
		if outcome != WaitCanceled {
			t.Fatalf("outcome = %v, want WaitCanceled", outcome)
		}
		if elapsed := time.Since(start); elapsed >= time.Second {
			t.Fatalf("canceled wait took %v, want well under the account-acquisition ceiling", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after cancellation")
	}
}

// TestWaitTimesOutAtTheAccountAcquisitionCeiling proves the 60-second
// ceiling using the fake clock's monotonic advance rather than a real
// 60-second sleep: the fake timer fires the instant AdvanceMonotonic
// crosses its deadline, so this test proves the exact boundary in
// milliseconds of real wall-clock time.
func TestWaitTimesOutAtTheAccountAcquisitionCeiling(t *testing.T) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	c := NewCoordinator(testKeys(), fake)

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	outcomeCh := make(chan WaitOutcome, 1)
	go func() {
		outcomeCh <- c.Wait(context.Background(), token)
	}()

	// Give the waiter goroutine time to actually create its timer before
	// advancing, since NewTimer must be called before the advance can
	// find it due.
	time.Sleep(20 * time.Millisecond)
	fake.AdvanceMonotonic(policy.MaxAccountAcquisitionTime - time.Second)

	select {
	case outcome := <-outcomeCh:
		t.Fatalf("outcome = %v before the ceiling elapsed, want no result yet", outcome)
	case <-time.After(50 * time.Millisecond):
	}

	fake.AdvanceMonotonic(time.Second)

	select {
	case outcome := <-outcomeCh:
		if outcome != WaitTimedOut {
			t.Fatalf("outcome = %v, want WaitTimedOut", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return once the account-acquisition ceiling elapsed")
	}
}

// TestHealthMutationNotifiesWaiters proves the wake mechanism generically,
// covering the case a future health/cooldown bead will rely on without
// this bead inventing that bead's business logic: any mutation that calls
// notifyLocked under the lock wakes a token captured before it, regardless
// of which field changed.
func TestHealthMutationNotifiesWaiters(t *testing.T) {
	c := testCoordinator()

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	outcomeCh := make(chan WaitOutcome, 1)
	go func() {
		outcomeCh <- c.Wait(context.Background(), token)
	}()

	time.Sleep(20 * time.Millisecond)
	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].health = HealthCoolingDown
	c.notifyLocked()
	c.mu.Unlock()

	select {
	case outcome := <-outcomeCh:
		if outcome != WaitNotified {
			t.Fatalf("outcome = %v, want WaitNotified", outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Wait did not return after a health mutation notified it")
	}
}

// TestNoLostWakeupOnTokenReplacement is the stress test for the classic bug
// in this pattern: if a waiter captured a token, then a notify replaced it,
// then a second notify fired again, the waiter must still have observed
// the first notify rather than being left waiting on a channel that was
// already superseded before it ever started selecting.
func TestNoLostWakeupOnTokenReplacement(t *testing.T) {
	const rounds = 500

	for round := 0; round < rounds; round++ {
		c := testCoordinator()

		c.mu.Lock()
		token := c.WaitToken()
		c.mu.Unlock()

		outcomeCh := make(chan WaitOutcome, 1)
		go func() {
			outcomeCh <- c.Wait(context.Background(), token)
		}()

		// Race two independent notifications against the waiter starting
		// up. Whichever happens first, the waiter must observe the token
		// it captured firing.
		var wg sync.WaitGroup
		wg.Add(2)
		go func() {
			defer wg.Done()
			c.IncrementInFlight(catalog.AccountK1)
			c.DecrementInFlight(catalog.AccountK1)
		}()
		go func() {
			defer wg.Done()
			c.IncrementInFlight(catalog.AccountK2)
			c.DecrementInFlight(catalog.AccountK2)
		}()
		wg.Wait()

		select {
		case outcome := <-outcomeCh:
			if outcome != WaitNotified {
				t.Fatalf("round %d: outcome = %v, want WaitNotified", round, outcome)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("round %d: wakeup lost, Wait never returned", round)
		}
	}
}

// TestManyCanceledWaitersExitWithoutLeaking drives many concurrent waiters,
// cancels them all, and joins every one through a WaitGroup: a goroutine
// leak here would leave the WaitGroup permanently un-Done and the test
// would hang past its own deadline instead of passing.
func TestManyCanceledWaitersExitWithoutLeaking(t *testing.T) {
	c := testCoordinator()
	const waiters = 200

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	results := make(chan WaitOutcome, waiters)
	for i := 0; i < waiters; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results <- c.Wait(ctx, token)
		}()
	}

	time.Sleep(20 * time.Millisecond)
	cancel()

	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("not every waiter exited after cancellation; possible goroutine leak")
	}
	close(results)

	for outcome := range results {
		if outcome != WaitCanceled {
			t.Errorf("outcome = %v, want WaitCanceled", outcome)
		}
	}
}
