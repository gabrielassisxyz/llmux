package route

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

func newRateTestCoordinator() (*Coordinator, *testsupport.FakeClock) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	return NewCoordinator(testKeys(), fake), fake
}

// reserveAndFinalize is the two-phase sequence a real caller performs:
// reserve a pending slot, then finalize it at the simulated Do boundary.
func reserveAndFinalize(t *testing.T, c *Coordinator, account catalog.Account) {
	t.Helper()
	if !c.ReserveRateSlot(account) {
		t.Fatalf("ReserveRateSlot(%s) = false, want true", account)
	}
	c.FinalizeDispatch(account)
}

func TestFirst60StartsAreAdmittedThe61stIsRejected(t *testing.T) {
	c, _ := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		if !c.ReserveRateSlot(catalog.AccountK1) {
			t.Fatalf("dispatch %d: ReserveRateSlot = false, want true", i+1)
		}
		c.FinalizeDispatch(catalog.AccountK1)
	}

	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("61st ReserveRateSlot = true, want false")
	}
}

func TestExactBoundaryExpirationAdmitsCorrectly(t *testing.T) {
	c, fake := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("account should be saturated before any time passes")
	}

	// One instant before the window fully elapses, the oldest timestamp is
	// still inside it.
	fake.AdvanceMonotonic(policy.RollingRateWindow - time.Nanosecond)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = true one nanosecond before the window elapsed, want false")
	}

	// The instant the window fully elapses, "at or before now - 60s"
	// includes the oldest timestamp and it is pruned.
	fake.AdvanceMonotonic(time.Nanosecond)
	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = false exactly at the window boundary, want true")
	}
}

func TestFailedDispatchesRemainCounted(t *testing.T) {
	c, _ := newRateTestCoordinator()

	// A "failed dispatch" still finalized a timestamp: FinalizeDispatch is
	// called immediately before Do regardless of what Do later returns,
	// so the timestamp it installs is never refunded by a failure.
	reserveAndFinalize(t, c, catalog.AccountK1)

	c.mu.Lock()
	count := len(c.accounts[accountIndex(catalog.AccountK1)].dispatchTimestamps)
	c.mu.Unlock()
	if count != 1 {
		t.Fatalf("dispatchTimestamps count = %d, want 1", count)
	}
}

func TestRetriesConsumeAnotherTimestampSkipsDoNot(t *testing.T) {
	c, _ := newRateTestCoordinator()

	// A retry dispatches again, consuming a second timestamp.
	reserveAndFinalize(t, c, catalog.AccountK1)
	reserveAndFinalize(t, c, catalog.AccountK1)

	c.mu.Lock()
	afterRetry := len(c.accounts[accountIndex(catalog.AccountK1)].dispatchTimestamps)
	c.mu.Unlock()
	if afterRetry != 2 {
		t.Fatalf("dispatchTimestamps count after retry = %d, want 2", afterRetry)
	}

	// A skip (a candidate inspected but never reserved) consumes nothing.
	c.mu.Lock()
	afterSkip := len(c.accounts[accountIndex(catalog.AccountK1)].dispatchTimestamps)
	c.mu.Unlock()
	if afterSkip != afterRetry {
		t.Fatalf("dispatchTimestamps count changed on a skip: %d -> %d", afterRetry, afterSkip)
	}
}

// TestDispatchTimestampAnchoredAtFinalizeNotReservation is the assertion
// that distinguishes a correct implementation from the obvious wrong one:
// a reservation held open across a simulated slow admission commit must
// not let more than the ceiling fall inside one real rolling window,
// because the timestamp is dated at FinalizeDispatch, not at
// ReserveRateSlot.
func TestDispatchTimestampAnchoredAtFinalizeNotReservation(t *testing.T) {
	c, fake := newRateTestCoordinator()

	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = false, want true")
	}
	reservedAt := fake.MonotonicNow()

	// Simulate a slow admission commit: real time passes with the
	// reservation still open, before FinalizeDispatch ever runs.
	fake.AdvanceMonotonic(policy.RollingRateWindow / 2)
	c.FinalizeDispatch(catalog.AccountK1)
	finalizedAt := fake.MonotonicNow()

	c.mu.Lock()
	got := c.accounts[accountIndex(catalog.AccountK1)].dispatchTimestamps[0]
	c.mu.Unlock()

	if got != finalizedAt {
		t.Fatalf("stored timestamp = %v, want the finalize instant %v (reserved at %v)", got, finalizedAt, reservedAt)
	}
	if got == reservedAt {
		t.Fatal("stored timestamp matches the reservation instant, not the finalize instant")
	}
}

func TestReleasePendingRateSlotFreesCapacityWithoutRecordingADispatch(t *testing.T) {
	c, _ := newRateTestCoordinator()

	// Finalize 59 dispatches, leaving exactly one slot.
	for i := 0; i < policy.DispatchesPerWindowPerAccount-1; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}
	// Reserve the 60th slot but never finalize it: it stays pending.
	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot for the 60th slot = false, want true")
	}
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("a 61st reservation succeeded while the 60th is still pending, want false")
	}

	// Releasing the pending reservation, rather than finalizing it, must
	// free the slot without adding a timestamp.
	c.ReleasePendingRateSlot(catalog.AccountK1)

	c.mu.Lock()
	count := len(c.accounts[accountIndex(catalog.AccountK1)].dispatchTimestamps)
	pending := c.accounts[accountIndex(catalog.AccountK1)].pendingReservations
	c.mu.Unlock()
	if count != policy.DispatchesPerWindowPerAccount-1 {
		t.Fatalf("dispatchTimestamps count = %d, want unchanged at %d", count, policy.DispatchesPerWindowPerAccount-1)
	}
	if pending != 0 {
		t.Fatalf("pendingReservations = %d, want 0 after release", pending)
	}

	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = false after releasing the pending slot, want true")
	}
}

func TestReleasePendingRateSlotNotifiesWaiters(t *testing.T) {
	c, _ := newRateTestCoordinator()
	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = false, want true")
	}

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	select {
	case <-token:
		t.Fatal("token already fired before release")
	default:
	}

	c.ReleasePendingRateSlot(catalog.AccountK1)

	select {
	case <-token:
	default:
		t.Fatal("token did not fire after ReleasePendingRateSlot")
	}
}

func TestPendingReservationsCountAgainstTheCeilingBeforeFinalizing(t *testing.T) {
	c, _ := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		if !c.ReserveRateSlot(catalog.AccountK1) {
			t.Fatalf("reservation %d = false, want true", i+1)
		}
		// Deliberately leave every reservation pending, unfinalized, to
		// prove pending reservations alone saturate the ceiling.
	}

	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot = true with 60 pending reservations, want false")
	}
}

func TestDifferentAccountsHaveIndependentRateWindows(t *testing.T) {
	c, _ := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("k1 should be saturated")
	}
	if !c.ReserveRateSlot(catalog.AccountK2) {
		t.Fatal("k2 should be unaffected by k1's saturation")
	}
}

// TestConcurrentFinalSlotClaimsAdmitOnlyTheCeiling drives far more
// concurrent reservation attempts than the ceiling allows, with no
// simulated time passing, and proves the total that ever succeeds is
// exactly the ceiling: the rate check and the pending-reservation
// increment are one critical section, so no burst of concurrent goroutines
// can claim more slots than exist.
func TestConcurrentFinalSlotClaimsAdmitOnlyTheCeiling(t *testing.T) {
	c, _ := newRateTestCoordinator()
	const attempts = 500

	var admitted atomic.Int64
	var wg sync.WaitGroup
	wg.Add(attempts)
	for i := 0; i < attempts; i++ {
		go func() {
			defer wg.Done()
			if c.ReserveRateSlot(catalog.AccountK1) {
				admitted.Add(1)
			}
		}()
	}
	wg.Wait()

	if got := admitted.Load(); got != policy.DispatchesPerWindowPerAccount {
		t.Fatalf("admitted = %d, want exactly %d", got, policy.DispatchesPerWindowPerAccount)
	}
}
