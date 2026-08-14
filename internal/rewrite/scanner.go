package rewrite

import (
	"encoding/json"
	"errors"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// ErrMalformedJSON covers syntactically invalid JSON, a non-object top
// level, a missing or non-string model, and a duplicate model or stream
// member.
var ErrMalformedJSON = errors.New("rewrite: malformed request body")

// ErrDepthExceeded is returned when a value nests more than
// policy.MaxJSONNestingDepth deep, counting the top-level object itself as
// depth one.
var ErrDepthExceeded = errors.New("rewrite: nesting depth exceeded")

// Span is a byte range into the original request body.
type Span struct {
	Start, End int
}

// Bytes returns the raw bytes this span covers.
func (s Span) Bytes(body []byte) []byte { return body[s.Start:s.End] }

// Metadata is the routing information the top-level scanner extracts.
// Spans for members the proxy does not read or replace are never retained,
// so Metadata's size does not grow with the number of top-level members.
type Metadata struct {
	// Model is the decoded top-level "model" string value.
	Model string
	// ModelSpan is the raw span of the model member's value, including its
	// surrounding quotes.
	ModelSpan Span
	// HasStream reports whether a top-level "stream" member exists.
	HasStream bool
	// StreamSpan is the raw span of the stream member's value, valid only
	// when HasStream is true.
	StreamSpan Span
}

// Scan walks the top-level members of the JSON object in body without
// unmarshalling it into a general structure, and validates the fixed
// routing envelope: the body must be syntactically valid JSON, the
// top-level value must be an object, exactly one top-level "model" member
// must exist and be a string, and at most one top-level "stream" member may
// exist. Every other member is opaque: its value is skipped, never decoded,
// and no span for it is retained. Top-level member names are compared after
// decoding JSON string escapes, so an escaped spelling of a routed name is
// recognized as the same member.
func Scan(body []byte) (Metadata, error) {
	i := skipWS(body, 0)
	if i >= len(body) || body[i] != '{' {
		return Metadata{}, ErrMalformedJSON
	}
	i++
	i = skipWS(body, i)

	var meta Metadata
	haveModel := false
	afterOpen := true

	for {
		if i >= len(body) {
			return Metadata{}, ErrMalformedJSON
		}
		if body[i] == '}' {
			i++
			break
		}

		if !afterOpen {
			if body[i] != ',' {
				return Metadata{}, ErrMalformedJSON
			}
			i++
			i = skipWS(body, i)
			if i >= len(body) {
				return Metadata{}, ErrMalformedJSON
			}
		}
		afterOpen = false

		if body[i] != '"' {
			return Metadata{}, ErrMalformedJSON
		}
		keyStart := i
		var err error
		i, err = skipString(body, keyStart)
		if err != nil {
			return Metadata{}, err
		}
		isModel, err := matchesRoutingName(body[keyStart:i], "model")
		if err != nil {
			return Metadata{}, ErrMalformedJSON
		}
		isStream, err := matchesRoutingName(body[keyStart:i], "stream")
		if err != nil {
			return Metadata{}, ErrMalformedJSON
		}

		i = skipWS(body, i)
		if i >= len(body) || body[i] != ':' {
			return Metadata{}, ErrMalformedJSON
		}
		i++
		i = skipWS(body, i)
		if i >= len(body) {
			return Metadata{}, ErrMalformedJSON
		}

		valueStart := i
		i, err = skipValue(body, i, 1)
		if err != nil {
			return Metadata{}, err
		}

		switch {
		case isModel:
			if haveModel {
				return Metadata{}, ErrMalformedJSON
			}
			// encoding/json treats null as a no-op for any destination
			// type rather than an error, so a non-string span must be
			// rejected by its leading byte, not by decodeString's result.
			if body[valueStart] != '"' {
				return Metadata{}, ErrMalformedJSON
			}
			value, decErr := decodeString(body[valueStart:i])
			if decErr != nil {
				return Metadata{}, ErrMalformedJSON
			}
			haveModel = true
			meta.Model = value
			meta.ModelSpan = Span{Start: valueStart, End: i}
		case isStream:
			if meta.HasStream {
				return Metadata{}, ErrMalformedJSON
			}
			meta.HasStream = true
			meta.StreamSpan = Span{Start: valueStart, End: i}
		}

		i = skipWS(body, i)
	}

	i = skipWS(body, i)
	if i != len(body) {
		return Metadata{}, ErrMalformedJSON
	}
	if !haveModel {
		return Metadata{}, ErrMalformedJSON
	}
	return meta, nil
}

// matchesRoutingName compares a JSON member name to an ASCII routing name
// while decoding escapes into the comparison itself. It allocates no decoded
// string for opaque member names.
func matchesRoutingName(raw []byte, want string) (bool, error) {
	if len(raw) < 2 || raw[0] != '"' || raw[len(raw)-1] != '"' {
		return false, ErrMalformedJSON
	}
	matched := 0
	for i := 1; i < len(raw)-1; {
		var value byte
		if raw[i] != '\\' {
			value = raw[i]
			i++
		} else {
			i++
			if i >= len(raw)-1 {
				return false, ErrMalformedJSON
			}
			switch raw[i] {
			case '"', '\\', '/':
				value = raw[i]
				i++
			case 'b', 'f', 'n', 'r', 't':
				return false, nil
			case 'u':
				if i+4 >= len(raw)-1 {
					return false, ErrMalformedJSON
				}
				hex, ok := asciiEscape(raw[i+1 : i+5])
				if !ok {
					return false, ErrMalformedJSON
				}
				if hex > 0x7f {
					return false, nil
				}
				value = byte(hex)
				i += 5
			default:
				return false, ErrMalformedJSON
			}
		}
		if matched >= len(want) || value != want[matched] {
			return false, nil
		}
		matched++
	}
	return matched == len(want), nil
}

func asciiEscape(raw []byte) (uint16, bool) {
	var value uint16
	for _, digit := range raw {
		value <<= 4
		switch {
		case digit >= '0' && digit <= '9':
			value |= uint16(digit - '0')
		case digit >= 'a' && digit <= 'f':
			value |= uint16(digit-'a') + 10
		case digit >= 'A' && digit <= 'F':
			value |= uint16(digit-'A') + 10
		default:
			return 0, false
		}
	}
	return value, true
}

// decodeString decodes a single, already-bounded JSON string span into a Go
// string. It is used only for the short routing names and the model value,
// never for an opaque member's value.
func decodeString(raw []byte) (string, error) {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", err
	}
	return s, nil
}

// skipWS advances i past JSON whitespace.
func skipWS(body []byte, i int) int {
	for i < len(body) {
		switch body[i] {
		case ' ', '\t', '\n', '\r':
			i++
		default:
			return i
		}
	}
	return i
}

func isDigit(c byte) bool { return c >= '0' && c <= '9' }

func isHexDigit(c byte) bool {
	return (c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')
}

func hasPrefixAt(body []byte, i int, lit string) bool {
	if i+len(lit) > len(body) {
		return false
	}
	for k := 0; k < len(lit); k++ {
		if body[i+k] != lit[k] {
			return false
		}
	}
	return true
}

// skipString advances past exactly one JSON string starting at body[i],
// which must be the opening quote, and returns the index just past the
// closing quote. It validates escape syntax without decoding it.
func skipString(body []byte, i int) (int, error) {
	if i >= len(body) || body[i] != '"' {
		return 0, ErrMalformedJSON
	}
	i++
	for i < len(body) {
		c := body[i]
		switch {
		case c == '"':
			return i + 1, nil
		case c == '\\':
			i++
			if i >= len(body) {
				return 0, ErrMalformedJSON
			}
			switch body[i] {
			case '"', '\\', '/', 'b', 'f', 'n', 'r', 't':
				i++
			case 'u':
				if i+4 >= len(body) {
					return 0, ErrMalformedJSON
				}
				for k := 1; k <= 4; k++ {
					if !isHexDigit(body[i+k]) {
						return 0, ErrMalformedJSON
					}
				}
				i += 5
			default:
				return 0, ErrMalformedJSON
			}
		case c < 0x20:
			return 0, ErrMalformedJSON
		default:
			i++
		}
	}
	return 0, ErrMalformedJSON
}

// skipNumber advances past exactly one JSON number starting at body[i].
func skipNumber(body []byte, i int) (int, error) {
	start := i
	if i < len(body) && body[i] == '-' {
		i++
	}
	if i >= len(body) || !isDigit(body[i]) {
		return 0, ErrMalformedJSON
	}
	if body[i] == '0' {
		i++
	} else {
		for i < len(body) && isDigit(body[i]) {
			i++
		}
	}
	if i < len(body) && body[i] == '.' {
		i++
		if i >= len(body) || !isDigit(body[i]) {
			return 0, ErrMalformedJSON
		}
		for i < len(body) && isDigit(body[i]) {
			i++
		}
	}
	if i < len(body) && (body[i] == 'e' || body[i] == 'E') {
		i++
		if i < len(body) && (body[i] == '+' || body[i] == '-') {
			i++
		}
		if i >= len(body) || !isDigit(body[i]) {
			return 0, ErrMalformedJSON
		}
		for i < len(body) && isDigit(body[i]) {
			i++
		}
	}
	if i == start {
		return 0, ErrMalformedJSON
	}
	return i, nil
}

// skipScalar advances past exactly one JSON string, number, true, false or
// null starting at body[i]. It never handles objects or arrays.
func skipScalar(body []byte, i int) (int, error) {
	if i >= len(body) {
		return 0, ErrMalformedJSON
	}
	switch {
	case body[i] == '"':
		return skipString(body, i)
	case body[i] == '-' || isDigit(body[i]):
		return skipNumber(body, i)
	case hasPrefixAt(body, i, "true"):
		return i + 4, nil
	case hasPrefixAt(body, i, "false"):
		return i + 5, nil
	case hasPrefixAt(body, i, "null"):
		return i + 4, nil
	default:
		return 0, ErrMalformedJSON
	}
}

// skipValue advances i past one complete JSON value starting at body[i],
// which may be an object or array containing further nested values.
// baseDepth is the nesting depth already accumulated by the caller (the
// top-level object itself counts as depth one). Depth is tracked with an
// explicit stack rather than native Go recursion, so arbitrarily deep input
// cannot consume the Go call stack; nesting past
// policy.MaxJSONNestingDepth is rejected before it happens.
func skipValue(body []byte, i int, baseDepth int) (int, error) {
	if i >= len(body) {
		return 0, ErrMalformedJSON
	}
	if body[i] != '{' && body[i] != '[' {
		return skipScalar(body, i)
	}

	var stack []bool // true = object, false = array
	depth := baseDepth

outer:
	for {
		isObject := body[i] == '{'
		stack = append(stack, isObject)
		depth++
		if depth > policy.MaxJSONNestingDepth {
			return 0, ErrDepthExceeded
		}
		i++
		i = skipWS(body, i)
		afterOpen := true

		for {
			if i >= len(body) {
				return 0, ErrMalformedJSON
			}
			top := stack[len(stack)-1]
			closeCh := byte(']')
			if top {
				closeCh = '}'
			}

			if body[i] == closeCh {
				i++
				stack = stack[:len(stack)-1]
				depth--
				if len(stack) == 0 {
					return i, nil
				}
				i = skipWS(body, i)
				afterOpen = false
				continue
			}

			if !afterOpen {
				if body[i] != ',' {
					return 0, ErrMalformedJSON
				}
				i++
				i = skipWS(body, i)
				if i >= len(body) {
					return 0, ErrMalformedJSON
				}
			}
			afterOpen = false

			if top {
				if body[i] != '"' {
					return 0, ErrMalformedJSON
				}
				var err error
				i, err = skipString(body, i)
				if err != nil {
					return 0, err
				}
				i = skipWS(body, i)
				if i >= len(body) || body[i] != ':' {
					return 0, ErrMalformedJSON
				}
				i++
				i = skipWS(body, i)
				if i >= len(body) {
					return 0, ErrMalformedJSON
				}
			}

			if body[i] == '{' || body[i] == '[' {
				continue outer
			}
			var err error
			i, err = skipScalar(body, i)
			if err != nil {
				return 0, err
			}
			i = skipWS(body, i)
		}
	}
}
