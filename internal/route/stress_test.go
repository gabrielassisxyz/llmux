package route

import (
	"context"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// This file holds the coordinator-only half of the concurrency stress
// suite: hundreds or thousands of goroutines driven against one Coordinator
// directly, asserting the per-account ceilings at the coordinator's own
// counters. The full-stack half, which asserts the same ceilings at a
// scripted fake upstream's observations, lives in internal/app/stress_test.go.
//
// Every subtest is wrapped in testsupport.AssertNoGoroutineLeak so a leaked
// worker fails the test rather than silently passing.

// TestStressInFlightCeilingUnderThousandsOfGoroutines drives a thousand
// goroutines through the full reserve-finalize-release cycle on one account
// and asserts the in-flight ceiling at an external observation point: the
// peak concurrent count never exceeds twelve, and the total admitted is
// exactly the rolling-window ceiling, because the clock never advances.
func TestStressInFlightCeilingUnderThousandsOfGoroutines(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c, _ := newReservationTestCoordinator()
		const workers = 1000

		var current, maxConcurrent, total atomic.Int64
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			go func() {
				defer wg.Done()
				for {
					lease, outcome := c.Reserve(catalog.AccountK1)
					switch outcome {
					case Reserved:
						total.Add(1)
						cur := current.Add(1)
						updateMax(&maxConcurrent, cur)
						lease.Finalize()
						current.Add(-1)
						lease.Release()
					case SkippedRateSaturated:
						return
					default:
						runtime.Gosched()
					}
				}
			}()
		}
		wg.Wait()

		if got := maxConcurrent.Load(); got > policy.InFlightAttemptsPerAccount {
			t.Fatalf("max concurrent in-flight = %d, want <= %d", got, policy.InFlightAttemptsPerAccount)
		}
		if got := total.Load(); got != policy.DispatchesPerWindowPerAccount {
			t.Fatalf("total dispatches = %d, want %d", got, policy.DispatchesPerWindowPerAccount)
		}
	})
}

// TestStressRollingWindowNeverExceedsCeiling drives hundreds of goroutines
// through reserve-finalize-release across several rolling windows, with an
// observer sampling the coordinator's internal window count throughout. The
// window (finalized timestamps plus pending reservations) must never exceed
// the per-account ceiling at any observation point.
func TestStressRollingWindowNeverExceedsCeiling(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c, fake := newReservationTestCoordinator()
		const workers = 200
		const rounds = 8

		var maxWindow atomic.Int64
		var stop atomic.Bool
		var observerWg sync.WaitGroup

		observerWg.Add(1)
		go func() {
			defer observerWg.Done()
			for !stop.Load() {
				c.mu.Lock()
				state := &c.accounts[accountIndex(catalog.AccountK1)]
				window := len(state.dispatchTimestamps) + state.pendingReservations
				c.mu.Unlock()
				updateMax(&maxWindow, int64(window))
				runtime.Gosched()
			}
		}()

		for round := 0; round < rounds; round++ {
			var roundWg sync.WaitGroup
			roundWg.Add(workers)
			for i := 0; i < workers; i++ {
				go func() {
					defer roundWg.Done()
					lease, outcome := c.Reserve(catalog.AccountK1)
					if outcome == Reserved {
						lease.Finalize()
						lease.Release()
					}
				}()
			}
			roundWg.Wait()
			fake.AdvanceMonotonic(policy.RollingRateWindow + time.Second)
		}
		stop.Store(true)
		observerWg.Wait()

		if got := maxWindow.Load(); got > policy.DispatchesPerWindowPerAccount {
			t.Fatalf("rolling window peaked at %d, want <= %d", got, policy.DispatchesPerWindowPerAccount)
		}
	})
}

// TestStressConcurrentFinalSlotClaimsAdmitOnlyOne fills every slot but the
// last, then races a thousand goroutines for that final slot. Exactly one
// caller may win it; the rate check and the pending increment are one
// critical section, so a burst cannot claim more slots than exist.
func TestStressConcurrentFinalSlotClaimsAdmitOnlyOne(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c, _ := newReservationTestCoordinator()

		for i := 0; i < policy.DispatchesPerWindowPerAccount-1; i++ {
			lease, outcome := c.Reserve(catalog.AccountK1)
			if outcome != Reserved {
				t.Fatalf("setup reserve %d = %v, want Reserved", i, outcome)
			}
			lease.Finalize()
			lease.Release()
		}

		const contenders = 1000
		var admitted atomic.Int64
		var wg sync.WaitGroup
		wg.Add(contenders)
		for i := 0; i < contenders; i++ {
			go func() {
				defer wg.Done()
				lease, outcome := c.Reserve(catalog.AccountK1)
				if outcome == Reserved {
					admitted.Add(1)
					lease.Finalize()
					lease.Release()
				}
			}()
		}
		wg.Wait()

		if got := admitted.Load(); got != 1 {
			t.Fatalf("final-slot admits = %d, want exactly 1", got)
		}
	})
}

// TestStressCanceledWaitersExitPromptly drives a thousand waiters on one
// token, cancels them all, and joins every one through a WaitGroup. A
// goroutine leak here would leave the WaitGroup permanently un-Done and the
// test would hang past its own deadline instead of passing.
func TestStressCanceledWaitersExitPromptly(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c := testCoordinator()
		const waiters = 1000

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
	})
}

// TestStressNotificationReplacementDoesNotLoseWakeups blocks five hundred
// waiters on one captured token, then fires a single notification. Every
// waiter must observe the token firing, whether it was already blocked or
// had not yet entered Wait when the notification fired.
func TestStressNotificationReplacementDoesNotLoseWakeups(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c := testCoordinator()
		const waiters = 500

		c.mu.Lock()
		token := c.WaitToken()
		c.mu.Unlock()

		var wg sync.WaitGroup
		outcomes := make(chan WaitOutcome, waiters)
		for i := 0; i < waiters; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				outcomes <- c.Wait(context.Background(), token)
			}()
		}

		time.Sleep(20 * time.Millisecond)
		c.IncrementInFlight(catalog.AccountK1)
		c.DecrementInFlight(catalog.AccountK1)

		done := make(chan struct{})
		go func() {
			wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Fatal("not every waiter woke after the notification; possible lost wakeup")
		}
		close(outcomes)

		for outcome := range outcomes {
			if outcome != WaitNotified {
				t.Errorf("outcome = %v, want WaitNotified", outcome)
			}
		}
	})
}

// TestStressNewSessionConcurrentRequestsShareOneAccount drives five hundred
// concurrent first requests for one session and asserts every one selects
// the same initial account, the account the provisional pin was installed
// on. The first request installs the pin atomically with its reservation,
// so concurrent first requests cannot split across accounts.
func TestStressNewSessionConcurrentRequestsShareOneAccount(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
		c, _ := newSelectionTestCoordinator(perm)
		key := testSessionKey('a')

		const requests = 500
		results := make(chan SelectionResult, requests)
		pins := make(chan ProvisionalPin, requests)
		var wg sync.WaitGroup
		for i := 0; i < requests; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				result, pin := c.SelectForNewSession(context.Background(), key)
				if result.Lease != nil {
					// Free the in-flight slot so the ceiling does not
					// serialize the remaining requests; the provisional
					// holder is released separately and stays held.
					result.Lease.Release()
				}
				results <- result
				pins <- pin
			}()
		}
		wg.Wait()
		close(results)
		close(pins)

		for result := range results {
			if result.Outcome != SelectionReserved {
				t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
			}
			if got := result.Lease.Account(); got != catalog.AccountK1 {
				t.Errorf("lease account = %v, want k1 (all requests share the pin's account)", got)
			}
		}
		for pin := range pins {
			if pin.Generation != 1 {
				t.Errorf("generation = %d, want 1", pin.Generation)
			}
		}

		c.mu.Lock()
		entry := c.pins[key]
		c.mu.Unlock()
		if entry.holders != requests {
			t.Errorf("holders = %d, want %d", entry.holders, requests)
		}
	})
}

// TestStressConcurrentPinUpdatesHonorArrivalSequence fires five hundred
// concurrent pin confirmations with distinct arrival sequences and
// alternating accounts, and asserts the surviving pin is the one carrying
// the highest sequence, not whichever update happened to arrive last.
func TestStressConcurrentPinUpdatesHonorArrivalSequence(t *testing.T) {
	testsupport.AssertNoGoroutineLeak(t, func() {
		c := testCoordinator()
		key := testSessionKey('a')

		const updates = 500
		var wg sync.WaitGroup
		for i := 0; i < updates; i++ {
			wg.Add(1)
			go func(seq uint64) {
				defer wg.Done()
				account := catalog.AccountK1
				if seq%2 == 0 {
					account = catalog.AccountK2
				}
				c.ConfirmPin(key, account, seq)
			}(uint64(i + 1))
		}
		wg.Wait()

		// The highest sequence is updates (even), so the surviving pin must
		// be k2 regardless of the order the confirmations actually ran in.
		account, ok := c.PinAccount(key)
		if !ok {
			t.Fatal("PinAccount returned no pin after concurrent updates")
		}
		if account != catalog.AccountK2 {
			t.Errorf("final pin account = %v, want k2 (highest sequence %d is even)", account, updates)
		}
	})
}
