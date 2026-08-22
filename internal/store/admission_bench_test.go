package store_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/store"
)

// BenchmarkInsertDispatchAdmission measures the durable admission commit under
// the concurrency the in-flight ceilings permit: one writer for a baseline and
// 36 concurrent writers across three accounts.
//
// Run with LLMUX_BENCH_DB_DIR pointed at the filesystem the store actually
// runs on (the default is the user cache directory, so the database never
// lands on a tmpfs-backed temporary directory), with -benchmem and -count>1,
// and compare repeated runs with benchstat rather than reading a single
// number.
//
// The per-iteration latency metric excludes identifier construction: the
// p50/p95/p99 values bound only the InsertDispatchAdmission call itself.
func BenchmarkInsertDispatchAdmission(b *testing.B) {
	benchDir := benchDatabaseDir(b)
	accounts := []string{"k1", "k2", "k3"}

	for _, concurrency := range []int{1, 36} {
		b.Run(fmt.Sprintf("concurrency=%d", concurrency), func(b *testing.B) {
			dir, err := os.MkdirTemp(benchDir, fmt.Sprintf("c%d-", concurrency))
			if err != nil {
				b.Fatalf("mkdir temp: %v", err)
			}

			path := filepath.Join(dir, "llmux.db")
			s, err := store.Open(path)
			if err != nil {
				b.Fatalf("open store: %v", err)
			}

			oldProcs := runtime.GOMAXPROCS(concurrency)
			defer runtime.GOMAXPROCS(oldProcs)

			var attemptSeq int64
			var firstErr atomic.Pointer[error]
			latencies := make(chan []int64, concurrency)

			b.RunParallel(func(pb *testing.PB) {
				local := make([]int64, 0, 1024)
				for pb.Next() {
					n := atomic.AddInt64(&attemptSeq, 1)
					admission := store.DispatchAdmission{
						AttemptID:        "attempt-" + strconv.FormatInt(n, 10),
						LogicalRequestID: "request-" + strconv.FormatInt(n, 10),
						AttemptNo:        1,
						AccountLabel:     accounts[n%int64(len(accounts))],
						RequestedAlias:   "benchmark-alias",
						UpstreamModel:    "benchmark-model",
						ReservedAtUS:     n,
						LimiterRPMUsed:   0,
						LimiterInFlight:  0,
					}
					opStart := time.Now()
					err := s.InsertDispatchAdmission(context.Background(), admission)
					lat := time.Since(opStart)
					local = append(local, lat.Nanoseconds())
					if err != nil {
						e := err
						firstErr.CompareAndSwap(nil, &e)
					}
				}
				latencies <- local
			})
			close(latencies)

			if errPtr := firstErr.Load(); errPtr != nil {
				_ = s.Close()
				_ = os.RemoveAll(dir)
				b.Fatalf("insert dispatch admission: %v", *errPtr)
			}

			allLatencies := mergeLatencySlices(latencies)
			reportLatencyPercentiles(b, allLatencies)

			if err := s.Close(); err != nil {
				b.Fatalf("close store: %v", err)
			}
			_ = os.RemoveAll(dir)
		})
	}
}

func benchDatabaseDir(b *testing.B) string {
	b.Helper()

	dir := os.Getenv("LLMUX_BENCH_DB_DIR")
	if dir == "" {
		cache, err := os.UserCacheDir()
		if err != nil {
			b.Fatalf("user cache dir: %v", err)
		}
		dir = filepath.Join(cache, "llmux-bench")
	}

	if err := os.MkdirAll(dir, 0700); err != nil {
		b.Fatalf("mkdir %s: %v", dir, err)
	}
	return dir
}

func mergeLatencySlices(ch chan []int64) []int64 {
	var merged []int64
	for local := range ch {
		merged = append(merged, local...)
	}
	sort.Slice(merged, func(i, j int) bool { return merged[i] < merged[j] })
	return merged
}

func reportLatencyPercentiles(b *testing.B, sorted []int64) {
	if len(sorted) == 0 {
		return
	}
	b.ReportMetric(float64(percentile(sorted, 0.50))/1e6, "p50-ms")
	b.ReportMetric(float64(percentile(sorted, 0.95))/1e6, "p95-ms")
	b.ReportMetric(float64(percentile(sorted, 0.99))/1e6, "p99-ms")
}

func percentile(sorted []int64, p float64) time.Duration {
	idx := int(float64(len(sorted)-1) * p)
	return time.Duration(sorted[idx])
}
