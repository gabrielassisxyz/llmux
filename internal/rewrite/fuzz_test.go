package rewrite

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"testing"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

func TestApplyInjectionRejectsTrailingNonWhitespace(t *testing.T) {
	_, err := ApplyInjection([]byte(`{"model":""}0`), "route_owned", "new")
	if !errors.Is(err, ErrMalformedJSON) {
		t.Fatalf("expected ErrMalformedJSON for trailing non-whitespace, got %v", err)
	}
}

func TestApplyInjectionAppendsBeforeCloseBraceWithTrailingWhitespace(t *testing.T) {
	body := []byte(`{"model":""} `)
	plan, err := ApplyInjection(body, "route_owned", "new")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	got, err := io.ReadAll(plan.Reader())
	if err != nil {
		t.Fatalf("reading plan: %v", err)
	}
	want := []byte(`{"model":"","route_owned":"new"} `)
	if !bytes.Equal(got, want) {
		t.Fatalf("output mismatch:\n got: %q\nwant: %q", got, want)
	}
}

// FuzzScan proves Scan never panics on arbitrary input and returns either a
// scanner error or a metadata envelope whose model span decodes to Model.
func FuzzScan(f *testing.F) {
	seeds := []string{
		`{"model":"a","messages":[]}`,
		`{"model":""}`,
		`{}`,
		`[]`,
		`null`,
		`{"model":"a","x":[[[[[1]]]]]}`,
		`{"\u006dodel":"a","messages":[]}`,
		`{"model":"a","\u0073tream":true}`,
		`{"model":"a","stream":true,"\u0073tream":false}`,
		`{"model":"m","x":"😀"}`,
		`{"model":"a","x":"\`,
		`{"model":"a"` + repeat(",", 1000),
		repeat("[", 10000),
		`{"model":"a","x":1e999999999}`,
		// Nesting exactly below, at, and above the accepted depth boundary.
		`{"model":"a","x":` + repeat("[", policy.MaxJSONNestingDepth-2) + `1` + repeat("]", policy.MaxJSONNestingDepth-2) + `}`,
		`{"model":"a","x":` + repeat("[", policy.MaxJSONNestingDepth-1) + `1` + repeat("]", policy.MaxJSONNestingDepth-1) + `}`,
		`{"model":"a","x":` + repeat("[", policy.MaxJSONNestingDepth) + `1` + repeat("]", policy.MaxJSONNestingDepth) + `}`,
		// Real multi-turn tool-loop shape.
		`{"model":"a","messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]},{"role":"tool","tool_call_id":"call_1","content":"result"},{"role":"assistant","content":"done"}]}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		meta, err := Scan([]byte(body))
		if err != nil {
			if meta != (Metadata{}) {
				t.Fatalf("Scan returned non-zero metadata with error: %+v, err=%v", meta, err)
			}
			return
		}

		modelRaw := meta.ModelSpan.Bytes([]byte(body))
		decoded, decErr := decodeString(modelRaw)
		if decErr != nil {
			t.Fatalf("Scan reported model span %q that does not decode: %v", modelRaw, decErr)
		}
		if decoded != meta.Model {
			t.Fatalf("Scan model span decodes to %q but Metadata.Model is %q", decoded, meta.Model)
		}

		if meta.HasStream {
			streamRaw := meta.StreamSpan.Bytes([]byte(body))
			if len(streamRaw) == 0 {
				t.Fatalf("Scan reported HasStream with an empty stream span")
			}
			if string(streamRaw) != "true" && string(streamRaw) != "false" {
				t.Fatalf("Scan stream span is not a boolean: %q", streamRaw)
			}
		}
	})
}

// FuzzApplyInjection proves ApplyInjection never panics on valid JSON input,
// produces valid JSON output, injects the expected value, and leaves every
// other top-level member byte-for-byte identical.
func FuzzApplyInjection(f *testing.F) {
	seeds := []string{
		`{"route_owned":"old","model":"m","messages":[]}`,
		`{"model":"m","messages":[]}`,
		`{"model":"m","messages":[],"route_owned":"old"}`,
		`{"route_\u006fwned":"old","model":"m","messages":[]}`,
		`{"model":"m","messages":[{"content":"{[,:]\"\\"}],"unknown":1e+09}`,
		`{"model":"m","messages":[{"content":"{[,:]\"\\"}],"unknown":1e+09,"unknown":-0}`,
		// Multi-turn tool-loop shape with an owned field to replace.
		`{"model":"m","route_owned":"old","messages":[{"role":"user","content":"hi"},{"role":"assistant","tool_calls":[{"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}]}`,
	}
	for _, s := range seeds {
		f.Add(s, "new")
	}
	f.Fuzz(func(t *testing.T, body string, value string) {
		const field = "route_owned"

		if !bytes.Contains([]byte(body), []byte(`"model"`)) {
			return
		}

		plan, err := ApplyInjection([]byte(body), field, value)
		if err != nil {
			return
		}

		got, readErr := io.ReadAll(plan.Reader())
		if readErr != nil {
			t.Fatalf("reading plan: %v", readErr)
		}

		// The output must be valid JSON.
		var result map[string]json.RawMessage
		if err := json.Unmarshal(got, &result); err != nil {
			t.Fatalf("ApplyInjection produced invalid JSON: %v\noutput: %q", err, got)
		}

		// The injected field must carry the encoded fuzz value.
		encoded, encErr := json.Marshal(value)
		if encErr != nil {
			t.Fatalf("marshal fuzz value: %v", encErr)
		}
		if gotField, ok := result[field]; !ok || string(gotField) != string(encoded) {
			t.Fatalf("injected field mismatch: got %s=%s, want %s", field, gotField, encoded)
		}

		// Untouched routed members must be preserved byte-for-byte.
		mustPreserve(t, []byte(body), got, "model")
		if _, ok := result["messages"]; ok {
			mustPreserve(t, []byte(body), got, "messages")
		}
	})
}

// mustPreserve extracts the raw top-level value of key from both input and
// output and fails the test if the byte span changed.
func mustPreserve(t *testing.T, input, output []byte, key string) {
	t.Helper()
	inSpan, inOK, inErr := topLevelRawValue(input, key)
	outSpan, outOK, outErr := topLevelRawValue(output, key)
	if inErr != nil {
		t.Fatalf("extract input %s: %v", key, inErr)
	}
	if outErr != nil {
		t.Fatalf("extract output %s: %v", key, outErr)
	}
	if inOK != outOK {
		t.Fatalf("member %s presence changed: input=%v output=%v", key, inOK, outOK)
	}
	if !inOK {
		return
	}
	if !bytes.Equal(inSpan.Bytes(input), outSpan.Bytes(output)) {
		t.Fatalf("member %s bytes changed: input=%q output=%q", key, inSpan.Bytes(input), outSpan.Bytes(output))
	}
}

// topLevelRawValue returns the raw span of the first top-level member whose
// decoded name matches key. It does not reject duplicate unknown members so it
// can validate bodies the scanner accepts.
func topLevelRawValue(body []byte, key string) (Span, bool, error) {
	i := skipWS(body, 0)
	if i >= len(body) || body[i] != '{' {
		return Span{}, false, nil
	}
	i++
	first := true
	for {
		i = skipWS(body, i)
		if i >= len(body) {
			return Span{}, false, nil
		}
		if body[i] == '}' {
			return Span{}, false, nil
		}

		if !first {
			if body[i] != ',' {
				return Span{}, false, nil
			}
			i++
			i = skipWS(body, i)
			if i >= len(body) {
				return Span{}, false, nil
			}
		}
		first = false

		if body[i] != '"' {
			return Span{}, false, nil
		}
		keyStart := i
		var err error
		i, err = skipString(body, keyStart)
		if err != nil {
			return Span{}, false, err
		}
		matches, err := matchesRoutingName(body[keyStart:i], key)
		if err != nil {
			return Span{}, false, err
		}

		i = skipWS(body, i)
		if i >= len(body) || body[i] != ':' {
			return Span{}, false, nil
		}
		i++
		i = skipWS(body, i)
		if i >= len(body) {
			return Span{}, false, nil
		}

		valueStart := i
		i, err = skipValue(body, i, 1)
		if err != nil {
			return Span{}, false, err
		}

		if matches {
			return Span{Start: valueStart, End: i}, true, nil
		}
	}
}

func repeat(s string, n int) string {
	var b bytes.Buffer
	b.Grow(len(s) * n)
	for range n {
		b.WriteString(s)
	}
	return b.String()
}
