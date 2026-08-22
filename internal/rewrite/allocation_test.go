package rewrite

import (
	"io"
	"strings"
	"testing"
)

// TestReplayAllocatesNoBodySizedBuffer proves the replay does not copy the
// body. NewPlan slices the original body into segments and Reader returns
// small readers over those segments, so four replays of a 1 MiB body must
// allocate only the segment readers and the fixed copy buffer, never a
// second body-sized buffer. The proof is allocation accounting, not code
// inspection: a body copy would push the bytes-per-op past the body size.
func TestReplayAllocatesNoBodySizedBuffer(t *testing.T) {
	content := strings.Repeat("x", 1<<20)
	body := []byte(`{"model":"m","messages":[{"content":"` + content + `"}],"route_owned":"old"}`)
	plan, err := ApplyInjection(body, "route_owned", "new")
	if err != nil {
		t.Fatalf("ApplyInjection() error = %v", err)
	}

	result := testing.Benchmark(func(b *testing.B) {
		b.ReportAllocs()
		b.SetBytes(int64(len(body)))
		for i := 0; i < b.N; i++ {
			r := plan.Reader()
			if _, err := io.Copy(io.Discard, r); err != nil {
				b.Fatalf("replay read = %v", err)
			}
		}
	})

	if got := result.AllocedBytesPerOp(); got > int64(len(body)) {
		t.Errorf("replay allocated %d bytes/op, want under one body-sized buffer (%d)", got, len(body))
	}
}
