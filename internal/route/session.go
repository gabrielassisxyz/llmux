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

	// generation identifies the provisional generation a holder is attached
	// to. It is per-session and resets when the pin is removed, so it is 1
	// for every provisional pin a session creates. A global counter that
	// survives removal is a later refinement for cross-recreation
	// uniqueness; the release-once discipline of the dispatch path makes
	// the per-session value sufficient today.
	generation uint64

	// holders counts the requests currently holding the provisional
	// generation. It is meaningful only while state is PinProvisional.
	holders int
}

// PinAccount returns the live confirmed pin for a session, if one exists.
// An expired pin is removed lazily here rather than by any background
// sweep. A provisional pin is not a live pin: it has never succeeded and
// earns no affinity.
func (c *Coordinator) PinAccount(key SessionKey) (catalog.Account, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.noteSessionOpLocked()

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
	return c.nextArrivalLocked(key)
}

// nextArrivalLocked returns the next arrival sequence for a session and
// advances the counter. The caller must hold c.mu.
func (c *Coordinator) nextArrivalLocked(key SessionKey) uint64 {
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

	c.noteSessionOpLocked()

	pin, ok := c.pins[key]
	if ok && sequence < pin.sequence {
		return
	}
	if !ok && c.pinMapFullLocked() {
		// The map is at its ceiling and this session has no pin: refuse to
		// create one. The response still succeeded; it just earns no
		// affinity.
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
// unpinned flow. A pin that is temporarily saturated or cooling is given a
// reopen-aware bounded stall before the selection spills to an alternative;
// the spill is marked on the result so the caller can record its source.
func (c *Coordinator) SelectForSession(ctx context.Context, key SessionKey) SelectionResult {
	var skips []SkipDecision

	pinAccount, hasPin := c.PinAccount(key)
	if !hasPin {
		return c.Select(ctx)
	}

	lease, skip, outcome := c.stallForPin(ctx, key, pinAccount)
	switch outcome {
	case pinStallReserved:
		return SelectionResult{Lease: lease, Skips: skips, Outcome: SelectionReserved}
	case pinStallCanceled:
		return SelectionResult{Skips: append(skips, skip), Outcome: SelectionCanceled}
	}

	// pinStallSpill: the pin could not be preserved. Select among all
	// accounts and mark the dispatch as a spill when an alternative wins.
	// A pin that expired or was re-pinned mid-stall produces no skip row.
	if skip.Account != "" {
		skips = append(skips, skip)
	}
	result := c.Select(ctx)
	result.Skips = append(skips, result.Skips...)
	if result.Lease != nil && result.Lease.Account() != pinAccount {
		result.SpillFrom = pinAccount
	}
	return result
}
