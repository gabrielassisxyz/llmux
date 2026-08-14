package resource

import (
	"context"
	"errors"

	"github.com/gabrielassisxyz/llmux/internal/policy"
)

// BodyCharge tracks the memory charged while reading a request body whose
// length is not declared. The buffer backing the read grows geometrically as
// bytes arrive; the charge stays ahead of that growth by extending itself in
// fixed policy.UnknownLengthBodyChargeStepBytes steps before each backing
// array increase, and settles down to the buffer's final capacity once the
// read completes.
type BodyCharge struct {
	res     *RequestResources
	charged int
}

// AcquireUnsizedBodyCharge takes the fixed initial step for a body whose
// length the request did not declare.
func (r *RequestResources) AcquireUnsizedBodyCharge() (*BodyCharge, error) {
	step := policy.UnknownLengthBodyChargeStepBytes
	if err := r.AcquireMemory(step); err != nil {
		return nil, err
	}
	return &BodyCharge{res: r, charged: step}, nil
}

// GrowTo extends the charge, one fixed step at a time, until it is at least
// target bytes, so the charge always covers the capacity the backing array
// is about to occupy before that capacity is allocated. A cancelled or
// expired ctx, or a step the gate denies, releases the entire charge held by
// this BodyCharge and returns the error; the caller must not read further.
func (b *BodyCharge) GrowTo(ctx context.Context, target int) error {
	if target < 0 {
		return errors.New("resource: negative body charge target")
	}
	step := policy.UnknownLengthBodyChargeStepBytes
	for b.charged < target {
		select {
		case <-ctx.Done():
			b.Release()
			return ctx.Err()
		default:
		}
		if err := b.res.AcquireMemory(step); err != nil {
			b.Release()
			return err
		}
		b.charged += step
	}
	return nil
}

// Settle releases the charge down to final, the buffer's actual final
// capacity, which must not exceed the amount currently charged. Capacity is
// what the process holds for the rest of the request, not the shorter
// length actually read.
func (b *BodyCharge) Settle(final int) error {
	if final < 0 || final > b.charged {
		return errors.New("resource: body charge settle out of range")
	}
	delta := b.charged - final
	if delta > 0 {
		b.res.releaseMemory(delta)
		b.charged -= delta
	}
	return nil
}

// Release returns the entire charge held by this BodyCharge to the request.
// Safe to call more than once.
func (b *BodyCharge) Release() {
	if b.charged > 0 {
		b.res.releaseMemory(b.charged)
		b.charged = 0
	}
}
