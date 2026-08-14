package rewrite

import (
	"errors"
	"strings"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestScan_MinimalValidBody(t *testing.T) {
	meta, err := Scan([]byte(`{"model":"kimi-k2.7","messages":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "kimi-k2.7" {
		t.Errorf("expected model %q, got %q", "kimi-k2.7", meta.Model)
	}
	if meta.HasStream {
		t.Errorf("expected no stream member")
	}
}

func TestScan_ArbitraryTopLevelFieldOrder(t *testing.T) {
	bodies := []string{
		`{"model":"a","messages":[],"stream":true}`,
		`{"messages":[],"model":"a","stream":true}`,
		`{"stream":true,"messages":[],"model":"a"}`,
		`{"messages":[],"stream":true,"model":"a"}`,
	}
	for _, b := range bodies {
		meta, err := Scan([]byte(b))
		if err != nil {
			t.Fatalf("body %q: unexpected error: %v", b, err)
		}
		if meta.Model != "a" || !meta.HasStream {
			t.Errorf("body %q: expected model=a, hasStream=true, got %+v", b, meta)
		}
	}
}

func TestScan_MessagesBeforeOrAfterModel(t *testing.T) {
	before := []byte(`{"messages":[{"role":"user","content":"hi"}],"model":"a"}`)
	after := []byte(`{"model":"a","messages":[{"role":"user","content":"hi"}]}`)
	for _, b := range [][]byte{before, after} {
		meta, err := Scan(b)
		if err != nil {
			t.Fatalf("body %q: unexpected error: %v", b, err)
		}
		if meta.Model != "a" {
			t.Errorf("body %q: expected model=a, got %q", b, meta.Model)
		}
	}
}

func TestScan_DeeplyNestedMessageStructures(t *testing.T) {
	body := []byte(`{"model":"a","messages":[{"role":"user","content":[{"type":"text","text":"hi","meta":{"a":{"b":{"c":[1,2,3]}}}}]}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_ToolCallsAndToolResults(t *testing.T) {
	body := []byte(`{"model":"a","messages":[{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"result"}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_UnknownMessageFields(t *testing.T) {
	body := []byte(`{"model":"a","messages":[{"role":"user","content":"hi","future_field":{"nested":true},"another":[1,2,3]}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_EscapedQuotesAndBackslashes(t *testing.T) {
	body := []byte(`{"model":"a","messages":[{"role":"user","content":"she said \"hi\" and used a \\backslash\\"}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_BracesAndBracketsInsideStrings(t *testing.T) {
	body := []byte(`{"model":"a","messages":[{"role":"user","content":"{not json} [also not]"}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_UnicodeAndSurrogateEscapes(t *testing.T) {
	// U+1F600 (grinning face) as a UTF-16 surrogate pair, plus a literal
	// multi-byte UTF-8 character, inside an opaque value.
	body := []byte(`{"model":"a","messages":[{"role":"user","content":"emoji 😀 and café"}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_EscapedModelName(t *testing.T) {
	// "model" decodes to "model".
	meta, err := Scan([]byte(`{"model":"a","messages":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_DuplicateModelViaEscapedSpelling(t *testing.T) {
	// "\u006dodel" decodes to "model", the same member as the plain spelling.
	body := "{\"model\":\"a\",\"\\u006dodel\":\"b\",\"messages\":[]}"
	_, err := Scan([]byte(body))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a duplicate escaped model, got %v", err)
	}
}

func TestScan_DuplicateStreamViaEscapedSpelling(t *testing.T) {
	// "\u0073tream" decodes to "stream", the same member as the plain spelling.
	body := "{\"model\":\"a\",\"stream\":true,\"\\u0073tream\":false,\"messages\":[]}"
	_, err := Scan([]byte(body))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a duplicate escaped stream, got %v", err)
	}
}

func TestScan_EscapedModelSpellingAloneIsRecognized(t *testing.T) {
	// A lone escaped spelling, with no plain-spelled duplicate, must still
	// resolve to the routed member and populate Metadata.
	body := "{\"\\u006dodel\":\"a\",\"messages\":[]}"
	meta, err := Scan([]byte(body))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_NumbersBooleansAndNull(t *testing.T) {
	body := []byte(`{"model":"a","messages":[],"temperature":0.7,"n":1,"top_p":1e-2,"neg":-0,"big":123456789012345,"stream":false,"reasoning":null}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" || !meta.HasStream {
		t.Errorf("expected model=a, hasStream=true, got %+v", meta)
	}
}

func TestScan_EmptyMessagesArray(t *testing.T) {
	meta, err := Scan([]byte(`{"model":"a","messages":[]}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_MissingMessages(t *testing.T) {
	meta, err := Scan([]byte(`{"model":"a"}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_MalformedJSONAtEveryStructuralPosition(t *testing.T) {
	cases := []string{
		``,
		`   `,
		`{`,
		`}`,
		`{"model"}`,
		`{"model":}`,
		`{"model":"a"`,
		`{"model":"a",}`,
		`{,"model":"a"}`,
		`{"model":"a" "messages":[]}`,
		`{"model":"a","messages":[}`,
		`{"model":"a","messages":[1,]}`,
		`{"model":"a","messages":[1 2]}`,
		`{model:"a"}`,
		`{'model':'a'}`,
		`{"model":"a",,"messages":[]}`,
		`{"model":tru}`,
		`{"model":"a","x":01}`,
		`{"model":"a","x":1.}`,
		`{"model":"a","x":.1}`,
		`{"model":"a","x":1e}`,
		`{"model":"a","x":"unterminated}`,
		`{"model":"a","x":"bad\escape"}`,
		`{"model":"a","x":"\u12"}`,
		`{"model":"a","x":"control` + string(rune(0x01)) + `char"}`,
	}
	for _, c := range cases {
		if _, err := Scan([]byte(c)); err == nil {
			t.Errorf("expected an error for malformed body %q, got none", c)
		}
	}
}

func TestScan_TrailingGarbage(t *testing.T) {
	_, err := Scan([]byte(`{"model":"a"}garbage`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for trailing garbage, got %v", err)
	}
}

func TestScan_TopLevelArrayRejected(t *testing.T) {
	_, err := Scan([]byte(`[{"model":"a"}]`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a top-level array, got %v", err)
	}
}

func TestScan_TopLevelScalarRejected(t *testing.T) {
	for _, body := range []string{`"a string"`, `42`, `true`, `null`} {
		if _, err := Scan([]byte(body)); !errors.Is(err, ErrMalformedJSON) {
			t.Errorf("body %q: expected ErrMalformedJSON, got %v", body, err)
		}
	}
}

func TestScan_MissingModelRejected(t *testing.T) {
	_, err := Scan([]byte(`{"messages":[]}`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a missing model, got %v", err)
	}
}

func TestScan_NonStringModelRejected(t *testing.T) {
	for _, body := range []string{
		`{"model":42}`,
		`{"model":true}`,
		`{"model":null}`,
		`{"model":{}}`,
		`{"model":[]}`,
	} {
		if _, err := Scan([]byte(body)); !errors.Is(err, ErrMalformedJSON) {
			t.Errorf("body %q: expected ErrMalformedJSON, got %v", body, err)
		}
	}
}

func TestScan_DuplicatePlainModelRejected(t *testing.T) {
	_, err := Scan([]byte(`{"model":"a","model":"b"}`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a plain duplicate model, got %v", err)
	}
}

func TestScan_DuplicatePlainStreamRejected(t *testing.T) {
	_, err := Scan([]byte(`{"model":"a","stream":true,"stream":false}`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a plain duplicate stream, got %v", err)
	}
}

func TestScan_DuplicateUnknownMemberIsNotRejected(t *testing.T) {
	// The proxy does not read unknown members, so a duplicate among them is
	// not this scanner's ambiguity to resolve.
	meta, err := Scan([]byte(`{"model":"a","custom":1,"custom":2}`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_DepthWithinBoundAccepted(t *testing.T) {
	// The top-level object is depth one; nest exactly enough arrays to
	// reach the bound without exceeding it.
	depth := policy.MaxJSONNestingDepth - 1
	body := `{"model":"a","x":` + strings.Repeat(`[`, depth) + `1` + strings.Repeat(`]`, depth) + `}`
	if _, err := Scan([]byte(body)); err != nil {
		t.Fatalf("expected the maximum accepted depth to be accepted, got %v", err)
	}
}

func TestScan_DepthOverBoundRejected(t *testing.T) {
	depth := policy.MaxJSONNestingDepth
	body := `{"model":"a","x":` + strings.Repeat(`[`, depth) + `1` + strings.Repeat(`]`, depth) + `}`
	_, err := Scan([]byte(body))
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("expected ErrDepthExceeded past the bound, got %v", err)
	}
}

func TestScan_DepthDoesNotRecurseNatively(t *testing.T) {
	// A nesting depth far beyond the bound must fail cleanly rather than
	// panic or exhaust the stack, proving skipValue does not recurse per
	// nesting level.
	depth := 1_000_000
	body := "{\"model\":\"a\",\"x\":" + strings.Repeat("[", depth) + strings.Repeat("]", depth) + "}"
	_, err := Scan([]byte(body))
	if !errors.Is(err, ErrDepthExceeded) {
		t.Fatalf("expected ErrDepthExceeded for extreme nesting, got %v", err)
	}
}

func TestScan_NoUnmarshalOfOpaqueValues(t *testing.T) {
	// A "messages" value that is not valid as a Go map (arbitrary nested
	// nulls, mixed types) must still scan successfully, because the
	// scanner never decodes it.
	body := []byte(`{"model":"a","messages":[null,1,"two",[3,false],{"k":null}]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_ModelSpanCoversRawValue(t *testing.T) {
	body := []byte(`{"model":"kimi-k2.7","messages":[]}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := string(meta.ModelSpan.Bytes(body)); got != `"kimi-k2.7"` {
		t.Errorf("expected model span to cover %q, got %q", `"kimi-k2.7"`, got)
	}
}

func TestScan_StreamSpanCoversRawValue(t *testing.T) {
	body := []byte(`{"model":"a","stream":true}`)
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !meta.HasStream {
		t.Fatal("expected HasStream to be true")
	}
	if got := string(meta.StreamSpan.Bytes(body)); got != "true" {
		t.Errorf("expected stream span to cover %q, got %q", "true", got)
	}
}

func TestScan_WhitespaceVariants(t *testing.T) {
	body := []byte("  \n\t{ \n \"model\" \t : \"a\" , \n \"messages\" : [ ] } \n ")
	meta, err := Scan(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta.Model != "a" {
		t.Errorf("expected model=a, got %q", meta.Model)
	}
}

func TestScan_EmptyObject(t *testing.T) {
	_, err := Scan([]byte(`{}`))
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for a missing model in an empty object, got %v", err)
	}
}

// FuzzScan proves Scan never panics regardless of input. The full fuzz
// corpus and CI wiring belong to a dedicated bead; this seed corpus still
// runs under plain go test.
func FuzzScan(f *testing.F) {
	seeds := []string{
		`{"model":"a","messages":[]}`,
		`{}`,
		`[]`,
		`null`,
		`{"model":"a","x":[[[[[1]]]]]}`,
		`{"model":"m","x":"😀"}`,
		`{"model":"a","x":"\`,
		`{"model":"a"` + strings.Repeat(",", 1000),
		strings.Repeat("[", 10000),
		`{"model":"a","x":1e999999999}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		defer func() {
			if r := recover(); r != nil {
				t.Fatalf("Scan panicked on input %q: %v", body, r)
			}
		}()
		_, _ = Scan([]byte(body))
	})
}
