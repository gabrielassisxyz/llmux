package route

import (
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// This file is the reference model the model-based suite in
// model_machine_test.go checks the real Coordinator against. It is written
// from the rules as stated (the concurrency invariants in doc.go, the
// account-health conformance matrix in health_matrix.go, and the constants
// in internal/policy) in the simplest form that expresses each rule, so that
// agreement between the two is evidence rather than the same code written
// twice. Where a rule is deliberately lazy in the implementation
// (window pruning, gate expiry) the model is lazy at exactly the same
// points, because "when does this happen" is itself one of the rules and a
// model that swept eagerly would disagree with a correct implementation.
//
// The model owns no clock. Every method that needs an instant is handed the
// one the driver read from the same fake clock the Coordinator holds, so a
// divergence can never be an artifact of the two reading time separately.

// modelAccount mirrors the per-account quantities the rules constrain.
type modelAccount struct {
	dispatchTimestamps  []time.Duration
	pendingReservations int
	inFlight            int
	health              HealthState
	rateGateDeadline    time.Duration
	recent429s          []time.Duration
}

// modelPin mirrors one session-affinity record.
type modelPin struct {
	account     catalog.Account
	expiry      time.Time
	sequence    uint64
	nextArrival uint64
	state       PinState
	generation  uint64
	holders     int
}

// coordinatorModel is the whole reference state.
type coordinatorModel struct {
	accounts   [3]modelAccount
	pins       map[SessionKey]modelPin
	sessionOps int
}

func newCoordinatorModel() *coordinatorModel {
	return &coordinatorModel{pins: map[SessionKey]modelPin{}}
}

// pruneBefore drops instants at or before cutoff from an ascending slice.
func pruneBefore(instants []time.Duration, cutoff time.Duration) []time.Duration {
	kept := instants[:0:0]
	for _, instant := range instants {
		if instant > cutoff {
			kept = append(kept, instant)
		}
	}
	return kept
}

// reserve states the admission rule: the blackout refuses before anything
// else is even looked at, then eligibility is evaluated against one
// snapshot, and only a granted reservation mutates anything.
func (m *coordinatorModel) reserve(idx int, now time.Duration) ReservationOutcome {
	if now < policy.PostStartDispatchBlackout {
		return SkippedBlackout
	}

	account := &m.accounts[idx]
	m.expireGateIfDue(idx, now)
	account.dispatchTimestamps = pruneBefore(account.dispatchTimestamps, now-policy.RollingRateWindow)

	switch {
	case account.health == HealthDisabled:
		return SkippedDisabled
	case account.rateGateDeadline != 0 && now < account.rateGateDeadline:
		return SkippedGate
	case account.inFlight >= policy.InFlightAttemptsPerAccount:
		return SkippedInFlightSaturated
	case len(account.dispatchTimestamps)+account.pendingReservations >= policy.DispatchesPerWindowPerAccount:
		return SkippedRateSaturated
	}

	account.pendingReservations++
	account.inFlight++
	return Reserved
}

// reserveRateSlot is the rate-only half of admission: it consults the
// window and the blackout and nothing else, so it neither expires a gate
// nor looks at health.
func (m *coordinatorModel) reserveRateSlot(idx int, now time.Duration) bool {
	if now < policy.PostStartDispatchBlackout {
		return false
	}

	account := &m.accounts[idx]
	account.dispatchTimestamps = pruneBefore(account.dispatchTimestamps, now-policy.RollingRateWindow)
	if len(account.dispatchTimestamps)+account.pendingReservations >= policy.DispatchesPerWindowPerAccount {
		return false
	}
	account.pendingReservations++
	return true
}

// finalizeDispatch anchors the window at the dispatch it authorizes.
func (m *coordinatorModel) finalizeDispatch(idx int, now time.Duration) {
	account := &m.accounts[idx]
	if account.pendingReservations > 0 {
		account.pendingReservations--
	}
	account.dispatchTimestamps = append(account.dispatchTimestamps, now)
}

func (m *coordinatorModel) releasePendingRateSlot(idx int) {
	account := &m.accounts[idx]
	if account.pendingReservations > 0 {
		account.pendingReservations--
	}
}

// releaseLease frees what a lease still owns. An open lease owes both a
// pending slot and an in-flight slot; one already finalized owes only the
// in-flight slot, because its timestamp is in the window and is never
// refunded.
func (m *coordinatorModel) releaseLease(idx int, wasOpen bool) {
	account := &m.accounts[idx]
	if wasOpen && account.pendingReservations > 0 {
		account.pendingReservations--
	}
	if account.inFlight > 0 {
		account.inFlight--
	}
}

func (m *coordinatorModel) incrementInFlight(idx int) int {
	m.accounts[idx].inFlight++
	return m.accounts[idx].inFlight
}

// decrementInFlight carries no floor: pairing a decrement with an earlier
// increment is the caller's contract, not something this rule enforces.
func (m *coordinatorModel) decrementInFlight(idx int) int {
	m.accounts[idx].inFlight--
	return m.accounts[idx].inFlight
}

// apply429 is the 429 row of the health matrix: history append, gate
// advance clamped at ten minutes and never shortened, and on the third
// 429 inside one window the cooldown circuit with its own floor.
func (m *coordinatorModel) apply429(idx int, now time.Duration, gateDelay time.Duration) bool {
	account := &m.accounts[idx]
	if account.health == HealthDisabled {
		return false
	}

	account.recent429s = pruneBefore(account.recent429s, now-policy.RollingRateWindow)
	account.recent429s = append(account.recent429s, now)

	advance := gateDelay
	if advance > retryAfterClamp {
		advance = retryAfterClamp
	}
	gate := now + advance

	entered := false
	if len(account.recent429s) >= cooldownThreshold {
		account.health = HealthCoolingDown
		if floor := now + cooldownGateFloor; gate < floor {
			gate = floor
		}
		entered = true
	}
	if gate > account.rateGateDeadline {
		account.rateGateDeadline = gate
	}
	return entered
}

// expireGateIfDue clears a gate whose instant has passed. Only a gate the
// cooldown circuit opened returns the account to enabled and clears the
// history; clearing after a single 429 would reset the count on the first
// one and the threshold could never be reached.
func (m *coordinatorModel) expireGateIfDue(idx int, now time.Duration) {
	account := &m.accounts[idx]
	if account.rateGateDeadline == 0 || now < account.rateGateDeadline {
		return
	}

	wasCoolingDown := account.health == HealthCoolingDown
	account.rateGateDeadline = 0
	if wasCoolingDown {
		account.health = HealthEnabled
		account.recent429s = nil
	}
}

// disable is terminal, and it takes every pin to the account with it:
// provisional as well as confirmed, since a straggler must not be able to
// confirm affinity to a dead account.
func (m *coordinatorModel) disable(idx int) {
	m.accounts[idx].health = HealthDisabled
	account := accountFromIndex(idx)
	for key, pin := range m.pins {
		if pin.account == account {
			delete(m.pins, key)
		}
	}
}

// noteSessionOp counts one session operation and sweeps expired confirmed
// pins every sessionSweepInterval operations. Provisional pins have no
// wall expiry and leave by holder release instead.
func (m *coordinatorModel) noteSessionOp(wall time.Time) {
	m.sessionOps++
	if m.sessionOps < sessionSweepInterval {
		return
	}
	m.sessionOps = 0

	for key, pin := range m.pins {
		if pin.state == PinConfirmed && !wall.Before(pin.expiry) {
			delete(m.pins, key)
		}
	}
}

func (m *coordinatorModel) pinAccount(key SessionKey, wall time.Time) (catalog.Account, bool) {
	m.noteSessionOp(wall)

	pin, ok := m.pins[key]
	if !ok || pin.state != PinConfirmed {
		return "", false
	}
	if !wall.Before(pin.expiry) {
		delete(m.pins, key)
		return "", false
	}
	return pin.account, true
}

// nextArrivalSequence hands out the session's next arrival sequence. The
// counter lives in the pin, so a session with no pin starts over at 1.
func (m *coordinatorModel) nextArrivalSequence(key SessionKey) uint64 {
	pin, ok := m.pins[key]
	if !ok {
		return 1
	}
	sequence := pin.nextArrival
	pin.nextArrival++
	m.pins[key] = pin
	return sequence
}

// confirmPin moves a pin after a fully successful response. A request
// older than the one that last established the pin is refused, and a
// session with no pin earns none once the map is at its ceiling.
func (m *coordinatorModel) confirmPin(key SessionKey, account catalog.Account, sequence uint64, wall time.Time) {
	m.noteSessionOp(wall)

	pin, ok := m.pins[key]
	if ok && sequence < pin.sequence {
		return
	}
	if !ok && len(m.pins) >= policy.LiveSessionPins {
		return
	}

	nextArrival := sequence + 1
	if ok && pin.nextArrival > nextArrival {
		nextArrival = pin.nextArrival
	}

	m.pins[key] = modelPin{
		account:     account,
		expiry:      wall.Add(policy.SessionAffinityTTL),
		sequence:    sequence,
		nextArrival: nextArrival,
		state:       PinConfirmed,
	}
}

func (m *coordinatorModel) removePin(key SessionKey) {
	delete(m.pins, key)
}

// releaseProvisionalHolder drops one hold, and removes the pin outright
// when the last holder of an unconfirmed generation lets go.
func (m *coordinatorModel) releaseProvisionalHolder(key SessionKey, generation uint64, wall time.Time) {
	m.noteSessionOp(wall)

	pin, ok := m.pins[key]
	if !ok || pin.state != PinProvisional || pin.generation != generation {
		return
	}
	pin.holders--
	if pin.holders <= 0 {
		delete(m.pins, key)
		return
	}
	m.pins[key] = pin
}

// recoverPins installs confirmed pins at the expiry a live pin
// established at the same instant would have. A finish instant in the
// future is clamped, which is the one place the wall clock moving
// backwards is a case rather than an impossibility.
func (m *coordinatorModel) recoverPins(pins []RecoveredPin, wall time.Time) int {
	clamped := 0
	for _, pin := range pins {
		finishedAt := pin.FinishedAt
		if finishedAt.After(wall) {
			finishedAt = wall
			clamped++
		}
		m.pins[pin.Key] = modelPin{
			account:     pin.Account,
			expiry:      finishedAt.Add(policy.SessionAffinityTTL),
			sequence:    0,
			nextArrival: 1,
			state:       PinConfirmed,
		}
	}
	return clamped
}

// selectNewSessionPass is one pass of new-session selection: attach to the
// session's provisional pin if it has one, otherwise reserve in the
// candidate order and install the pin atomically with the reservation.
// Only one pass is modelled because the driver always hands the real
// coordinator a context that has already ended, so the wait after a failed
// pass returns immediately and never loops. It reports the reservation it
// granted, the pin the request acquired, and whether the pin map was full.
func (m *coordinatorModel) selectNewSessionPass(
	key SessionKey,
	order []int,
	now time.Duration,
	wall time.Time,
) (reserved bool, reservedIdx int, pin ProvisionalPin, pinMapFull bool) {
	if existing, ok := m.pins[key]; ok && existing.state == PinProvisional {
		existing.holders++
		m.pins[key] = existing
		pin = ProvisionalPin{
			Account:    existing.account,
			Sequence:   m.nextArrivalSequence(key),
			Generation: existing.generation,
		}

		idx := accountIndex(existing.account)
		outcome := m.reserve(idx, now)
		if outcome == Reserved {
			return true, idx, pin, false
		}
		if outcome != SkippedDisabled {
			return false, 0, pin, false
		}
		// The pinned account is disabled, so the provisional pin is dead
		// and selection falls through to the ordinary flow.
		delete(m.pins, key)
	}

	for _, idx := range order {
		if m.reserve(idx, now) != Reserved {
			continue
		}

		m.noteSessionOp(wall)
		if len(m.pins) >= policy.LiveSessionPins {
			return true, idx, ProvisionalPin{}, true
		}

		account := accountFromIndex(idx)
		sequence := m.nextArrivalSequence(key)
		m.pins[key] = modelPin{
			account:     account,
			sequence:    sequence,
			nextArrival: sequence + 1,
			state:       PinProvisional,
			generation:  1,
			holders:     1,
		}
		return true, idx, ProvisionalPin{Account: account, Sequence: sequence, Generation: 1}, false
	}
	return false, 0, pin, false
}
