package testsupport

import (
	"testing"
	"time"
)

func TestFakeClockAdvancesWallAndMonotonicIndependently(t *testing.T) {
	start := time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC)
	clock := NewFakeClock(start)

	clock.AdvanceMonotonic(time.Minute)
	if got := clock.WallNow(); !got.Equal(start) {
		t.Fatalf("WallNow() = %v, want unchanged %v", got, start)
	}
	if got := clock.MonotonicNow(); got != time.Minute {
		t.Fatalf("MonotonicNow() = %v, want %v", got, time.Minute)
	}

	clock.AdvanceWall(-time.Hour)
	if got := clock.MonotonicNow(); got != time.Minute {
		t.Fatalf("MonotonicNow() after wall rollback = %v, want %v", got, time.Minute)
	}
	if got := clock.WallNow(); !got.Equal(start.Add(-time.Hour)) {
		t.Fatalf("WallNow() = %v, want %v", got, start.Add(-time.Hour))
	}
}

func TestFakeClockTimersUseOnlyMonotonicTime(t *testing.T) {
	clock := NewFakeClock(time.Date(2026, time.August, 13, 12, 0, 0, 0, time.UTC))
	timer := clock.NewTimer(time.Minute)

	clock.AdvanceWall(24 * time.Hour)
	select {
	case <-timer.C():
		t.Fatal("timer fired after wall-clock change")
	default:
	}

	clock.AdvanceMonotonic(time.Minute)
	select {
	case <-timer.C():
	default:
		t.Fatal("timer did not fire after monotonic advance")
	}
}

func TestFakeTimerCanBeStopped(t *testing.T) {
	clock := NewFakeClock(time.Now())
	timer := clock.NewTimer(time.Minute)
	if !timer.Stop() {
		t.Fatal("first Stop() = false, want true")
	}
	if timer.Stop() {
		t.Fatal("second Stop() = true, want false")
	}
	clock.AdvanceMonotonic(time.Minute)
	select {
	case <-timer.C():
		t.Fatal("stopped timer fired")
	default:
	}
}

func TestFixedPermutationSourceReturnsCopy(t *testing.T) {
	source := FixedPermutationSource{Values: []int{2, 0, 1}}
	got := source.Perm(3)
	got[0] = 9
	if source.Values[0] != 2 {
		t.Fatalf("source.Values[0] = %d, want 2", source.Values[0])
	}
}
