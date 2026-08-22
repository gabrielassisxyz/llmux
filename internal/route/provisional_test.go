package route

import (
	"context"
	"sync"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

func TestSelectForNewSessionInstallsProvisionalPin(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	result, pin := c.SelectForNewSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("SelectForNewSession returned a nil lease")
	}
	if pin.Generation != 1 {
		t.Errorf("generation = %d, want 1", pin.Generation)
	}
	if pin.Sequence != 1 {
		t.Errorf("sequence = %d, want 1", pin.Sequence)
	}
	if pin.Account != catalog.AccountK1 {
		t.Errorf("account = %v, want k1", pin.Account)
	}

	// A provisional pin is not a live pin: PinAccount must not return it.
	if _, ok := c.PinAccount(key); ok {
		t.Error("PinAccount returned a provisional pin")
	}

	c.mu.Lock()
	entry := c.pins[key]
	c.mu.Unlock()
	if entry.state != PinProvisional {
		t.Errorf("state = %v, want PinProvisional", entry.state)
	}
	if entry.holders != 1 {
		t.Errorf("holders = %d, want 1", entry.holders)
	}
	if entry.generation != 1 {
		t.Errorf("generation = %d, want 1", entry.generation)
	}
}

func TestSelectForNewSessionConcurrentRequestsSharePin(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	const requests = 3
	results := make(chan SelectionResult, requests)
	pins := make(chan ProvisionalPin, requests)
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			result, pin := c.SelectForNewSession(context.Background(), key)
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
}

func TestReleaseProvisionalHolderRemovesPinOnLastHolder(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, pin1 := c.SelectForNewSession(context.Background(), key)
	_, pin2 := c.SelectForNewSession(context.Background(), key)

	c.ReleaseProvisionalHolder(key, pin1.Generation)
	c.mu.Lock()
	_, exists := c.pins[key]
	c.mu.Unlock()
	if !exists {
		t.Fatal("pin removed while a holder remained")
	}

	c.ReleaseProvisionalHolder(key, pin2.Generation)
	c.mu.Lock()
	_, exists = c.pins[key]
	c.mu.Unlock()
	if exists {
		t.Error("pin not removed after the last holder released")
	}
}

func TestReleaseProvisionalHolderIgnoresStaleGeneration(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, pin := c.SelectForNewSession(context.Background(), key)

	c.ReleaseProvisionalHolder(key, pin.Generation+1)

	c.mu.Lock()
	entry, exists := c.pins[key]
	c.mu.Unlock()
	if !exists {
		t.Fatal("pin removed by a stale-generation release")
	}
	if entry.holders != 1 {
		t.Errorf("holders = %d, want 1 (stale release must not decrement)", entry.holders)
	}
}

func TestReleaseProvisionalHolderIgnoresConfirmedPin(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, pin := c.SelectForNewSession(context.Background(), key)
	c.ConfirmPin(key, pin.Account, pin.Sequence)

	c.ReleaseProvisionalHolder(key, pin.Generation)

	if account, ok := c.PinAccount(key); !ok || account != pin.Account {
		t.Errorf("PinAccount = %v, %v; want %v, true (confirmed pin must survive)", account, ok, pin.Account)
	}
}

func TestFailedFirstRequestLeavesNoPinBehind(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, pin := c.SelectForNewSession(context.Background(), key)
	c.ReleaseProvisionalHolder(key, pin.Generation)

	c.mu.Lock()
	_, exists := c.pins[key]
	c.mu.Unlock()
	if exists {
		t.Error("failed first request left a pin behind")
	}
}

// TestProvisionalPinSurvivesWallClock proves a provisional pin has no
// wall-clock TTL: it lives until the last holder releases, not until the
// affinity hour elapses.
func TestProvisionalPinSurvivesWallClock(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, _ = c.SelectForNewSession(context.Background(), key)

	fake.AdvanceWall(2 * policy.SessionAffinityTTL)

	c.mu.Lock()
	_, exists := c.pins[key]
	c.mu.Unlock()
	if !exists {
		t.Error("provisional pin expired on the wall clock; it must live until the last holder releases")
	}
}
