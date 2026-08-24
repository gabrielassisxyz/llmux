package route

import (
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
	"github.com/gabrielassisxyz/llmux/internal/clock"
	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/testsupport"
)

// TestHealthMatrixIsCompleteAndUnambiguous proves the table is a closed,
// duplicate-free source of truth: exactly the fourteen events, each once, with
// no unknown event and no zero-value row.
func TestHealthMatrixIsCompleteAndUnambiguous(t *testing.T) {
	if len(healthEventNames) != int(HealthEventProcessRestart)+1 {
		t.Fatalf("healthEventNames has %d entries, want %d", len(healthEventNames), int(HealthEventProcessRestart)+1)
	}
	if len(HealthMatrix) != len(healthEventNames) {
		t.Fatalf("HealthMatrix has %d rows, want %d", len(HealthMatrix), len(healthEventNames))
	}

	seen := make(map[HealthEvent]bool, len(HealthMatrix))
	for _, row := range HealthMatrix {
		if row.Event < HealthEvent401 || row.Event > HealthEventProcessRestart {
			t.Errorf("row has unknown event %d", row.Event)
			continue
		}
		if seen[row.Event] {
			t.Errorf("duplicate event %s", row.Event)
		}
		seen[row.Event] = true
		if row.Effects == "" {
			t.Errorf("%s has an empty Effects column", row.Event)
		}
	}
	for e := HealthEvent401; e <= HealthEventProcessRestart; e++ {
		if !seen[e] {
			t.Errorf("missing event %s", e)
		}
	}
}

// TestHealthMatrixUnitRowsDriveTheCoordinator drives every row that has a
// coordinator mutation through the real method and asserts the health, gate
// and history columns against the table. The none rows are relay-side and are
// exercised at the full-handler level, not here. Driving every non-none row,
// and nothing else, is what turns a new mutation kind added to the table
// without a driver into a test failure instead of a silently absent case.
func TestHealthMatrixUnitRowsDriveTheCoordinator(t *testing.T) {
	driven := 0
	nonNone := 0
	for _, row := range HealthMatrix {
		if row.Mutation == MutationNone {
			continue
		}
		nonNone++
		row := row
		t.Run(row.Event.String(), func(t *testing.T) {
			c, target, beforeHealth, beforeGate, beforeHistory, wantGate := driveHealthRow(t, row)
			assertHealthRow(t, c, target, beforeHealth, beforeGate, beforeHistory, wantGate, row)
			driven++
		})
	}
	if driven != nonNone {
		t.Fatalf("drove %d rows, want %d (a mutation kind lacks a driver)", driven, nonNone)
	}
}

// driveHealthRow sets up a fresh coordinator, applies the row's mutation and
// returns the coordinator, the target account, the state immediately before
// the mutation, and the exact gate deadline the advanced and floored rows
// should leave behind. The pre-mutation snapshot is taken immediately before
// the mutation so HealthUnchanged is asserted against the state the mutation
// actually found.
func driveHealthRow(t *testing.T, row HealthRow) (c *Coordinator, target catalog.Account, beforeHealth HealthState, beforeGate time.Duration, beforeHistory int, wantGate time.Duration) {
	t.Helper()
	target = catalog.AccountK1

	switch row.Mutation {
	case MutationRestart:
		fake := testsupport.NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
		c = NewCoordinator(testKeys(), fake, clock.RandomPermutationSource{})
		return c, target, HealthEnabled, 0, 0, 0

	case MutationDisable:
		c, _ = newRateTestCoordinator()
		beforeHealth, beforeGate, beforeHistory = snapshotHealth(c, target)
		c.Disable(target)
		return c, target, beforeHealth, beforeGate, beforeHistory, 0

	case MutationApply429:
		c, fake := newRateTestCoordinator()
		if row.Event == HealthEvent429Third {
			c.Apply429(target, "1", time.Time{}, false)
			c.Apply429(target, "1", time.Time{}, false)
			beforeHealth, beforeGate, beforeHistory = snapshotHealth(c, target)
			before := fake.MonotonicNow()
			c.Apply429(target, "1", time.Time{}, false)
			return c, target, beforeHealth, beforeGate, beforeHistory, before + policy.RollingRateWindow
		}
		beforeHealth, beforeGate, beforeHistory = snapshotHealth(c, target)
		before := fake.MonotonicNow()
		c.Apply429(target, "45", time.Time{}, false)
		return c, target, beforeHealth, beforeGate, beforeHistory, before + 45*time.Second

	case MutationExpireGateIfDue:
		c, fake := newRateTestCoordinator()
		if row.Event == HealthEventCooldownGateExpiry {
			c.Apply429(target, "1", time.Time{}, false)
			c.Apply429(target, "1", time.Time{}, false)
			c.Apply429(target, "1", time.Time{}, false)
		} else {
			c.Apply429(target, "5", time.Time{}, false)
			c.Apply429(target, "5", time.Time{}, false)
		}
		beforeHealth, beforeGate, beforeHistory = snapshotHealth(c, target)
		fake.AdvanceMonotonic(2 * time.Minute)
		c.ExpireGateIfDue(target)
		return c, target, beforeHealth, beforeGate, beforeHistory, 0

	default:
		t.Fatalf("no unit driver for mutation %d", row.Mutation)
		return nil, "", 0, 0, 0, 0
	}
}

// assertHealthRow checks the coordinator's post-mutation state against the
// row's three state columns.
func assertHealthRow(t *testing.T, c *Coordinator, target catalog.Account, beforeHealth HealthState, beforeGate time.Duration, beforeHistory int, wantGate time.Duration, row HealthRow) {
	t.Helper()
	gotHealth := c.Health(target)
	gotGate, gotHistory := snapshotGateHistory(c, target)

	switch row.Health {
	case HealthUnchanged:
		if gotHealth != beforeHealth {
			t.Errorf("health = %v, want unchanged %v", gotHealth, beforeHealth)
		}
	case HealthBecomesEnabled:
		if gotHealth != HealthEnabled {
			t.Errorf("health = %v, want HealthEnabled", gotHealth)
		}
	case HealthBecomesCoolingDown:
		if gotHealth != HealthCoolingDown {
			t.Errorf("health = %v, want HealthCoolingDown", gotHealth)
		}
	case HealthBecomesDisabled:
		if gotHealth != HealthDisabled {
			t.Errorf("health = %v, want HealthDisabled", gotHealth)
		}
	}

	switch row.Gate {
	case GateUntouched:
		if gotGate != beforeGate {
			t.Errorf("gate deadline = %v, want untouched %v", gotGate, beforeGate)
		}
	case GateAdvanced, GateAdvancedFloored:
		if gotGate != wantGate {
			t.Errorf("gate deadline = %v, want %v", gotGate, wantGate)
		}
	case GateCleared:
		if gotGate != 0 {
			t.Errorf("gate deadline = %v, want 0", gotGate)
		}
	}

	switch row.History {
	case HistoryUntouched:
		if gotHistory != beforeHistory {
			t.Errorf("recent429s count = %d, want untouched %d", gotHistory, beforeHistory)
		}
	case HistoryAppend:
		if gotHistory != beforeHistory+1 {
			t.Errorf("recent429s count = %d, want %d", gotHistory, beforeHistory+1)
		}
	case HistoryCleared:
		if gotHistory != 0 {
			t.Errorf("recent429s count = %d, want 0", gotHistory)
		}
	}
}

// snapshotHealth reads the account's health, gate deadline and recent-429
// history length under the coordinator lock.
func snapshotHealth(c *Coordinator, account catalog.Account) (HealthState, time.Duration, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.accounts[accountIndex(account)]
	return state.health, state.rateGateDeadline, len(state.recent429s)
}

// snapshotGateHistory reads the account's gate deadline and recent-429
// history length under the coordinator lock.
func snapshotGateHistory(c *Coordinator, account catalog.Account) (time.Duration, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	state := &c.accounts[accountIndex(account)]
	return state.rateGateDeadline, len(state.recent429s)
}
