package testsupport

import (
	"runtime"
	"testing"
	"time"
)

// AssertNoGoroutineLeak runs fn and fails t when the goroutine count does
// not settle back to its pre-fn baseline within a short window. It is the
// leak detector the stress suite wraps around every subtest, since no
// goroutine-leak library is vendored in this module.
//
// fn must join every goroutine it starts before returning; the settle loop
// only absorbs the runtime's own lazily-spawned goroutines (GC workers,
// timer goroutines), not a leaked worker that never exits.
func AssertNoGoroutineLeak(t *testing.T, fn func()) {
	t.Helper()
	before := runtime.NumGoroutine()
	fn()

	deadline := time.Now().Add(3 * time.Second)
	for {
		if runtime.NumGoroutine() <= before {
			return
		}
		if time.Now().After(deadline) {
			t.Errorf("goroutine leak: %d goroutines before, %d after", before, runtime.NumGoroutine())
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}
