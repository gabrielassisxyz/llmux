package rewrite

import (
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestApplyInjectionPreservesEveryUntouchedByteAcrossHostileBodies(t *testing.T) {
	tests := []struct {
		name  string
		body  string
		field string
		value string
		want  string
	}{
		{
			name:  "owned member first",
			body:  `{"route_owned":"old","model":"m","messages":[{"content":"{[,:]\\\"\\\\"}],"unknown":1e+09,"unknown":-0}`,
			field: "route_owned",
			value: "new",
			want:  `{"route_owned":"new","model":"m","messages":[{"content":"{[,:]\\\"\\\\"}],"unknown":1e+09,"unknown":-0}`,
		},
		{
			name:  "owned member last with escaped spelling",
			body:  `{"model":"m","messages":[{"tool_calls":[{"arguments":"{\\\"x\\\":[1,2]}"}]}],"route_\u006fwned":"old"}`,
			field: "route_owned",
			value: "new",
			want:  `{"model":"m","messages":[{"tool_calls":[{"arguments":"{\\\"x\\\":[1,2]}"}]}],"route_\u006fwned":"new"}`,
		},
		{
			name:  "missing member appends without changing stream spelling",
			body:  `{"str\u0065am":true,"model":"m","messages":[],"opaque":{"braces":"{}[],:"}}`,
			field: "route_owned",
			value: "new",
			want:  `{"str\u0065am":true,"model":"m","messages":[],"opaque":{"braces":"{}[],:"},"route_owned":"new"}`,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan, err := ApplyInjection([]byte(test.body), test.field, test.value)
			if err != nil {
				t.Fatalf("ApplyInjection() error = %v", err)
			}
			for attempt := range 4 {
				got, err := io.ReadAll(plan.Reader())
				if err != nil {
					t.Fatalf("attempt %d read = %v", attempt, err)
				}
				if !bytes.Equal(got, []byte(test.want)) {
					t.Fatalf("attempt %d bytes = %q, want %q", attempt, got, test.want)
				}
			}
		})
	}
}

func TestApplyInjectionRejectsDuplicatedEscapedOwnedMembers(t *testing.T) {
	_, err := ApplyInjection([]byte(`{"route_owned":"first","route_\u006fwned":"second","model":"m"}`), "route_owned", "new")
	if err != ErrMalformedJSON {
		t.Fatalf("ApplyInjection() error = %v, want ErrMalformedJSON", err)
	}
}

func TestApplyInjectionReplaysLargeOpaqueBodyWithoutChangingItsBytes(t *testing.T) {
	content := strings.Repeat("x", 8*1024*1024)
	body := []byte(`{"model":"m","messages":[{"content":"` + content + `"}],"route_owned":"old","number":1e+09}`)
	want := []byte(`{"model":"m","messages":[{"content":"` + content + `"}],"route_owned":"new","number":1e+09}`)

	plan, err := ApplyInjection(body, "route_owned", "new")
	if err != nil {
		t.Fatalf("ApplyInjection() error = %v", err)
	}
	for attempt := range 4 {
		got, err := io.ReadAll(plan.Reader())
		if err != nil {
			t.Fatalf("attempt %d read = %v", attempt, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("attempt %d changed large opaque body", attempt)
		}
	}
}
