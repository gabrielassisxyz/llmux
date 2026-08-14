package rewrite

import (
	"encoding/json"
	"fmt"
)

// ApplyInjection returns a segmented plan that replaces or appends one route-owned top-level field.
func ApplyInjection(body []byte, field, value string) (Plan, error) {
	i := skipWS(body, 0)
	if i >= len(body) || body[i] != '{' {
		return Plan{}, ErrMalformedJSON
	}
	i++
	first := true
	var existing *Span
	for {
		i = skipWS(body, i)
		if i >= len(body) {
			return Plan{}, ErrMalformedJSON
		}
		if body[i] == '}' {
			encoded, err := json.Marshal(value)
			if err != nil {
				return Plan{}, fmt.Errorf("encode injection value: %w", err)
			}
			if existing != nil {
				return NewPlan(body, []Replacement{{Span: *existing, Value: encoded}})
			}
			prefix := []byte(`,"` + field + `":`)
			if first {
				prefix = prefix[1:]
			}
			return NewPlan(body, []Replacement{{Span: Span{Start: i, End: i}, Value: append(prefix, encoded...)}})
		}
		if !first {
			if body[i] != ',' {
				return Plan{}, ErrMalformedJSON
			}
			i++
			i = skipWS(body, i)
		}
		first = false
		keyStart := i
		var err error
		i, err = skipString(body, i)
		if err != nil {
			return Plan{}, err
		}
		matches, err := matchesRoutingName(body[keyStart:i], field)
		if err != nil {
			return Plan{}, err
		}
		i = skipWS(body, i)
		if i >= len(body) || body[i] != ':' {
			return Plan{}, ErrMalformedJSON
		}
		i++
		i = skipWS(body, i)
		start := i
		i, err = skipValue(body, i, 1)
		if err != nil {
			return Plan{}, err
		}
		if matches {
			if existing != nil {
				return Plan{}, ErrMalformedJSON
			}
			span := Span{Start: start, End: i}
			existing = &span
		}
	}
}
