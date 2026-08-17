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

// newSelectionTestCoordinator returns a coordinator whose fake clock has
// already advanced past the post-start dispatch blackout, with the supplied
// permutation source injected.
func newSelectionTestCoordinator(perm clock.PermutationSource) (*Coordinator, *testsupport.FakeClock) {
	fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	c := NewCoordinator(testKeys(), fake, perm)
	fake.AdvanceMonotonic(policy.PostStartDispatchBlackout)
	return c, fake
}

// countingPermutationSource returns a fixed sequence of permutations and
// counts how many times Perm was called, so a test can prove the selection
// reshuffles after a wake rather than reusing the first permutation.
type countingPermutationSource struct {
	calls  int
	values [][]int
}

func (s *countingPermutationSource) Perm(n int) []int {
	v := s.values[s.calls%len(s.values)]
	s.calls++
	return append([]int(nil), v...)
}

func TestSelectFollowsInjectedPermutation(t *testing.T) {
	// The permutation names k3 first, so a healthy coordinator must select
	// k3 even though k1 is the first account in the fixed array.
	perm := testsupport.FixedPermutationSource{Values: []int{2, 0, 1}}
	c, _ := newSelectionTestCoordinator(perm)

	result := c.Select(context.Background())
	if result.Outcome != SelectionReserved {
		t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
	}
	if result.Lease == nil {
		t.Fatal("Select returned a nil lease with SelectionReserved")
	}
	if got := result.Lease.Account(); got != catalog.AccountK3 {
		t.Errorf("lease account = %v, want k3 (first in the injected permutation)", got)
	}
}

func TestSelectAllDisabledReturnsImmediately(t *testing.T) {
	c, _ := newSelectionTestCoordinator(testsupport.FixedPermutationSource{Values: []int{0, 1, 2}})

	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].health = HealthDisabled
	}
	c.mu.Unlock()

	start := time.Now()
	result := c.Select(context.Background())
	elapsed := time.Since(start)

	if result.Outcome != SelectionAllDisabled {
		t.Fatalf("outcome = %v, want SelectionAllDisabled", result.Outcome)
	}
	if result.Lease != nil {
		t.Fatal("Select returned a lease for an all-disabled state")
	}
	if elapsed >= time.Second {
		t.Errorf("all-disabled Select took %v, want an immediate return with no wait", elapsed)
	}
}

func TestSelectReshufflesAfterWake(t *testing.T) {
	// The first permutation is [k1,k2,k3]; the second is [k3,k1,k2]. After
	// the wake the second pass must use the second permutation, which is
	// proven by the source being called twice.
	perm := &countingPermutationSource{values: [][]int{{0, 1, 2}, {2, 1, 0}}}
	c, _ := newSelectionTestCoordinator(perm)

	// Saturate every account so the first pass skips all three and waits.
	c.mu.Lock()
	for i := range c.accounts {
		c.accounts[i].inFlight = policy.InFlightAttemptsPerAccount
	}
	c.mu.Unlock()

	resultCh := make(chan SelectionResult, 1)
	go func() {
		resultCh <- c.Select(context.Background())
	}()

	// Give the selection goroutine time to run its first pass and block on
	// the notification token before releasing capacity.
	time.Sleep(50 * time.Millisecond)

	c.mu.Lock()
	c.accounts[accountIndex(catalog.AccountK1)].inFlight--
	c.notifyLocked()
	c.mu.Unlock()

	select {
	case result := <-resultCh:
		if result.Outcome != SelectionReserved {
			t.Fatalf("outcome = %v, want SelectionReserved", result.Outcome)
		}
		if got := result.Lease.Account(); got != catalog.AccountK1 {
			t.Errorf("lease account = %v, want k1 (the only released account)", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Select did not return after the wake")
	}

	if perm.calls < 2 {
		t.Errorf("permutation source called %d times, want at least 2 (reshuffle after wake)", perm.calls)
	}
}

func TestSelectDistributesLoadByPermutation(t *testing.T) {
	// A rotating source cycles through the three rotations, so each account
	// is first exactly one third of the time. With every account healthy the
	// first candidate is always reserved, so the load must follow the
	// permutation rather than always hitting k1.
	perm := &countingPermutationSource{values: [][]int{{0, 1, 2}, {1, 2, 0}, {2, 0, 1}}}
	c, _ := newSelectionTestCoordinator(perm)

	const rounds = 300
	counts := map[catalog.Account]int{}
	for i := 0; i < rounds; i++ {
		result := c.Select(context.Background())
		if result.Outcome != SelectionReserved {
			t.Fatalf("round %d: outcome = %v, want SelectionReserved", i, result.Outcome)
		}
		counts[result.Lease.Account()]++
		result.Lease.Release()
	}

	for _, account := range []catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3} {
		got := counts[account]
		if got < rounds/3-rounds/10 || got > rounds/3+rounds/10 {
			t.Errorf("%s selected %d times, want roughly %d", account, got, rounds/3)
		}
	}
}
