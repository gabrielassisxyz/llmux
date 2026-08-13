package testsupport

import (
	"sync"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/clock"
)

// FakeClock advances wall and monotonic time independently.
type FakeClock struct {
	mu        sync.Mutex
	wall      time.Time
	monotonic time.Duration
	timers    []*FakeTimer
}

// NewFakeClock constructs a fake at the supplied wall-clock instant.
func NewFakeClock(wall time.Time) *FakeClock {
	return &FakeClock{wall: wall.UTC()}
}

func (clock *FakeClock) WallNow() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.wall
}

func (clock *FakeClock) MonotonicNow() time.Duration {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.monotonic
}

func (clock *FakeClock) NewTimer(after time.Duration) clock.Timer {
	clock.mu.Lock()
	defer clock.mu.Unlock()

	timer := &FakeTimer{
		clock:    clock,
		deadline: clock.monotonic + after,
		channel:  make(chan time.Time, 1),
	}
	clock.timers = append(clock.timers, timer)
	clock.fireDueTimers()
	return timer
}

// AdvanceWall changes only the fake wall clock and permits backward movement.
func (clock *FakeClock) AdvanceWall(by time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.wall = clock.wall.Add(by)
}

// AdvanceMonotonic moves elapsed time forward and fires newly due timers.
func (clock *FakeClock) AdvanceMonotonic(by time.Duration) {
	if by < 0 {
		panic("monotonic time cannot move backward")
	}
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.monotonic += by
	clock.fireDueTimers()
}

func (clock *FakeClock) fireDueTimers() {
	for _, timer := range clock.timers {
		if timer.stopped || timer.fired || timer.deadline > clock.monotonic {
			continue
		}
		timer.fired = true
		timer.channel <- clock.wall
	}
}

// FakeTimer is the cancellable timer created by FakeClock.
type FakeTimer struct {
	clock    *FakeClock
	deadline time.Duration
	channel  chan time.Time
	stopped  bool
	fired    bool
}

func (timer *FakeTimer) C() <-chan time.Time {
	return timer.channel
}

func (timer *FakeTimer) Stop() bool {
	timer.clock.mu.Lock()
	defer timer.clock.mu.Unlock()
	if timer.stopped || timer.fired {
		return false
	}
	timer.stopped = true
	return true
}

// FixedPermutationSource returns an explicit permutation for deterministic tests.
type FixedPermutationSource struct {
	Values []int
}

func (source FixedPermutationSource) Perm(n int) []int {
	if len(source.Values) != n {
		panic("fixed permutation has an unexpected length")
	}
	return append([]int(nil), source.Values...)
}
