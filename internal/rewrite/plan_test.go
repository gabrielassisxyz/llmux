package rewrite

import (
	"io"
	"testing"
)

func TestPlanPreservesUntouchedBytesAcrossReplays(t *testing.T) {
	body := []byte(`{"messages":[{"content":"\\u003craw\\u003e","n":-0}],"model":"old","unknown":1e+09,"unknown":2}`)
	modelStart := len(`{"messages":[{"content":"\\u003craw\\u003e","n":-0}],"model":`)
	plan, err := NewPlan(body, []Replacement{{Span: Span{Start: modelStart, End: modelStart + len(`"old"`)}, Value: []byte(`"new"`)}})
	if err != nil {
		t.Fatal(err)
	}
	want := `{"messages":[{"content":"\\u003craw\\u003e","n":-0}],"model":"new","unknown":1e+09,"unknown":2}`
	for attempt := range 4 {
		got, readErr := io.ReadAll(plan.Reader())
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Fatalf("attempt %d replay = %s", attempt, got)
		}
	}
	if plan.ContentLength() != int64(len(want)) {
		t.Fatalf("content length = %d, want %d", plan.ContentLength(), len(want))
	}
}
