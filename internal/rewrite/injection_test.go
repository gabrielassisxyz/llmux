package rewrite

import (
	"io"
	"testing"
)

func TestApplyInjectionOverridesExistingValue(t *testing.T) {
	plan, err := ApplyInjection([]byte(`{"messages":[{"content":"\\u003craw\\u003e"}],"reasoning_effort":"low","other":1e+9}`), "reasoning_effort", "max")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(plan.Reader())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"messages":[{"content":"\\u003craw\\u003e"}],"reasoning_effort":"max","other":1e+9}`
	if string(got) != want {
		t.Fatalf("rewrite = %s", got)
	}
}

func TestApplyInjectionAppendsMissingValue(t *testing.T) {
	plan, err := ApplyInjection([]byte(`{"model":"x","stream_options":{"include_usage":true}}`), "reasoning_effort", "high")
	if err != nil {
		t.Fatal(err)
	}
	got, err := io.ReadAll(plan.Reader())
	if err != nil {
		t.Fatal(err)
	}
	want := `{"model":"x","stream_options":{"include_usage":true},"reasoning_effort":"high"}`
	if string(got) != want {
		t.Fatalf("rewrite = %s", got)
	}
}
