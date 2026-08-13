package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// freshCoordinator returns a coordinator whose clock is still at its
// monotonic zero point, unlike newRateTestCoordinator, which advances past
// the blackout so rate-window tests are not incidentally testing it too.
func freshCoordinator() (*Coordinator, *testsupport.FakeClock) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	return NewCoordinator(testKeys(), fake), fake
}

func TestNoDispatchAdmittedDuringTheBlackout(t *testing.T) {
	c, fake := freshCoordinator()

	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot at process start = true, want false (blackout)")
	}

	fake.AdvanceMonotonic(policy.PostStartDispatchBlackout - time.Nanosecond)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot one nanosecond before the blackout ends = true, want false")
	}
}

func TestFirstAdmissionAfterTheBlackoutSucceeds(t *testing.T) {
	c, fake := freshCoordinator()

	fake.AdvanceMonotonic(policy.PostStartDispatchBlackout)
	if !c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("ReserveRateSlot exactly at the blackout boundary = false, want true")
	}
}

func TestBlackoutBlocksEveryAccountAtOnce(t *testing.T) {
	c, _ := freshCoordinator()

	for _, account := range []catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3} {
		if c.ReserveRateSlot(account) {
			t.Errorf("ReserveRateSlot(%s) during blackout = true, want false", account)
		}
	}
}

// TestBlackoutIsImmuneToWallClockSteps proves the blackout reads only the
// monotonic clock: stepping wall time forward or backward, without
// advancing monotonic time at all, must change nothing about whether the
// blackout still holds.
func TestBlackoutIsImmuneToWallClockSteps(t *testing.T) {
	c, fake := freshCoordinator()

	fake.AdvanceWall(24 * time.Hour)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("a forward wall-clock step lifted the blackout, want it to remain in effect")
	}

	fake.AdvanceWall(-48 * time.Hour)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("a backward wall-clock step lifted the blackout, want it to remain in effect")
	}
}

// TestRollingWindowContentsAreImmuneToWallClockSteps extends the same
// proof to the rate window itself, not just the blackout: a saturated
// account must stay saturated, and an unsaturated one must not become
// artificially saturated, across wall-clock steps that leave monotonic
// time untouched.
func TestRollingWindowContentsAreImmuneToWallClockSteps(t *testing.T) {
	c, fake := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	fake.AdvanceWall(24 * time.Hour)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("a forward wall-clock step opened capacity on a saturated account, want it to remain saturated")
	}

	fake.AdvanceWall(-48 * time.Hour)
	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("a backward wall-clock step opened capacity on a saturated account, want it to remain saturated")
	}
}

// TestEmptyRateStateDoesNotShortenTheBlackout proves the case the bead's
// own background section calls out by name: an account with no recorded
// admissions looks like a fresh install with nothing to be conservative
// about, but it is also what a cold-archive restart hands a process whose
// real dispatches are minutes old in a database moved aside. The blackout
// must hold regardless of which of those two situations produced the
// empty state, since nothing observable here distinguishes them.
func TestEmptyRateStateDoesNotShortenTheBlackout(t *testing.T) {
	c, _ := freshCoordinator()

	c.mu.Lock()
	state := &c.accounts[accountIndex(catalog.AccountK1)]
	state.dispatchTimestamps = nil
	state.pendingReservations = 0
	c.mu.Unlock()

	if c.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("an account with empty rate state was admitted during the blackout, want false regardless of prior state")
	}
}

// TestRestartAcrossASaturatedWindowNeverExceedsTheCeiling simulates the
// scenario the blackout exists for: a process saturates an account and
// then crashes, a new process starts with the wall clock stepped forward
// and backward across the boundary, and the new process's own coordinator
// (a separate instance, since nothing survives a crash) is proven blacked
// out for its own full window regardless. The two processes never share a
// Coordinator, so there is no rate state for the second to inherit even in
// principle; what this test proves is that the second process's blackout
// holds under exactly the wall-clock manipulation a real restart involves.
func TestRestartAcrossASaturatedWindowNeverExceedsTheCeiling(t *testing.T) {
	crashed, _ := newRateTestCoordinator()
	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, crashed, catalog.AccountK1)
	}
	if crashed.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("test setup: the crashed process's account should be saturated")
	}

	restarted, restartedClock := freshCoordinator()
	restartedClock.AdvanceWall(6 * time.Hour)
	if restarted.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("restarted process admitted a dispatch immediately after a forward wall-clock step, want blackout to hold")
	}

	restartedClock.AdvanceWall(-12 * time.Hour)
	if restarted.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("restarted process admitted a dispatch after a backward wall-clock step, want blackout to hold")
	}

	restartedClock.AdvanceMonotonic(policy.PostStartDispatchBlackout)
	if !restarted.ReserveRateSlot(catalog.AccountK1) {
		t.Fatal("restarted process refused a dispatch once its own full blackout window elapsed, want it admitted")
	}
}
