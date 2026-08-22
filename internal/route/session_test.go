package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// newSessionTestCoordinator returns a coordinator whose fake clock has not
// advanced past the post-start blackout, for the pin-lifecycle tests that
// never touch admission.
func newSessionTestCoordinator() (*Coordinator, *testsupport.FakeClock) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	c := NewCoordinator(testKeys(), fake, clock.RandomPermutationSource{})
	return c, fake
}

// testSessionKey builds a distinct, fixed session key from a marker byte.
func testSessionKey(marker byte) SessionKey {
	var key SessionKey
	key[0] = 1
	key[1] = marker
	return key
}

func TestConfirmPinSetsAccountExpiryAndSequence(t *testing.T) {
	c, fake := newSessionTestCoordinator()
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK2, 1)

	account, ok := c.PinAccount(key)
	if !ok {
		t.Fatal("PinAccount() = false, want a live pin after ConfirmPin")
	}
	if account != catalog.AccountK2 {
		t.Errorf("PinAccount() = %v, want k2", account)
	}

	c.mu.Lock()
	pin := c.pins[key]
	c.mu.Unlock()
	if pin.state != PinConfirmed {
		t.Errorf("state = %v, want PinConfirmed", pin.state)
	}
	if pin.sequence != 1 {
		t.Errorf("sequence = %d, want 1", pin.sequence)
	}
	wantExpiry := fake.WallNow().Add(policy.SessionAffinityTTL)
	if !pin.expiry.Equal(wantExpiry) {
		t.Errorf("expiry = %v, want %v (wall now + TTL)", pin.expiry, wantExpiry)
	}
}

func TestConfirmPinRefusesStaleSequence(t *testing.T) {
	c, _ := newSessionTestCoordinator()
	key := testSessionKey('a')

	// The newer request (sequence 2) completes first.
	c.ConfirmPin(key, catalog.AccountK1, 2)
	// The older request (sequence 1) completes later and must not overwrite.
	c.ConfirmPin(key, catalog.AccountK2, 1)

	account, ok := c.PinAccount(key)
	if !ok {
		t.Fatal("PinAccount() = false, want a live pin")
	}
	if account != catalog.AccountK1 {
		t.Errorf("PinAccount() = %v, want k1 (stale completion must not overwrite)", account)
	}
}

func TestConfirmPinAcceptsNewerSequence(t *testing.T) {
	c, _ := newSessionTestCoordinator()
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK1, 1)
	c.ConfirmPin(key, catalog.AccountK2, 2)

	account, ok := c.PinAccount(key)
	if !ok {
		t.Fatal("PinAccount() = false, want a live pin")
	}
	if account != catalog.AccountK2 {
		t.Errorf("PinAccount() = %v, want k2 (newer completion wins)", account)
	}
}

func TestConfirmPinPreservesArrivalCounter(t *testing.T) {
	c, _ := newSessionTestCoordinator()
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK1, 1)
	// Two more requests arrive while the pin is live.
	if got := c.NextArrivalSequence(key); got != 2 {
		t.Errorf("NextArrivalSequence() = %d, want 2", got)
	}
	if got := c.NextArrivalSequence(key); got != 3 {
		t.Errorf("NextArrivalSequence() = %d, want 3", got)
	}

	// A re-pin must not rewind the arrival counter.
	c.ConfirmPin(key, catalog.AccountK2, 3)
	if got := c.NextArrivalSequence(key); got != 4 {
		t.Errorf("NextArrivalSequence() = %d, want 4 (counter must survive re-pin)", got)
	}
}

func TestPinAccountLazyExpiry(t *testing.T) {
	c, fake := newSessionTestCoordinator()
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK1, 1)
	fake.AdvanceWall(policy.SessionAffinityTTL)

	if _, ok := c.PinAccount(key); ok {
		t.Error("PinAccount() = true, want false after the wall TTL elapsed")
	}
	c.mu.Lock()
	_, exists := c.pins[key]
	c.mu.Unlock()
	if exists {
		t.Error("expired pin was not removed lazily")
	}
}

func TestRemovePin(t *testing.T) {
	c, _ := newSessionTestCoordinator()
	key := testSessionKey('a')

	c.ConfirmPin(key, catalog.AccountK1, 1)
	c.RemovePin(key)

	if _, ok := c.PinAccount(key); ok {
		t.Error("PinAccount() = true, want false after RemovePin")
	}
}

func TestNextArrivalSequenceStartsAtOne(t *testing.T) {
	c, _ := newSessionTestCoordinator()
	key := testSessionKey('a')

	if got := c.NextArrivalSequence(key); got != 1 {
		t.Errorf("NextArrivalSequence() = %d, want 1 for a new session", got)
	}
}

// TestConfirmPinExpiryIsWallClock proves the affinity hour is dated from
// wall-clock completion and from no earlier boundary. A request that runs
// for nine synthetic monotonic minutes must still date its hour from the
// wall instant at which it completed, so a monotonic advance of a full hour
// leaves the pin live while a wall advance of the same length expires it.
func TestConfirmPinExpiryIsWallClock(t *testing.T) {
	c, fake := newSessionTestCoordinator()
	key := testSessionKey('a')

	fake.AdvanceMonotonic(9 * time.Minute)
	c.ConfirmPin(key, catalog.AccountK1, 1)

	fake.AdvanceMonotonic(policy.SessionAffinityTTL)
	if _, ok := c.PinAccount(key); !ok {
		t.Fatal("pin expired on the monotonic clock; expiry must be wall-clock")
	}

	fake.AdvanceWall(policy.SessionAffinityTTL)
	if _, ok := c.PinAccount(key); ok {
		t.Error("pin still live after the wall TTL elapsed")
	}
}

func TestSelectForSessionTriesPinFirst(t *testing.T) {
	// The permutation names k1 first, but the pin is on k2, so a correct
	// pin-first selection must reserve k2 without shuffling.
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	result := c.SelectForSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForSession returned a nil lease")
	}
	if got := result.Lease.Account(); got != catalog.AccountK2 {
		t.Errorf("lease account = %v, want k2 (the pin, not the permutation's first)", got)
	}
}

func TestSelectForSessionRemovesDisabledPin(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].health = HealthDisabled
	c.mu.Unlock()

	result := c.SelectForSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForSession returned a nil lease")
	}
	if got := result.Lease.Account(); got == catalog.AccountK2 {
		t.Errorf("lease account = %v, want a non-disabled account", got)
	}
	if result.SpillFrom != catalog.AccountK2 {
		t.Errorf("SpillFrom = %v, want k2 (the disabled pin)", result.SpillFrom)
	}
	if _, ok := c.PinAccount(key); ok {
		t.Error("disabled pin was not removed")
	}
}

func TestSelectForSessionFallsThroughWhenSaturated(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')
	c.ConfirmPin(key, catalog.AccountK2, 1)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK2)].inFlight = policy.InFlightAttemptsPerAccount
	c.mu.Unlock()

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.SelectForSession(context.Background(), key)
	}()

	// The stall waits on notification until the grace elapses. Give the
	// selection goroutine time to reach the wait, then advance the clock
	// past the grace so the pin is given up and the selection spills.
	time.Sleep(50 * time.Millisecond)
	fake.AdvanceMonotonic(policy.SaturatedPinGrace)

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
		// A saturated pin is not disabled, so it must remain.
		if account, ok := c.PinAccount(key); !ok || account != catalog.AccountK2 {
			t.Errorf("PinAccount() = %v, %v; want k2, true (saturated pin stays)", account, ok)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SelectForSession did not return after the grace elapsed")
	}
}

func TestSelectForSessionWithoutPinFallsThrough(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	result := c.SelectForSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForSession returned a nil lease")
	}
	if got := result.Lease.Account(); got != catalog.AccountK1 {
		t.Errorf("lease account = %v, want k1 (permutation's first, no pin)", got)
	}
}
