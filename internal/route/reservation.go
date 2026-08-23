package route

import (
	"sync"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

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

	// reservedAt is the monotonic instant the reservation was granted.
	reservedAt time.Duration

	// rateWindowAtReserve is the number of dispatch slots the account's
	// rolling window held at reservation time, including this lease's
	// pending slot.
	rateWindowAtReserve int

	// inFlightAtReserve is the account's in-flight count at reservation
	// time, including this lease.
	inFlightAtReserve int

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

// ReservedAt returns the monotonic instant the reservation was granted.
func (l *PendingLease) ReservedAt() time.Duration { return l.reservedAt }

// RateWindowAtReserve returns the number of dispatch slots the account's
// rolling window held at reservation time, including this lease's pending
// slot.
func (l *PendingLease) RateWindowAtReserve() int { return l.rateWindowAtReserve }

// InFlightAtReserve returns the account's in-flight count at reservation
// time, including this lease.
func (l *PendingLease) InFlightAtReserve() int { return l.inFlightAtReserve }

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
	}
	// The release-once guard above already makes a second decrement
	// impossible; the floor here is defense in depth so a release can
	// never drive the counter negative at any observation point.
	if state.inFlight > 0 {
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
	return &PendingLease{
		coord:               c,
		account:             account,
		reservedAt:          now,
		rateWindowAtReserve: len(state.dispatchTimestamps) + state.pendingReservations,
		inFlightAtReserve:   state.inFlight,
	}, Reserved
}
