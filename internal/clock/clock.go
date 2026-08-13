// Package clock defines time and permutation boundaries used by the proxy.
package clock

import (
	"math/rand/v2"
	"time"
)

// Clock keeps persisted instants and elapsed durations on distinct reads.
type Clock interface {
	WallNow() time.Time
	MonotonicNow() time.Duration
	NewTimer(after time.Duration) Timer
}

// Timer is a cancellable single-shot timer.
type Timer interface {
	C() <-chan time.Time
	Stop() bool
}

// PermutationSource makes account selection deterministic in tests.
type PermutationSource interface {
	Perm(n int) []int
}

// RealClock uses the process wall clock and its monotonic elapsed clock.
type RealClock struct {
	startedAt time.Time
}

// NewRealClock constructs the production clock boundary.
func NewRealClock() RealClock {
	return RealClock{startedAt: time.Now()}
}

func (clock RealClock) WallNow() time.Time {
	return time.Now().UTC()
}

func (clock RealClock) MonotonicNow() time.Duration {
	return time.Since(clock.startedAt)
}

func (clock RealClock) NewTimer(after time.Duration) Timer {
	return realTimer{timer: time.NewTimer(after)}
}

// RandomPermutationSource supplies production account permutations.
type RandomPermutationSource struct{}

func (RandomPermutationSource) Perm(n int) []int {
	return rand.Perm(n)
}

type realTimer struct {
	timer *time.Timer
}

func (timer realTimer) C() <-chan time.Time {
	return timer.timer.C
}

func (timer realTimer) Stop() bool {
	return timer.timer.Stop()
}
