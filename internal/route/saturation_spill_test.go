package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// TestSuccessfulSpillRepins proves the re-pin half of a spill: when a
// session's pinned account is saturated and the selection spills to an
// alternative, a fully successful response on the alternative re-pins the
// session to it. The pin after the request is asserted, not just the
// response, because a spill that relays correctly and re-pins wrongly is
// invisible in the bytes.
func TestSuccessfulSpillRepins(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK2, 1)
	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := runSelectForSession(c, key)
	fake.AdvanceMonotonic(policy.SaturatedPinGrace)

	var result SelectionResult
	select {
	case result = <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the grace elapsed")
	}

	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.SpillFrom != catalog.AccountK2 {
		t.Fatalf("SpillFrom = %v, want k2", result.SpillFrom)
	}
	spilledTo := result.Lease.Account()
	if spilledTo == catalog.AccountK2 {
		t.Fatalf("lease account = %v, want a spill to another account", spilledTo)
	}

	// The caller confirms the successful spill, re-pinning to the account
	// that actually served the response.
	c.ConfirmPin(key, spilledTo, 2)

	if account, ok := c.PinAccount(key); !ok || account != spilledTo {
		t.Errorf("PinAccount() = %v, %v; want %v, true (successful spill re-pins)", account, ok, spilledTo)
	}
}

// TestDeadlineAfterGraceAllowsSpill proves the deadline half of the pin
// stall: a request whose deadline lies beyond the five-second grace is not
// canceled at the grace. The stall ends at the grace and the selection
// spills, rather than the deadline cutting the request short.
func TestDeadlineAfterGraceAllowsSpill(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK2, 1)
	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectForSession(ctx, key)
	}()
	time.Sleep(50 * time.Millisecond)

	// One second before the grace, the stall must still be waiting: the
	// deadline is beyond the grace, so it cannot have canceled the request.
	fake.AdvanceMonotonic(policy.SaturatedPinGrace - time.Second)
	select {
	case result := <-resultCh:
		t.Fatalf("SelectForSession returned before the grace elapsed: %+v", result)
	case <-time.After(50 * time.Millisecond):
	}

	// At the grace the stall ends and the selection spills.
	fake.AdvanceMonotonic(time.Second)
	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved (deadline beyond grace allows spill)", result.Outcome)
		}
		if result.SpillFrom != catalog.AccountK2 {
			t.Errorf("SpillFrom = %v, want k2", result.SpillFrom)
		}
		if got := result.Lease.Account(); got == catalog.AccountK2 {
			t.Errorf("lease account = %v, want a spill to another account", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the grace elapsed")
	}
}
