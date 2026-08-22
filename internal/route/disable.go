package route

import "github.com/gabrielassisxyz/llmux/internal/catalog"

// Disable permanently removes account from rotation for the rest of the
// process lifetime. It is the mutation a caller applies when an upstream
// response is classified as a 401 (upstream_authentication), and it must
// run under the coordinator lock, before the response body is drained and
// before anything is written to SQLite: the window between classification
// and those steps is one in which every concurrent selection can still hand
// out a credential this process has already watched upstream refuse.
//
// In one critical section it marks the account HealthDisabled, removes
// every session pin pointing at it, confirmed and provisional alike (a pin
// to a disabled account can never be served again, and a leftover
// provisional pin would let a straggler confirm affinity to a dead
// account), and wakes every waiter so a blocked selection re-evaluates
// against the reduced eligible set.
//
// The gate deadline and the recent-429 history are left untouched: neither
// is consulted again once the account is disabled.
//
// There is no re-enable path. Disable is idempotent; gate expiry only ever
// returns a HealthCoolingDown account to enabled, and Apply429 is a no-op
// on a disabled account, so no timer or request can bring a disabled
// account back. Correcting a revoked credential requires a restart, which
// clears volatile disabled state without making a probe.
func (c *Coordinator) Disable(account catalog.Account) {
	c.mu.Lock()
	defer c.mu.Unlock()

	state := &c.accounts[accountIndex(account)]
	state.health = HealthDisabled

	for key, pin := range c.pins {
		if pin.account == account {
			delete(c.pins, key)
		}
	}

	c.notifyLocked()
}
