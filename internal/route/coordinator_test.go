package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

func testKeys() AccountKeys {
	return AccountKeys{K1: "k1-secret", K2: "k2-secret", K3: "k3-secret"}
}

func TestNewCoordinatorCreatesExactlyThreeAccounts(t *testing.T) {
	c := NewCoordinator(testKeys())

	if len(c.accounts) != 3 {
		t.Fatalf("len(accounts) = %d, want 3", len(c.accounts))
	}

	want := map[catalog.Account]string{
		catalog.AccountK1: testKeys().K1,
		catalog.AccountK2: testKeys().K2,
		catalog.AccountK3: testKeys().K3,
	}
	seen := make(map[catalog.Account]bool, 3)
	for _, state := range c.accounts {
		wantKey, known := want[state.label]
		if !known {
			t.Errorf("unexpected account label %q", state.label)
			continue
		}
		if seen[state.label] {
			t.Errorf("duplicate account label %q", state.label)
		}
		seen[state.label] = true
		if state.key != wantKey {
			t.Errorf("%s key = %q, want %q", state.label, state.key, wantKey)
		}
		if state.health != HealthEnabled {
			t.Errorf("%s health = %v, want HealthEnabled", state.label, state.health)
		}
	}
	for label := range want {
		if !seen[label] {
			t.Errorf("missing account %q", label)
		}
	}
}

// TestNewCoordinatorInitializesAllAccountFields exercises every field this
// bead's data model specifies, even the ones with no public mutator yet:
// the rolling-window, cooldown and wait/wake beads read and write these
// directly as same-package code, so their zero-value contract is this
// bead's responsibility to prove.
func TestNewCoordinatorInitializesAllAccountFields(t *testing.T) {
	c := NewCoordinator(testKeys())

	for _, state := range c.accounts {
		if len(state.dispatchTimestamps) != 0 {
			t.Errorf("%s dispatchTimestamps = %v, want empty", state.label, state.dispatchTimestamps)
		}
		if state.rateGateDeadline != 0 {
			t.Errorf("%s rateGateDeadline = %v, want zero", state.label, state.rateGateDeadline)
		}
		if len(state.recent429s) != 0 {
			t.Errorf("%s recent429s = %v, want empty", state.label, state.recent429s)
		}
		if state.notifyGeneration != 0 {
			t.Errorf("%s notifyGeneration = %d, want zero", state.label, state.notifyGeneration)
		}
		if state.inFlight != 0 {
			t.Errorf("%s inFlight = %d, want zero", state.label, state.inFlight)
		}
	}
}

// TestSeparateAliasesShareTheSameAccountCount proves the invariant the
// background section states as non-negotiable: no alias creates its own
// rate bucket. Two different pinned aliases that resolve to the same
// account must move the same coordinator-internal counter.
func TestSeparateAliasesShareTheSameAccountCount(t *testing.T) {
	c := NewCoordinator(testKeys())

	first, ok := catalog.Resolve("kimi-k2.7-k1")
	if !ok {
		t.Fatal("catalog.Resolve(kimi-k2.7-k1) not found")
	}
	second, ok := catalog.Resolve("glm-5.2-k1")
	if !ok {
		t.Fatal("catalog.Resolve(glm-5.2-k1) not found")
	}
	if first.EligibleAccounts[0] != second.EligibleAccounts[0] {
		t.Fatalf("test setup: %q and %q resolved to different accounts", "kimi-k2.7-k1", "glm-5.2-k1")
	}

	c.IncrementInFlight(first.EligibleAccounts[0])
	c.IncrementInFlight(second.EligibleAccounts[0])

	if got := c.InFlight(catalog.AccountK1); got != 2 {
		t.Errorf("InFlight(k1) = %d, want 2 after dispatches through two separate aliases", got)
	}
}

// TestBaseAndPinnedAliasesShareTheSameAccountCount proves the same
// invariant across a base alias (eligible for all three accounts) and one
// of its own pinned variants (eligible for exactly one): both name the same
// account identity, so both must move the same counter.
func TestBaseAndPinnedAliasesShareTheSameAccountCount(t *testing.T) {
	c := NewCoordinator(testKeys())

	base, ok := catalog.Resolve("kimi-k2.7")
	if !ok {
		t.Fatal("catalog.Resolve(kimi-k2.7) not found")
	}
	pinned, ok := catalog.Resolve("kimi-k2.7-k2")
	if !ok {
		t.Fatal("catalog.Resolve(kimi-k2.7-k2) not found")
	}

	var baseK2 catalog.Account
	found := false
	for _, account := range base.EligibleAccounts {
		if account == catalog.AccountK2 {
			baseK2 = account
			found = true
		}
	}
	if !found {
		t.Fatal("test setup: base alias is not eligible for k2")
	}

	c.IncrementInFlight(baseK2)
	c.IncrementInFlight(pinned.EligibleAccounts[0])

	if got := c.InFlight(catalog.AccountK2); got != 2 {
		t.Errorf("InFlight(k2) = %d, want 2 after dispatches through the base and pinned aliases", got)
	}
}

func TestDifferentAccountsRemainIndependent(t *testing.T) {
	c := NewCoordinator(testKeys())

	c.IncrementInFlight(catalog.AccountK1)
	c.IncrementInFlight(catalog.AccountK1)
	c.IncrementInFlight(catalog.AccountK2)
	c.DecrementInFlight(catalog.AccountK2)

	if got := c.InFlight(catalog.AccountK1); got != 2 {
		t.Errorf("InFlight(k1) = %d, want 2", got)
	}
	if got := c.InFlight(catalog.AccountK2); got != 0 {
		t.Errorf("InFlight(k2) = %d, want 0", got)
	}
	if got := c.InFlight(catalog.AccountK3); got != 0 {
		t.Errorf("InFlight(k3) = %d, want 0 (never touched)", got)
	}
}

func TestHealthDefaultsToEnabled(t *testing.T) {
	c := NewCoordinator(testKeys())
	for _, account := range []catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3} {
		if got := c.Health(account); got != HealthEnabled {
			t.Errorf("Health(%s) = %v, want HealthEnabled", account, got)
		}
	}
}

// TestCoordinatorConcurrentAccessIsRaceFree drives many goroutines against
// every exported accessor at once. Run under go test -race; correctness is
// checked by comparing the final in-flight count against exactly the net
// of increments and decrements this test issued, which only holds if the
// mutex actually serializes every access.
func TestCoordinatorConcurrentAccessIsRaceFree(t *testing.T) {
	c := NewCoordinator(testKeys())
	const goroutines = 50
	const opsPerGoroutine = 200

	done := make(chan struct{}, goroutines)
	for i := 0; i < goroutines; i++ {
		go func(id int) {
			defer func() { done <- struct{}{} }()
			for j := 0; j < opsPerGoroutine; j++ {
				c.IncrementInFlight(catalog.AccountK1)
				_ = c.InFlight(catalog.AccountK1)
				_ = c.Health(catalog.AccountK2)
				c.DecrementInFlight(catalog.AccountK1)
			}
		}(i)
	}

	deadline := time.After(10 * time.Second)
	for i := 0; i < goroutines; i++ {
		select {
		case <-done:
		case <-deadline:
			t.Fatal("goroutines did not finish within the deadline; possible deadlock")
		}
	}

	if got := c.InFlight(catalog.AccountK1); got != 0 {
		t.Errorf("InFlight(k1) = %d after equal increments and decrements, want 0", got)
	}
}
