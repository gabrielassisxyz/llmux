package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// saturateRPM fills account's rolling window with finalized dispatches so
// the account is RPM-saturated until the oldest timestamp expires.
func saturateRPM(t *testing.T, c *Coordinator, account catalog.Account) {
	t.Helper()
	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, account)
	}
}

// enterCooldown applies three 429s so the cooldown circuit opens and the
// gate deadline is floored at one rolling window from the third 429.
func enterCooldown(t *testing.T, c *Coordinator, account catalog.Account) {
	t.Helper()
	for i := 0; i < 3; i++ {
		c.Apply429(account, "1", time.Time{}, false)
	}
}

// runSelectForSession starts SelectForSession in a goroutine and returns
// the result channel, after giving the goroutine time to reach its first
// wait so a subsequent clock advance fires the timer it is blocked on.
func runSelectForSession(c *Coordinator, key SessionKey) chan SelectionResult {
	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectForSession(context.Background(), key)
	}()
	time.Sleep(50 * time.Millisecond)
	return resultCh
}

func TestPinFreesInsideGraceStaysPinned(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := runSelectForSession(c, key)

	// Free the pin inside the grace: the notification must wake the stall
	// and the pin must be reserved without spilling.
	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight--
	c.notifyLocked()
	c.mu.Unlock()

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if result.Lease == nil {
			t.Fatal("SelectForSession returned a nil lease")
		}
		if got := result.Lease.Account(); got != catalog.AccountK2 {
			t.Errorf("lease account = %v, want k2 (the pin freed inside grace)", got)
		}
		if result.SpillFrom != "" {
			t.Errorf("SpillFrom = %v, want empty (no spill)", result.SpillFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the pin freed")
	}
}

func TestRPMReopenInsideGraceWaitsAndStaysPinned(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	saturateRPM(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	// The oldest timestamp reopens in four seconds.
	fake.AdvanceMonotonic(policy.RollingRateWindow - 4*time.Second)

	resultCh := runSelectForSession(c, key)

	// The stall must wait the known four-second reopening, then reserve the
	// pin rather than spilling.
	fake.AdvanceMonotonic(4 * time.Second)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if result.Lease == nil {
			t.Fatal("SelectForSession returned a nil lease")
		}
		if got := result.Lease.Account(); got != catalog.AccountK2 {
			t.Errorf("lease account = %v, want k2 (RPM reopened inside grace)", got)
		}
		if result.SpillFrom != "" {
			t.Errorf("SpillFrom = %v, want empty (no spill)", result.SpillFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the RPM reopening")
	}
}

func TestRPMReopenBeyondGraceSpillsImmediately(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	saturateRPM(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	// The oldest timestamp reopens in 45 seconds, well beyond the grace.
	fake.AdvanceMonotonic(policy.RollingRateWindow - 45*time.Second)

	// The stall must not burn the grace: it spills immediately.
	result := c.SelectForSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForSession returned a nil lease")
	}
	if got := result.Lease.Account(); got == catalog.AccountK2 {
		t.Errorf("lease account = %v, want a spill to another account", got)
	}
	if result.SpillFrom != catalog.AccountK2 {
		t.Errorf("SpillFrom = %v, want k2 (the RPM-saturated pin)", result.SpillFrom)
	}
}

func TestCooldownReopenInsideGraceWaitsAndStaysPinned(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	enterCooldown(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	// The cooldown gate reopens in four seconds.
	fake.AdvanceMonotonic(policy.RollingRateWindow - 4*time.Second)

	resultCh := runSelectForSession(c, key)

	fake.AdvanceMonotonic(4 * time.Second)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if result.Lease == nil {
			t.Fatal("SelectForSession returned a nil lease")
		}
		if got := result.Lease.Account(); got != catalog.AccountK2 {
			t.Errorf("lease account = %v, want k2 (cooldown reopened inside grace)", got)
		}
		if result.SpillFrom != "" {
			t.Errorf("SpillFrom = %v, want empty (no spill)", result.SpillFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the cooldown reopening")
	}
}

func TestCooldownReopenBeyondGraceSpillsImmediately(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	enterCooldown(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	// The cooldown gate reopens in 45 seconds, well beyond the grace.
	fake.AdvanceMonotonic(policy.RollingRateWindow - 45*time.Second)

	result := c.SelectForSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForSession returned a nil lease")
	}
	if got := result.Lease.Account(); got == catalog.AccountK2 {
		t.Errorf("lease account = %v, want a spill to another account", got)
	}
	if result.SpillFrom != catalog.AccountK2 {
		t.Errorf("SpillFrom = %v, want k2 (the cooling pin)", result.SpillFrom)
	}
}

// TestMultipleDeterministicBlockersUseLatestReopen proves the stall waits
// for the latest of the coexisting deterministic reopenings, because every
// blocker must clear before the account is eligible again.
func TestMultipleDeterministicBlockersUseLatestReopen(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	// RPM-saturated with the oldest timestamp reopening in four seconds.
	saturateRPM(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	fake.AdvanceMonotonic(policy.RollingRateWindow - 4*time.Second)

	// A single 429 opens a rate gate that reopens in two seconds, earlier
	// than the RPM reopening.
	c.Apply429(catalog.AccountK2, "2", time.Time{}, false)

	resultCh := runSelectForSession(c, key)

	// Advance past the earlier gate reopening: the stall must still be
	// waiting for the later RPM reopening, so the pin is not yet reserved.
	fake.AdvanceMonotonic(2 * time.Second)
	select {
	case result := <-resultCh:
		t.Fatalf("SelectForSession returned early at the gate reopening: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}

	// Advance to the latest reopening: the pin must now be reserved.
	fake.AdvanceMonotonic(2 * time.Second)
	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if result.Lease == nil {
			t.Fatal("SelectForSession returned a nil lease")
		}
		if got := result.Lease.Account(); got != catalog.AccountK2 {
			t.Errorf("lease account = %v, want k2 (all blockers cleared)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the latest reopening")
	}
}

// TestDeterministicAndInFlightBlockersSpillAtGrace proves that when a
// deterministic blocker clears inside the grace but in-flight saturation
// persists, the stall keeps waiting on notification until the grace, then
// spills.
func TestDeterministicAndInFlightBlockersSpillAtGrace(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	saturateRPM(t, c, catalog.AccountK2)
	c.ConfirmPin(key, catalog.AccountK2, 1)
	fake.AdvanceMonotonic(policy.RollingRateWindow - 4*time.Second)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := runSelectForSession(c, key)

	// The RPM reopening clears at four seconds, but in-flight saturation
	// remains, so the stall must keep waiting until the grace.
	fake.AdvanceMonotonic(4 * time.Second)
	time.Sleep(50 * time.Millisecond)
	fake.AdvanceMonotonic(time.Second)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if result.Lease == nil {
			t.Fatal("SelectForSession returned a nil lease")
		}
		if got := result.Lease.Account(); got == catalog.AccountK2 {
			t.Errorf("lease account = %v, want a spill to another account", got)
		}
		if result.SpillFrom != catalog.AccountK2 {
			t.Errorf("SpillFrom = %v, want k2 (the saturated pin)", result.SpillFrom)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the grace elapsed")
	}
}

func TestDeadlineDuringGraceCancels(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	result := c.SelectForSession(ctx, key)
	if result.Outcome != SelectionCanceled {
		t.Fatalf("outcome = %v, want SelectionCanceled", result.Outcome)
	}
	if result.Lease != nil {
		t.Fatal("SelectForSession returned a lease for a canceled context")
	}
}

func TestAllAccountsBlockedCapacityTimeout(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	// Saturate every account's in-flight ceiling, including the pin.
	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].inFlight = policy.InFlightAttemptsPerAccount
	}
	c.mu.Unlock()

	resultCh := runSelectForSession(c, key)

	// The pin stall ends at the grace, then the alternative selection waits
	// for any account until the acquisition ceiling.
	fake.AdvanceMonotonic(policy.SaturatedPinGrace)
	time.Sleep(50 * time.Millisecond)
	fake.AdvanceMonotonic(policy.MaxAccountAcquisitionTime)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionCapacityTimeout {
			t.Fatalf("outcome = %v, want SelectionCapacityTimeout", result.Outcome)
		}
		if result.Lease != nil {
			t.Fatal("SelectForSession returned a lease for an all-saturated state")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the acquisition ceiling")
	}
}
