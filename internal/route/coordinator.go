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
	"github.com/gabrielassisxyz/llmux/internal/policy"
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
	perm     clock.PermutationSource

	// pins holds the live session-affinity map, keyed by the versioned
	// session digest. It is guarded by mu like every other field.
	pins map[SessionKey]sessionPin

	// notify is closed and replaced under mu by every mutation a waiter
	// might care about. See notifyLocked and WaitToken in wait.go.
	notify chan struct{}
}

// NewCoordinator constructs a Coordinator holding exactly three account
// records, all initially enabled. keys is assumed already validated
// (non-empty, mutually distinct): that is the configuration loader's
// responsibility, not this constructor's. clk is the injected clock
// boundary Wait uses for its account-acquisition ceiling. perm is the
// injected permutation source Select uses to order candidate accounts, so
// tests can drive a deterministic order.
func NewCoordinator(keys AccountKeys, clk clock.Clock, perm clock.PermutationSource) *Coordinator {
	c := &Coordinator{clk: clk, perm: perm, notify: make(chan struct{}), pins: make(map[SessionKey]sessionPin)}
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

// accountFromIndex maps a permutation index to its fixed account identity.
// It is the inverse of accountIndex: a permutation source returns indices
// into the three-account array, and the selection pass translates each back
// to the catalog.Account it names.
func accountFromIndex(idx int) catalog.Account {
	switch idx {
	case 0:
		return catalog.AccountK1
	case 1:
		return catalog.AccountK2
	default:
		return catalog.AccountK3
	}
}

// ReservationOutcome reports why Reserve returned the lease it did, if any.
type ReservationOutcome int

const (
	Reserved ReservationOutcome = iota
	SkippedBlackout
	SkippedDisabled
	SkippedGate
	SkippedInFlightSaturated
	SkippedRateSaturated
)

// PendingLease is a held reservation that has not yet been committed to the
// rolling window. It is release-once: a caller either finalizes it at the
// Do boundary or releases it when the admission commit fails.
type PendingLease struct {
	coord   *Coordinator
	account catalog.Account

	mu    sync.Mutex
	state pendingLeaseState
}

type pendingLeaseState int

const (
	pendingLeaseOpen pendingLeaseState = iota
	pendingLeaseFinalized
	pendingLeaseReleased
)

// Account returns the account this lease reserved.
func (l *PendingLease) Account() catalog.Account { return l.account }

// Finalize moves the pending reservation into the rolling window. It is safe
// to call more than once; only the first call has any effect. Finalizing does
// not release the in-flight slot: the caller owns that until the upstream
// response body closes.
func (l *PendingLease) Finalize() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state != pendingLeaseOpen {
		return
	}
	l.state = pendingLeaseFinalized

	l.coord.mu.Lock()
	defer l.coord.mu.Unlock()
	state := &l.coord.accounts[accountIndex(l.account)]
	if state.pendingReservations > 0 {
		state.pendingReservations--
	}
	state.dispatchTimestamps = append(state.dispatchTimestamps, l.coord.clk.MonotonicNow())
}

// Release frees the reservation. The first call from the open state frees both
// the pending slot and the in-flight slot and wakes waiters; a call after
// finalize only frees the in-flight slot. Further calls are no-ops.
func (l *PendingLease) Release() {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.state == pendingLeaseReleased {
		return
	}
	wasOpen := l.state == pendingLeaseOpen
	l.state = pendingLeaseReleased

	l.coord.mu.Lock()
	defer l.coord.mu.Unlock()
	state := &l.coord.accounts[accountIndex(l.account)]
	if wasOpen {
		if state.pendingReservations > 0 {
			state.pendingReservations--
		}
		state.inFlight--
	} else {
		state.inFlight--
	}
	l.coord.notifyLocked()
}

// Reserve performs the single critical-section admission algorithm for account
// and returns a held lease on success. Every skip reason is produced before
// any state is mutated, and the rate check and the pending/in-flight
// increments happen under one lock hold so concurrent callers cannot claim the
// same final slot.
func (c *Coordinator) Reserve(account catalog.Account) (*PendingLease, ReservationOutcome) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.reserveLocked(account)
}

// reserveLocked performs the admission check for account. The caller must
// hold c.mu. It is the shared core of Reserve and the selection pass, so a
// selection can consider several accounts under one lock hold without
// releasing the lock between candidates.
func (c *Coordinator) reserveLocked(account catalog.Account) (*PendingLease, ReservationOutcome) {
	now := c.clk.MonotonicNow()
	if now < policy.PostStartDispatchBlackout {
		return nil, SkippedBlackout
	}

	state := &c.accounts[accountIndex(account)]
	c.expireGateIfDueLocked(state)
	c.pruneRateWindowLocked(state)

	switch {
	case state.health == HealthDisabled:
		return nil, SkippedDisabled
	case state.rateGateDeadline != 0 && now < state.rateGateDeadline:
		return nil, SkippedGate
	case state.inFlight >= policy.InFlightAttemptsPerAccount:
		return nil, SkippedInFlightSaturated
	case len(state.dispatchTimestamps)+state.pendingReservations >= policy.DispatchesPerWindowPerAccount:
		return nil, SkippedRateSaturated
	}

	state.pendingReservations++
	state.inFlight++
	return &PendingLease{coord: c, account: account}, Reserved
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
