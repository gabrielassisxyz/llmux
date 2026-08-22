package route

import (
	"context"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// SessionKey is the versioned keyed digest that identifies a session for
// affinity. It is the fixed-size value the proxy derives from the
// X-Session-ID header: one version byte followed by a 32-byte HMAC-SHA-256
// digest. The route package owns no header parsing and receives the digest
// already derived, so the raw identifier never reaches this package.
type SessionKey [33]byte

// PinState is the provisional/confirmed state of a session pin.
type PinState int

const (
	// PinProvisional marks a pin that has never been confirmed by a fully
	// successful response. It is held rather than owned: it lives only
	// while a request still uses it, and it earns no hour of affinity.
	PinProvisional PinState = iota
	// PinConfirmed marks a pin established by a fully successful response.
	PinConfirmed
)

// sessionPin is the per-session affinity record. Every field is read or
// written only while the Coordinator's mutex is held.
type sessionPin struct {
	// account is the pinned account.
	account catalog.Account

	// expiry is the wall-clock instant after which the pin is dead. It is
	// set to one hour after successful completion, never after arrival,
	// reservation, or Do.
	expiry time.Time

	// sequence is the request-arrival sequence of the request that last
	// established the pin. A pin update carrying a lower sequence is stale
	// and refused.
	sequence uint64

	// nextArrival is the request-arrival sequence the next request for this
	// session receives. It advances on every arrival, so concurrent
	// requests get distinct, ordered sequences.
	nextArrival uint64

	// state is provisional or confirmed.
	state PinState
}

// PinAccount returns the live confirmed pin for a session, if one exists.
// An expired pin is removed lazily here rather than by any background
// sweep. A provisional pin is not a live pin: it has never succeeded and
// earns no affinity.
func (c *Coordinator) PinAccount(key SessionKey) (catalog.Account, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if !ok || pin.state != PinConfirmed {
		return "", false
	}
	if !c.clk.WallNow().Before(pin.expiry) {
		delete(c.pins, key)
		return "", false
	}
	return pin.account, true
}

// NextArrivalSequence assigns and returns the request-arrival sequence for
// the next request in a session. The first request for a session receives
// sequence 1. The counter lives in the pin entry, so it survives exactly as
// long as the pin does; a session whose pin is removed starts over at 1.
func (c *Coordinator) NextArrivalSequence(key SessionKey) uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if !ok {
		return 1
	}
	seq := pin.nextArrival
	pin.nextArrival++
	c.pins[key] = pin
	return seq
}

// ConfirmPin updates the session pin after a fully successful response. It
// is the only path that moves a pin, and it is called for a confirmation, a
// successful spill, and a successful explicit -kN request alike: all three
// mean "the response fully succeeded, so this account now holds the newest
// complete prefix".
//
// sequence is the request's arrival sequence. A request older than the one
// that last established the pin is refused, so a stale concurrent
// completion cannot overwrite a newer pin update.
func (c *Coordinator) ConfirmPin(key SessionKey, account catalog.Account, sequence uint64) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if ok && sequence < pin.sequence {
		return
	}

	nextArrival := sequence + 1
	if ok && pin.nextArrival > nextArrival {
		nextArrival = pin.nextArrival
	}

	c.pins[key] = sessionPin{
		account:     account,
		expiry:      c.clk.WallNow().Add(policy.SessionAffinityTTL),
		sequence:    sequence,
		nextArrival: nextArrival,
		state:       PinConfirmed,
	}
}

// RemovePin removes a session pin. It is called when the pinned account is
// disabled, so the next request for the session selects a new account
// immediately rather than retrying the disabled one.
func (c *Coordinator) RemovePin(key SessionKey) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.pins, key)
}

// SelectForSession acquires a lease for a session, trying the live pin
// first. A live pin that is eligible is reserved without shuffling. A pin
// whose account is disabled is removed and selection falls through to the
// unpinned flow. A pin that is temporarily saturated or cooling falls
// through to the unpinned flow; the reopen-aware stall before spill is a
// later bead's policy layered on top of this lookup.
func (c *Coordinator) SelectForSession(ctx context.Context, key SessionKey) SelectionResult {
	var skips []SkipDecision
	if lease, skip, found := c.tryPinnedAccount(key); found {
		if lease != nil {
			return SelectionResult{Lease: lease, Skips: skips, Outcome: SelectionReserved}
		}
		skips = append(skips, skip)
	}

	result := c.Select(ctx)
	result.Skips = append(skips, result.Skips...)
	return result
}

// tryPinnedAccount attempts to reserve the live pin's account under one
// lock hold. It reports whether a live pin existed. A disabled pin is
// removed and reported as a skip; a saturated or cooling pin is reported as
// a skip and left in place.
func (c *Coordinator) tryPinnedAccount(key SessionKey) (lease *PendingLease, skip SkipDecision, found bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	pin, ok := c.pins[key]
	if !ok || pin.state != PinConfirmed {
		return nil, SkipDecision{}, false
	}
	if !c.clk.WallNow().Before(pin.expiry) {
		delete(c.pins, key)
		return nil, SkipDecision{}, false
	}

	lease, outcome := c.reserveLocked(pin.account)
	if lease != nil {
		return lease, SkipDecision{}, true
	}
	if outcome == SkippedDisabled {
		delete(c.pins, key)
	}
	return nil, SkipDecision{Account: pin.account, Reason: outcome}, true
}
