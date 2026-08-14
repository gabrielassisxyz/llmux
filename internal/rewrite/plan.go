package rewrite

import (
	"bytes"
	"fmt"
	"io"
)

// Replacement substitutes one raw value span, or inserts Value at a zero-length span.
type Replacement struct {
	Span  Span
	Value []byte
}

// Plan replays immutable original-body spans and small replacement values.
type Plan struct {
	segments [][]byte
	length   int64
}

// NewPlan builds a replayable segmented request body without constructing a second body buffer.
func NewPlan(body []byte, replacements []Replacement) (Plan, error) {
	segments := make([][]byte, 0, len(replacements)*2+1)
	position := 0
	var length int64
	appendSegment := func(segment []byte) error {
		if int64(len(segment)) > (1<<63-1)-length {
			return fmt.Errorf("rewrite plan content length overflows")
		}
		length += int64(len(segment))
		segments = append(segments, segment)
		return nil
	}
	for _, replacement := range replacements {
		if replacement.Span.Start < position || replacement.Span.End < replacement.Span.Start || replacement.Span.End > len(body) {
			return Plan{}, fmt.Errorf("rewrite plan has invalid replacement span")
		}
		if err := appendSegment(body[position:replacement.Span.Start]); err != nil {
			return Plan{}, err
		}
		if err := appendSegment(replacement.Value); err != nil {
			return Plan{}, err
		}
		position = replacement.Span.End
	}
	if err := appendSegment(body[position:]); err != nil {
		return Plan{}, err
	}
	return Plan{segments: segments, length: length}, nil
}

// ContentLength returns the checked sum of the immutable segments.
func (plan Plan) ContentLength() int64 { return plan.length }

// Reader returns a fresh replay reader over the same immutable segments.
func (plan Plan) Reader() io.Reader {
	readers := make([]io.Reader, len(plan.segments))
	for index, segment := range plan.segments {
		readers[index] = bytes.NewReader(segment)
	}
	return io.MultiReader(readers...)
}
