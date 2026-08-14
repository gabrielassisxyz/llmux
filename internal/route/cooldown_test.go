package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestSingle429GatesOnlyThatAccountForTheStatedDelay(t *testing.T) {
	c, fake := newRateTestCoordinator()
	before := fake.MonotonicNow()

	result := c.Apply429(catalog.AccountK1, "45", time.Time{}, false)

	if result.EnteredCooldown {
		t.Error("EnteredCooldown = true on the first 429, want false")
	}
	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Error("account left enabled on a single 429")
	}
	if c.Health(catalog.AccountK2) != HealthEnabled {
		t.Error("a 429 for k1 affected k2's health")
	}

	c.mu.Lock()
	gate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	otherGate := c.accounts[accountIndex(catalog.AccountK2)].rateGateDeadline
	c.mu.Unlock()

	if want := before + 45*time.Second; gate != want {
		t.Errorf("gate deadline = %v, want %v", gate, want)
	}
	if otherGate != 0 {
		t.Errorf("k2's gate deadline = %v, want untouched at 0", otherGate)
	}
}

func TestSingle429WithNoRetryAfterGatesOneSecond(t *testing.T) {
	c, fake := newRateTestCoordinator()
	before := fake.MonotonicNow()

	c.Apply429(catalog.AccountK1, "", time.Time{}, false)

	c.mu.Lock()
	gate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()

	if want := before + time.Second; gate != want {
		t.Errorf("gate deadline = %v, want %v (one second after receipt)", gate, want)
	}
}

func TestApply429NeverShortensAnExistingLongerGate(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "600", time.Time{}, false)
	c.mu.Lock()
	longerGate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()

	fake.AdvanceMonotonic(time.Second)
	c.Apply429(catalog.AccountK1, "", time.Time{}, false)

	c.mu.Lock()
	gate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()
	if gate != longerGate {
		t.Errorf("gate deadline = %v, want existing longer deadline %v", gate, longerGate)
	}
}

func TestSingleOrDouble429DoesNotEnterCooldownOrClearHistory(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "5", time.Time{}, false)
	result := c.Apply429(catalog.AccountK1, "5", time.Time{}, false)

	if result.EnteredCooldown {
		t.Error("EnteredCooldown = true on the second 429, want false")
	}
	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Error("account entered a non-enabled health state before the third 429")
	}

	c.mu.Lock()
	count := len(c.accounts[accountIndex(catalog.AccountK1)].recent429s)
	c.mu.Unlock()
	if count != 2 {
		t.Errorf("recent429s count = %d, want 2 (retained, not cleared)", count)
	}
}

func TestThirdRetryWithinWindowEntersCooldownAndFloorsGate(t *testing.T) {
	c, fake := newRateTestCoordinator()
	before := fake.MonotonicNow()

	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	result := c.Apply429(catalog.AccountK1, "1", time.Time{}, false)

	if !result.EnteredCooldown {
		t.Fatal("EnteredCooldown = false on the third 429 in the window, want true")
	}
	if c.Health(catalog.AccountK1) != HealthCoolingDown {
		t.Errorf("health = %v, want HealthCoolingDown", c.Health(catalog.AccountK1))
	}

	c.mu.Lock()
	gate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()
	if want := before + policy.RollingRateWindow; gate != want {
		t.Errorf("gate deadline = %v, want %v (floored at 60s out, the stated 1s delay is smaller)", gate, want)
	}
}

func TestRetryAfterClampedToTenMinutes(t *testing.T) {
	c, fake := newRateTestCoordinator()
	before := fake.MonotonicNow()

	c.Apply429(catalog.AccountK1, "3600", time.Time{}, false) // one hour, stated

	c.mu.Lock()
	gate := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()
	if want := before + 10*time.Minute; gate != want {
		t.Errorf("gate deadline = %v, want %v (clamped to 10 minutes)", gate, want)
	}
}

func TestApply429StoredValueUnclampedEvenWhenGateIsClamped(t *testing.T) {
	c, _ := newRateTestCoordinator()

	result := c.Apply429(catalog.AccountK1, "3600", time.Time{}, false)
	if !result.StoredRetryAfterPresent || result.StoredRetryAfter != time.Hour {
		t.Errorf("StoredRetryAfter = %v, present = %v, want 1h, present (unclamped)", result.StoredRetryAfter, result.StoredRetryAfterPresent)
	}
}

func TestGateDeadlineExpiryThatCooldownOpenedReturnsToEnabledAndClearsHistory(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "1", time.Time{}, false)
	if c.Health(catalog.AccountK1) != HealthCoolingDown {
		t.Fatal("test setup: expected cooling_down after the third 429")
	}

	fake.AdvanceMonotonic(policy.RollingRateWindow)
	c.ExpireGateIfDue(catalog.AccountK1)

	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Errorf("health after cooldown-gate expiry = %v, want HealthEnabled", c.Health(catalog.AccountK1))
	}
	c.mu.Lock()
	count := len(c.accounts[accountIndex(catalog.AccountK1)].recent429s)
	deadline := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()
	if count != 0 {
		t.Errorf("recent429s count after cooldown-gate expiry = %d, want 0 (cleared)", count)
	}
	if deadline != 0 {
		t.Errorf("rateGateDeadline after expiry = %v, want 0", deadline)
	}
}

func TestGateDeadlineExpiryThatASingle429OpenedDoesNotClearHistory(t *testing.T) {
	c, fake := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "5", time.Time{}, false)
	c.Apply429(catalog.AccountK1, "5", time.Time{}, false)
	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Fatal("test setup: expected the account to remain enabled after two 429s")
	}

	fake.AdvanceMonotonic(5 * time.Second)
	c.ExpireGateIfDue(catalog.AccountK1)

	if c.Health(catalog.AccountK1) != HealthEnabled {
		t.Errorf("health = %v, want HealthEnabled (never left it)", c.Health(catalog.AccountK1))
	}
	c.mu.Lock()
	count := len(c.accounts[accountIndex(catalog.AccountK1)].recent429s)
	c.mu.Unlock()
	if count != 2 {
		t.Errorf("recent429s count after a single-429 gate expiry = %d, want 2 (not cleared: clearing here would reset the count on the first 429)", count)
	}
}

func TestExpireGateIfDueIsANoOpBeforeTheDeadline(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.Apply429(catalog.AccountK1, "60", time.Time{}, false)
	c.ExpireGateIfDue(catalog.AccountK1)

	c.mu.Lock()
	deadline := c.accounts[accountIndex(catalog.AccountK1)].rateGateDeadline
	c.mu.Unlock()
	if deadline == 0 {
		t.Error("ExpireGateIfDue cleared a deadline that had not yet passed")
	}
}

func TestApply429NotifiesWaiters(t *testing.T) {
	c, _ := newRateTestCoordinator()

	c.mu.Lock()
	token := c.WaitToken()
	c.mu.Unlock()

	c.Apply429(catalog.AccountK1, "5", time.Time{}, false)

	select {
	case <-token:
	default:
		t.Fatal("token did not fire after Apply429")
	}
}

func TestExpireGateIfDueNotifiesWaitersOnlyWhenItActuallyExpires(t *testing.T) {
	c, fake := newRateTestCoordinator()
	c.Apply429(catalog.AccountK1, "60", time.Time{}, false)

	c.mu.Lock()
	tooEarly := c.WaitToken()
	c.mu.Unlock()
	c.ExpireGateIfDue(catalog.AccountK1)
	select {
	case <-tooEarly:
		t.Fatal("token fired before the deadline actually passed")
	default:
	}

	fake.AdvanceMonotonic(60 * time.Second)
	c.mu.Lock()
	dueToken := c.WaitToken()
	c.mu.Unlock()
	c.ExpireGateIfDue(catalog.AccountK1)
	select {
	case <-dueToken:
	default:
		t.Fatal("token did not fire once the deadline passed")
	}
}
