package rewrite

import (
	"context"
	"errors"
	"io"
	"net/http"
	"time"

	"github.com/gabrielassisxyz/llmux/internal/policy"
	"github.com/gabrielassisxyz/llmux/internal/proxy"
	"github.com/gabrielassisxyz/llmux/internal/resource"
)

type contextKey string

const requestBodyKey contextKey = "request_body"

// errBodyTooLarge is returned when the request body exceeds
// policy.MaxRequestBodyBytes.
var errBodyTooLarge = errors.New("rewrite: request body exceeds maximum size")

// RequestBody returns the raw request body bytes read by RequireBoundedBody.
func RequestBody(ctx context.Context) ([]byte, bool) {
	body, ok := ctx.Value(requestBodyKey).([]byte)
	return body, ok
}

// RequireBoundedBody reads the request body into one request-lifetime
// buffer, charged against the process-wide memory gate in allocated
// capacity before that capacity is allocated, and bounded by
// policy.MaxRequestBodyBytes. It reads the RequestResources attached to the
// context by resource.RequireResources, which must run first.
func RequireBoundedBody(writer UnroutedRequestWriter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		res := resource.ContextResources(r.Context())
		body, err := readBoundedBody(r, res)
		if err != nil {
			if r.Context().Err() != nil {
				// The client vanished or the logical deadline already
				// expired; there is nothing to write to.
				return
			}

			reqID, _ := proxy.RequestID(r.Context())
			var code proxy.ErrorCode
			switch {
			case errors.Is(err, errBodyTooLarge):
				code = proxy.ErrRequestTooLarge
				proxy.WriteError(w, reqID, code)
			case errors.Is(err, resource.ErrOverloaded):
				code = proxy.ErrProxyOverloaded
				// A global memory failure consulted no account, so there is
				// no reopening time to derive; the zero value falls back to
				// the fixed one-second Retry-After.
				proxy.WriteRateLimitError(w, reqID, code, time.Time{}, time.Time{})
			default:
				code = proxy.ErrInvalidRequest
				proxy.WriteError(w, reqID, code)
			}

			if writer != nil {
				_ = writer.RecordUnroutedRequest(r.Context(), code)
			}
			return
		}

		ctx := context.WithValue(r.Context(), requestBodyKey, body)
		next(w, r.WithContext(ctx))
	}
}

func readBoundedBody(r *http.Request, res *resource.RequestResources) ([]byte, error) {
	if r.ContentLength >= 0 {
		return readKnownLengthBody(r, res)
	}
	return readUnsizedBody(r, res)
}

// readKnownLengthBody charges the declared length once and reads it into
// one exact allocation, which is what a declared length buys.
func readKnownLengthBody(r *http.Request, res *resource.RequestResources) ([]byte, error) {
	if r.ContentLength > policy.MaxRequestBodyBytes {
		return nil, errBodyTooLarge
	}

	size := int(r.ContentLength)
	if err := res.AcquireMemory(size); err != nil {
		return nil, err
	}

	buf := make([]byte, size)
	if _, err := io.ReadFull(r.Body, buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// readUnsizedBody grows a buffer geometrically, capped at
// policy.MaxRequestBodyBytes, charging each doubling before it happens so
// the charge never falls behind the capacity the buffer is about to
// occupy. Once the buffer is full at the maximum size, a single probe byte
// read outside the charged buffer distinguishes a body that ends exactly at
// the boundary from one that exceeds it, without ever allocating past the
// boundary to find out.
func readUnsizedBody(r *http.Request, res *resource.RequestResources) ([]byte, error) {
	charge, err := res.AcquireUnsizedBodyCharge()
	if err != nil {
		return nil, err
	}

	buf := make([]byte, 0, policy.UnknownLengthBodyChargeStepBytes)

	for len(buf) < policy.MaxRequestBodyBytes {
		if len(buf) == cap(buf) {
			nextCap := min(cap(buf)*2, policy.MaxRequestBodyBytes)
			if err := charge.GrowTo(r.Context(), nextCap); err != nil {
				return nil, err
			}
			grown := make([]byte, len(buf), nextCap)
			copy(grown, buf)
			buf = grown
		}

		n, readErr := r.Body.Read(buf[len(buf):cap(buf)])
		buf = buf[:len(buf)+n]
		if readErr != nil {
			if readErr != io.EOF {
				charge.Release()
				return nil, readErr
			}
			if err := charge.Settle(cap(buf)); err != nil {
				return nil, err
			}
			return buf, nil
		}
	}

	var probe [1]byte
	n, readErr := r.Body.Read(probe[:])
	if readErr != nil && readErr != io.EOF {
		charge.Release()
		return nil, readErr
	}
	if n > 0 {
		charge.Release()
		return nil, errBodyTooLarge
	}

	if err := charge.Settle(cap(buf)); err != nil {
		return nil, err
	}
	return buf, nil
}
