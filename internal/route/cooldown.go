package route

import (
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// cooldownThreshold is the count of 429s within one rolling window that
// opens the cooldown circuit, per PLAN.md 20.2: three in sixty seconds.
const cooldownThreshold = 3

// cooldownGateFloor is the minimum gate duration the cooldown circuit
// enforces, independent of what any individual 429 in the window stated.
const cooldownGateFloor = policy.RollingRateWindow

// retryAfterClamp bounds how far a single 429 may advance an account's
// gate deadline, regardless of what Retry-After stated.
const retryAfterClamp = 10 * time.Minute

// Apply429Result carries the parsed Retry-After for whoever eventually
// writes the attempt row: the stored value must preserve the distinction
// between "upstream said nothing usable" and "upstream said retry now",
// which is exactly what StoredPresent and StoredRetryAfter carry.
type Apply429Result struct {
	StoredRetryAfter        time.Duration
	StoredRetryAfterPresent bool
	EnteredCooldown         bool
}

// Apply429 records an upstream 429 for account. It adds the receipt
// instant to the recent-429 history, pruning entries outside the rolling
// window; derives the gate delay from header and responseDate; extends
// the gate deadline by that delay, clamped to ten minutes, without ever
// shortening an existing gate; and, on the
// third 429 within the window, additionally enters cooldown and floors the
// gate deadline at one full rolling window from now.
//
// Every mutation happens under the coordinator lock. The caller is
// responsible for calling this before the response is drained and before
// anything is written to SQLite: the window between classification and
// those steps is one in which every concurrent selection can still hand
// out a credential or a slot this process has already watched upstream
// refuse.
func (c *Coordinator) Apply429(account catalog.Account, header string, responseDate time.Time, hasResponseDate bool) Apply429Result {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	now := c.clk.MonotonicNow()

	parsed := parseRetryAfter(header, responseDate, hasResponseDate)

	state.recent429s = pruneOlderThanLocked(state.recent429s, now-policy.RollingRateWindow)
	state.recent429s = append(state.recent429s, now)

	advance := parsed.gate
	if advance > retryAfterClamp {
		advance = retryAfterClamp
	}
	gate := now + advance

	entered := false
	if len(state.recent429s) >= cooldownThreshold {
		state.health = HealthCoolingDown
		if floor := now + cooldownGateFloor; gate < floor {
			gate = floor
		}
		entered = true
	}
	if gate > state.rateGateDeadline {
		state.rateGateDeadline = gate
	}

	c.notifyLocked()

	return Apply429Result{
		StoredRetryAfter:        parsed.stored,
		StoredRetryAfterPresent: parsed.storedPresent,
		EnteredCooldown:         entered,
	}
}

// ExpireGateIfDue lazily expires account's gate deadline if it has passed.
// Expiry is lazy by design: nothing sweeps it on a timer, so this must be
// called wherever an account's eligibility is about to be evaluated.
//
// If the gate was opened by the cooldown circuit, the account returns to
// enabled and its recent-429 history is cleared. If the gate was opened by
// a single 429, the account was never anything but enabled, and its
// history is left untouched: clearing it here would reset the count on
// the very first 429 and the cooldown threshold could never be reached.
func (c *Coordinator) ExpireGateIfDue(account catalog.Account) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	if state.rateGateDeadline == 0 || c.clk.MonotonicNow() < state.rateGateDeadline {
		return
	}

	wasCoolingDown := state.health == HealthCoolingDown
	state.rateGateDeadline = 0
	if wasCoolingDown {
		state.health = HealthEnabled
		state.recent429s = nil
	}
	c.notifyLocked()
}
