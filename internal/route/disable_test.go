package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
)

func TestDisableMarksOnlyThatAccountDisabled(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Disable(catalog.AccountK1)

	if c.Health(catalog.AccountK1) != HealthDisabled {
		t.Errorf("k1 health = %v, want HealthDisabled", c.Health(catalog.AccountK1))
	}
	if c.Health(catalog.AccountK2) != HealthEnabled {
		t.Errorf("k2 health = %v, want HealthEnabled (untouched)", c.Health(catalog.AccountK2))
	}
	if c.Health(catalog.AccountK3) != HealthEnabled {
		t.Errorf("k3 health = %v, want HealthEnabled (untouched)", c.Health(catalog.AccountK3))
	}
}

func TestDisableLeavesGateDeadlineAndHistoryUntouched(t *testing.T) {
	c, _ := newRateTestCoordinator()

	// Give k1 a gate deadline and a recent-429 history before disabling.
	c.Apply429(catalog.AccountK1, "30", time.Time{}, false)

	c.mu.Lock()
	gateBefore := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	historyBefore := len(c.accounts[accountIndex(catalog.AccountK1)].recent429s)
	c.mu.Unlock()

	c.Disable(catalog.AccountK1)

	c.mu.Lock()
	gateAfter := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	historyAfter := len(c.accounts[accountIndex(catalog.AccountK1)].recent429s)
	c.mu.Unlock()

	if gateAfter != gateBefore {
		t.Errorf("rateGateDeadline = %v after Disable, want untouched %v", gateAfter, gateBefore)
	}
	if historyAfter != historyBefore {
		t.Errorf("recent429s count = %d after Disable, want untouched %d", historyAfter, historyBefore)
	}
}

func TestDisableRemovesConfirmedPinsPointingAtIt(t *testing.T) {
	c, _ := newSessionTestCoordinator()

	disabledKey := testSessionKey('a')
	healthyKey := testSessionKey('b')
	c.ConfirmPin(disabledKey, catalog.AccountK1, 1)
	c.ConfirmPin(healthyKey, catalog.AccountK2, 1)

	c.Disable(catalog.AccountK1)

	if _, ok := c.PinAccount(disabledKey); ok {
		t.Error("confirmed pin to the disabled account survived Disable")
	}
	if _, ok := c.PinAccount(healthyKey); !ok {
		t.Error("confirmed pin to a healthy account was removed by Disable")
	}
}

func TestDisableRemovesProvisionalPinsPointingAtIt(t *testing.T) {
	c, _ := newSessionTestCoordinator()

	disabledKey := testSessionKey('a')
	healthyKey := testSessionKey('b')
	c.mu.Lock()
	c.pins[disabledKey] = sessionPin{account: catalog.AccountK1, state: PinProvisional, generation: 1, holders: 1}
	c.pins[healthyKey] = sessionPin{account: catalog.AccountK2, state: PinProvisional, generation: 1, holders: 1}
	c.mu.Unlock()

	c.Disable(catalog.AccountK1)

	c.mu.Lock()
	_, disabledExists := c.pins[disabledKey]
	_, healthyExists := c.pins[healthyKey]
	c.mu.Unlock()
	if disabledExists {
		t.Error("provisional pin to the disabled account survived Disable")
	}
	if !healthyExists {
		t.Error("provisional pin to a healthy account was removed by Disable")
	}
}

func TestDisableNotifiesWaiters(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	c.Disable(catalog.AccountK1)

	select {
	case <-token:
	default:
		t.Fatal("token did not fire after Disable")
	}
}

func TestDisableIsIdempotent(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Disable(catalog.AccountK1)
	c.Disable(catalog.AccountK1)

	if c.Health(catalog.AccountK1) != HealthDisabled {
		t.Errorf("k1 health = %v after a second Disable, want HealthDisabled", c.Health(catalog.AccountK1))
	}
	if c.Health(catalog.AccountK2) != HealthEnabled {
		t.Errorf("k2 health = %v, want HealthEnabled (a repeated Disable must not spread)", c.Health(catalog.AccountK2))
	}
}

func TestDisabledAccountIsNeverReenabledByGateExpiry(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "5", time.Time{}, false)
	c.Disable(catalog.AccountK1)

	fake.AdvanceMonotonic(5 * time.Second)
	c.ExpireGateIfDue(catalog.AccountK1)

	if c.Health(catalog.AccountK1) != HealthDisabled {
		t.Errorf("k1 health = %v after gate expiry, want HealthDisabled (no re-enable path)", c.Health(catalog.AccountK1))
	}
}

func TestDisabledAccountIsNeverReenabledByApply429(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Disable(catalog.AccountK1)

	// Three 429s would normally open the cooldown circuit; on a disabled
	// account they must change nothing, or gate expiry could later re-enable
	// the account.
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)

	if c.Health(catalog.AccountK1) != HealthDisabled {
		t.Errorf("k1 health = %v after Apply429, want HealthDisabled (no re-enable path)", c.Health(catalog.AccountK1))
	}
}

func TestReserveSkipsAccountDisabledThroughDisable(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Disable(catalog.AccountK1)

	lease, outcome := c.Reserve(catalog.AccountK1)
	if outcome != SkippedDisabled {
		t.Fatalf("outcome = %v, want SkippedDisabled", outcome)
	}
	if lease != nil {
		t.Fatal("Reserve returned a lease for an account disabled through Disable")
	}
}
