package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestSelectExplicitReservesNamedAccount(t *testing.T) {
	c, _ := newRateTestCoordinator()

	result := c.SelectExplicit(context.Background(), catalog.AccountK2)
	if result.Outcome != SelectionReserved {
		t.Fatalf("Outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("Lease is nil")
	}
	if got := result.Lease.Account(); got != catalog.AccountK2 {
		t.Errorf("Lease.Account() = %v, want k2", got)
	}
}

func TestSelectExplicitDisabledReturnsImmediately(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].health = HealthDisabled
	c.mu.Unlock()

	start := time.Now()
	result := c.SelectExplicit(context.Background(), catalog.AccountK1)
	elapsed := time.Since(start)

	if result.Outcome != SelectionAllDisabled {
		t.Fatalf("Outcome = %v, want SelectionAllDisabled", result.Outcome)
	}
	if result.Lease != nil {
		t.Fatal("SelectExplicit returned a lease for a disabled account")
	}
	if elapsed >= time.Second {
		t.Errorf("disabled SelectExplicit took %v, want an immediate return with no wait", elapsed)
	}
}

// TestSelectExplicitNeverSpills proves the defining property: a saturated
// named account is waited on and then timed out, never spilled to another
// account. Every skip the phase records names the requested account.
func TestSelectExplicitNeverSpills(t *testing.T) {
	c, fake := newRateTestCoordinator()

	for i := 0; i < policy.DispatchesPerWindowPerAccount; i++ {
		reserveAndFinalize(t, c, catalog.AccountK1)
	}

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectExplicit(context.Background(), catalog.AccountK1)
	}()

	time.Sleep(20 * time.Millisecond)
	fake.AdvanceMonotonic(policy.MaxAccountAcquisitionTime)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionCapacityTimeout {
			t.Fatalf("Outcome = %v, want SelectionCapacityTimeout", result.Outcome)
		}
		if result.Lease != nil {
			t.Fatal("SelectExplicit returned a lease after the ceiling elapsed")
		}
		for _, skip := range result.Skips {
			if skip.Account != catalog.AccountK1 {
				t.Errorf("skip account = %v, want k1 only (never spills)", skip.Account)
			}
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectExplicit did not return after the ceiling elapsed")
	}
}

// TestSelectExplicitRespectsInFlightCeiling proves the explicit path gets
// no exception to the in-flight admission rule: a saturated account is
// waited on and timed out, not bypassed.
func TestSelectExplicitRespectsInFlightCeiling(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectExplicit(context.Background(), catalog.AccountK1)
	}()

	time.Sleep(20 * time.Millisecond)
	fake.AdvanceMonotonic(policy.MaxAccountAcquisitionTime)

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionCapacityTimeout {
			t.Fatalf("Outcome = %v, want SelectionCapacityTimeout", result.Outcome)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectExplicit did not return after the ceiling elapsed")
	}
}

// TestSelectExplicitWakesOnCapacityRelease proves the explicit path rechecks
// the named account after a wake and reserves it once capacity returns.
func TestSelectExplicitWakesOnCapacityRelease(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectExplicit(context.Background(), catalog.AccountK1)
	}()

	time.Sleep(20 * time.Millisecond)
	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].inFlight--
	c.notifyLocked()
	c.mu.Unlock()

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("Outcome = %v, want SelectionReserved", result.Outcome)
		}
		if got := result.Lease.Account(); got != catalog.AccountK1 {
			t.Errorf("Lease.Account() = %v, want k1", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectExplicit did not return after capacity was released")
	}
}

func TestEarliestReopenForNamedAccount(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline = fake.MonotonicNow() + 30*time.Second
	c.accounts[accountIndex(catalog.AccountK2)].rateGateDeadline = fake.MonotonicNow() + 10*time.Second
	c.mu.Unlock()

	reopen, ok := c.EarliestReopenFor(catalog.AccountK1)
	if !ok {
		t.Fatal("EarliestReopenFor(k1) = false, want true")
	}
	// K1's own gate is 30s out; K2's earlier gate must not leak in.
	if want := wallAt(30); !reopen.Equal(want) {
		t.Errorf("EarliestReopenFor(k1) = %v, want %v (k1's own gate, not k2's)", reopen, want)
	}
}

func TestEarliestReopenForDisabledAccount(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].health = HealthDisabled
	c.mu.Unlock()

	if _, ok := c.EarliestReopenFor(catalog.AccountK1); ok {
		t.Fatal("EarliestReopenFor(k1) = true, want false for a disabled account")
	}
}
