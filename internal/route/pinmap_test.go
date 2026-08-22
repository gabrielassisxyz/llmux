package route

import (
	"context"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// testSessionKeyN builds a distinct session key from an integer, so a test
// can fill the pin map with unique keys.
func testSessionKeyN(n int) SessionKey {
	var key SessionKey
	key[0] = 1
	key[1] = byte(n)
	key[2] = byte(n >> 8)
	return key
}

func TestPinMapCeilingRefusesNewSession(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKeyN(policy.LiveSessionPins)

	c.mu.Lock()
	for i := 0; i < policy.LiveSessionPins; i++ {
		c.pins[testSessionKeyN(i)] = sessionPin{state: PinConfirmed, expiry: time.Now().Add(time.Hour)}
	}
	c.mu.Unlock()

	result, pin := c.SelectForNewSession(context.Background(), key)
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if !result.PinMapFull {
		t.Error("PinMapFull = false, want true when the map is at its ceiling")
	}
	if pin.Generation != 0 {
		t.Errorf("generation = %d, want 0 (no pin created)", pin.Generation)
	}

	c.mu.Lock()
	size := len(c.pins)
	c.mu.Unlock()
	if size != policy.LiveSessionPins {
		t.Errorf("map size = %d, want %d (no new entry)", size, policy.LiveSessionPins)
	}
}

func TestPinMapCeilingNeverEvicts(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, _ := newSelectionTestCoordinator(perm)
	key := testSessionKeyN(policy.LiveSessionPins)

	c.mu.Lock()
	for i := 0; i < policy.LiveSessionPins; i++ {
		c.pins[testSessionKeyN(i)] = sessionPin{state: PinConfirmed, expiry: time.Now().Add(time.Hour)}
	}
	c.mu.Unlock()

	_, _ = c.SelectForNewSession(context.Background(), key)

	// Every pre-existing pin must survive the refusal.
	c.mu.Lock()
	defer c.mu.Unlock()
	for i := 0; i < policy.LiveSessionPins; i++ {
		if _, ok := c.pins[testSessionKeyN(i)]; !ok {
			t.Fatalf("existing pin %d was evicted by a full map", i)
		}
	}
}

func TestSweepFiresEvery256Operations(t *testing.T) {
	c, fake := newSessionTestCoordinator()
	keyA := testSessionKey('a')
	keyB := testSessionKey('b')

	c.ConfirmPin(keyA, catalog.AccountK1, 1)
	fake.AdvanceWall(policy.SessionAffinityTTL)

	// 255 operations on a different key bring the counter to 256, firing
	// the sweep, which removes the expired keyA pin without keyA ever being
	// accessed again.
	for i := 0; i < sessionSweepInterval-1; i++ {
		c.PinAccount(keyB)
	}

	c.mu.Lock()
	_, exists := c.pins[keyA]
	c.mu.Unlock()
	if exists {
		t.Error("expired pin not swept after 256 session operations")
	}
}

func TestSweepRemovesOnlyExpiredConfirmedPins(t *testing.T) {
	c, fake := newSessionTestCoordinator()
	expired := testSessionKey('a')
	live := testSessionKey('b')

	c.ConfirmPin(expired, catalog.AccountK1, 1)
	c.ConfirmPin(live, catalog.AccountK2, 1)
	fake.AdvanceWall(policy.SessionAffinityTTL)

	// Refresh the live pin so it is not expired, then advance again so the
	// expired pin is well past its TTL.
	c.ConfirmPin(live, catalog.AccountK2, 2)
	fake.AdvanceWall(time.Minute)

	for i := 0; i < sessionSweepInterval; i++ {
		c.PinAccount(testSessionKey('c'))
	}

	c.mu.Lock()
	_, expiredExists := c.pins[expired]
	_, liveExists := c.pins[live]
	c.mu.Unlock()
	if expiredExists {
		t.Error("expired confirmed pin not swept")
	}
	if !liveExists {
		t.Error("live confirmed pin swept")
	}
}

func TestSweepLeavesProvisionalPins(t *testing.T) {
	perm := testsupport.FixedPermutationSource{Values: []int{0, 1, 2}}
	c, fake := newSelectionTestCoordinator(perm)
	key := testSessionKey('a')

	_, _ = c.SelectForNewSession(context.Background(), key)
	fake.AdvanceWall(2 * policy.SessionAffinityTTL)

	for i := 0; i < sessionSweepInterval; i++ {
		c.PinAccount(testSessionKey('b'))
	}

	c.mu.Lock()
	_, exists := c.pins[key]
	c.mu.Unlock()
	if !exists {
		t.Error("provisional pin swept; it must live until the last holder releases")
	}
}
