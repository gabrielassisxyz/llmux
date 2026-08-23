package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// startupWall is the wall instant freshCoordinator's fake clock starts at.
var startupWall = time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)

func TestRecoverPinsInstallsConfirmedPin(t *testing.T) {
	c, _ := freshCoordinator()
	key := SessionKey{1, 2, 3}

	clamped := c.RecoverPins([]RecoveredPin{
		{Key: key, Account: catalog.AccountK2, FinishedAt: startupWall.Add(-30 * time.Minute)},
	})
	if clamped != 0 {
		t.Errorf("clamped = %d, want 0", clamped)
	}
	if got, ok := c.PinAccount(key); !ok || got != catalog.AccountK2 {
		t.Errorf("PinAccount = %q, %v; want k2, true", got, ok)
	}
}

// TestRecoverPinsExpiryIsCompletionBased proves the recovered pin's expiry
// is anchored at the recovered completion instant, not at the restart
// instant: a completion 30 minutes before startup must expire 30 minutes
// after startup, not a full hour after it.
func TestRecoverPinsExpiryIsCompletionBased(t *testing.T) {
	c, _ := freshCoordinator()
	key := SessionKey{1, 2, 3}
	finishedAt := startupWall.Add(-30 * time.Minute)

	c.RecoverPins([]RecoveredPin{{Key: key, Account: catalog.AccountK2, FinishedAt: finishedAt}})

	want := finishedAt.Add(policy.SessionAffinityTTL)
	if got := c.pins[key].expiry; !got.Equal(want) {
		t.Errorf("expiry = %v, want %v (completion + TTL, not startup + TTL)", got, want)
	}
}

// TestRecoverPinsExpiryMatchesLivePin proves a recovered pin expires at the
// same wall instant as a pin established live at the same completion time.
func TestRecoverPinsExpiryMatchesLivePin(t *testing.T) {
	key := SessionKey{1, 2, 3}
	finishedAt := startupWall.Add(-30 * time.Minute)

	live, liveClock := freshCoordinator()
	liveClock.AdvanceWall(-30 * time.Minute)
	live.ConfirmPin(key, catalog.AccountK2, 1)

	restarted, _ := freshCoordinator()
	restarted.RecoverPins([]RecoveredPin{{Key: key, Account: catalog.AccountK2, FinishedAt: finishedAt}})

	if got, want := restarted.pins[key].expiry, live.pins[key].expiry; !got.Equal(want) {
		t.Errorf("recovered expiry = %v, want %v (the live pin's expiry)", got, want)
	}
}

// TestRecoverPinsWallStepMovesBothAlike proves the recovered expiry is a wall
// instant rather than a monotonic deadline: stepping the wall clock forward
// moves a recovered pin and a live pin to expiry at the same instant, so
// nothing on the recovery path converted a stored instant into a deadline on
// another clock.
func TestRecoverPinsWallStepMovesBothAlike(t *testing.T) {
	key := SessionKey{1, 2, 3}
	finishedAt := startupWall.Add(-30 * time.Minute)

	live, liveClock := freshCoordinator()
	liveClock.AdvanceWall(-30 * time.Minute)
	live.ConfirmPin(key, catalog.AccountK2, 1)

	restarted, restartedClock := freshCoordinator()
	restarted.RecoverPins([]RecoveredPin{{Key: key, Account: catalog.AccountK2, FinishedAt: finishedAt}})

	// Both pins expire at 12:30. Step each clock to one second before that.
	liveClock.AdvanceWall(59*time.Minute + 59*time.Second)
	restartedClock.AdvanceWall(29*time.Minute + 59*time.Second)
	if _, ok := live.PinAccount(key); !ok {
		t.Errorf("live pin expired before its wall expiry")
	}
	if _, ok := restarted.PinAccount(key); !ok {
		t.Errorf("recovered pin expired before its wall expiry")
	}

	// One more second: both expire at the same wall instant.
	liveClock.AdvanceWall(time.Second)
	restartedClock.AdvanceWall(time.Second)
	if _, ok := live.PinAccount(key); ok {
		t.Errorf("live pin still live at its wall expiry")
	}
	if _, ok := restarted.PinAccount(key); ok {
		t.Errorf("recovered pin still live at its wall expiry")
	}
}

// TestRecoverPinsClampsFutureTimestamp proves a recovered completion that
// lands in the future is clamped to startup time and counted, so the caller
// can warn, and the pin then expires one TTL after startup rather than one
// TTL after the bogus future instant.
func TestRecoverPinsClampsFutureTimestamp(t *testing.T) {
	c, _ := freshCoordinator()
	key := SessionKey{1, 2, 3}
	future := startupWall.Add(2 * time.Hour)

	clamped := c.RecoverPins([]RecoveredPin{{Key: key, Account: catalog.AccountK1, FinishedAt: future}})
	if clamped != 1 {
		t.Errorf("clamped = %d, want 1", clamped)
	}
	want := startupWall.Add(policy.SessionAffinityTTL)
	if got := c.pins[key].expiry; !got.Equal(want) {
		t.Errorf("expiry = %v, want %v (startup + TTL after clamping)", got, want)
	}
}

// TestRecoverPinsRecoversNothingElse proves recovery touches only the pin
// map: rate state, in-flight counts and disabled health all stay at their
// fresh-process values, and no upstream request is possible from this
// package.
func TestRecoverPinsRecoversNothingElse(t *testing.T) {
	c, _ := freshCoordinator()
	c.RecoverPins([]RecoveredPin{
		{Key: SessionKey{1}, Account: catalog.AccountK1, FinishedAt: startupWall.Add(-30 * time.Minute)},
	})

	for _, account := range []catalog.Account{catalog.AccountK1, catalog.AccountK2, catalog.AccountK3} {
		state := &c.accounts[accountIndex(account)]
		if len(state.dispatchTimestamps) != 0 || state.pendingReservations != 0 {
			t.Errorf("%s rate state = %d timestamps, %d pending; want empty", account, len(state.dispatchTimestamps), state.pendingReservations)
		}
		if state.inFlight != 0 {
			t.Errorf("%s inFlight = %d, want 0", account, state.inFlight)
		}
		if state.health != HealthEnabled {
			t.Errorf("%s health = %v, want HealthEnabled", account, state.health)
		}
	}
}

// TestRecoverPinsAllowsLiveSequenceUpdate proves a recovered pin carries no
// live arrival sequence: the next request receives sequence 1, and a live
// confirmation at that sequence wins over the recovered pin rather than
// being refused as stale.
func TestRecoverPinsAllowsLiveSequenceUpdate(t *testing.T) {
	c, _ := freshCoordinator()
	key := SessionKey{1, 2, 3}
	c.RecoverPins([]RecoveredPin{{Key: key, Account: catalog.AccountK1, FinishedAt: startupWall.Add(-30 * time.Minute)}})

	seq := c.NextArrivalSequence(key)
	if seq != 1 {
		t.Fatalf("NextArrivalSequence = %d, want 1", seq)
	}
	c.ConfirmPin(key, catalog.AccountK2, seq)
	if got, ok := c.PinAccount(key); !ok || got != catalog.AccountK2 {
		t.Errorf("PinAccount = %q, %v; want k2, true after live confirmation", got, ok)
	}
}
