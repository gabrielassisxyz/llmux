// Package route holds the account limiter, health, session affinity,
// account selection and lease state this proxy coordinates across its
// three upstream accounts. It performs no I/O of any kind: dispatch,
// persistence and logging belong to the packages that call into it.
package route

import (
	"sync"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/clock"
)

// HealthState is an account's current admission eligibility.
type HealthState int

const (
	HealthEnabled HealthState = iota
	HealthCoolingDown
	HealthDisabled
)

// accountState is the per-account state this design tracks. Every field is
// read or written only while the Coordinator's mutex is held.
type accountState struct {
	label catalog.Account
	key   string

	// dispatchTimestamps holds the monotonic instant of each dispatch
	// still inside the rolling rate window, ascending.
	dispatchTimestamps []time.Duration

	// pendingReservations counts rate-window slots granted by
	// ReserveRateSlot but not yet moved into dispatchTimestamps by
	// FinalizeDispatch or freed by ReleasePendingRateSlot. Counting it
	// against the ceiling alongside dispatchTimestamps is what stops a
	// concurrent caller from being admitted into a slot the window has
	// already granted but not yet recorded.
	pendingReservations int

	inFlight int
	health   HealthState

	// rateGateDeadline is the monotonic instant before which the account
	// is not eligible, advanced by any 429 and floored by the cooldown
	// circuit. The zero value means no active gate.
	rateGateDeadline time.Duration

	// recent429s holds the monotonic instant of each 429 the cooldown
	// circuit has not yet consumed.
	recent429s []time.Duration

	// notifyGeneration increments on any state change a waiter cares
	// about, so a replace-on-notify channel knows to wake.
	notifyGeneration uint64
}

// AccountKeys holds the three upstream account credentials a Coordinator is
// constructed with, one per fixed account identity. The type's three named
// fields make "exactly three accounts" a property of the type itself
// rather than a runtime check on a variable-length collection.
type AccountKeys struct {
	K1 string
	K2 string
	K3 string
}

// Coordinator guards all account and session state behind one mutex. No
// I/O of any kind, no network call, no database write, no log line, occurs
// while the lock is held: skip observations, process logs and durable
// writes all happen after unlock.
type Coordinator struct {
	mu       sync.Mutex
	accounts [3]accountState
	clk      clock.Clock

	// notify is closed and replaced under mu by every mutation a waiter
	// might care about. See notifyLocked and WaitToken in wait.go.
	notify chan struct{}
}

// NewCoordinator constructs a Coordinator holding exactly three account
// records, all initially enabled. keys is assumed already validated
// (non-empty, mutually distinct): that is the configuration loader's
// responsibility, not this constructor's. clk is the injected clock
// boundary Wait uses for its account-acquisition ceiling.
func NewCoordinator(keys AccountKeys, clk clock.Clock) *Coordinator {
	c := &Coordinator{clk: clk, notify: make(chan struct{})}
	c.accounts[accountIndex(catalog.AccountK1)] = accountState{label: catalog.AccountK1, key: keys.K1, health: HealthEnabled}
	c.accounts[accountIndex(catalog.AccountK2)] = accountState{label: catalog.AccountK2, key: keys.K2, health: HealthEnabled}
	c.accounts[accountIndex(catalog.AccountK3)] = accountState{label: catalog.AccountK3, key: keys.K3, health: HealthEnabled}
	return c
}

// accountIndex maps a fixed account identity to its slot in the array.
// Every caller in this package passes one of the three catalog.Account
// constants, so the fallback branch is unreachable in practice; it exists
// only so an invalid label degrades to a defined, harmless slot rather
// than an out-of-bounds panic.
func accountIndex(label catalog.Account) int {
	switch label {
	case catalog.AccountK1:
		return 0
	case catalog.AccountK2:
		return 1
	default:
		return 2
	}
}

// InFlight returns account's current in-flight dispatch count.
func (c *Coordinator) InFlight(account catalog.Account) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accounts[accountIndex(account)].inFlight
}

// IncrementInFlight increments account's in-flight count and returns the
// new value.
func (c *Coordinator) IncrementInFlight(account catalog.Account) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.accounts[accountIndex(account)]
	state.inFlight++
	return state.inFlight
}

// DecrementInFlight decrements account's in-flight count and returns the
// new value. Releasing a slot is exactly the event a waiter blocked on
// capacity cares about, so this wakes them.
func (c *Coordinator) DecrementInFlight(account catalog.Account) int {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.accounts[accountIndex(account)]
	state.inFlight--
	c.notifyLocked()
	return state.inFlight
}

// Health returns account's current health state.
func (c *Coordinator) Health(account catalog.Account) HealthState {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.accounts[accountIndex(account)].health
}
