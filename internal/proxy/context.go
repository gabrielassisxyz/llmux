package proxy

import (
	"context"
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/idgen"
	"github.com/gabrielassisxyz/llmux/internal/policy"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// RequestID returns the logical request ID associated with the context.
func RequestID(ctx context.Context) (string, bool) {
	id, ok := ctx.Value(requestIDKey).(string)
	return id, ok
}

// AssignRequestID generates a logical request ID and attaches it to the
// request context. It answers local 500 internal_error if the generator
// fails. Every request past the route guard carries an identity, so this
// runs before any rejection on the identity side of the boundary.
func AssignRequestID(generator idgen.Generator, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		reqID, err := generator.LogicalRequestID()
		if err != nil {
			WriteError(w, "", ErrInternalError)
			return
		}

		ctx := context.WithValue(r.Context(), requestIDKey, reqID)
		next(w, r.WithContext(ctx))
	}
}

// RequireLogicalDeadline bounds the request lifetime to
// policy.LogicalRequestDeadline and answers local 504 deadline_exceeded if
// the context expires before the downstream response is committed. It reads
// the request ID from the context, so AssignRequestID must run first.
func RequireLogicalDeadline(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), policy.LogicalRequestDeadline)
		defer cancel()

		r = r.WithContext(ctx)

		// We run the next handler in the same goroutine.
		// The handlers downstream must be context-aware and return promptly when ctx.Done() is closed.
		// To write the 504 if the deadline is exceeded and the response is not yet committed,
		// we use a response writer wrapper.
		cw := &commitWriter{ResponseWriter: w}
		next(cw, r)

		if !cw.committed && ctx.Err() == context.DeadlineExceeded {
			reqID, _ := RequestID(ctx)
			WriteError(w, reqID, ErrDeadlineExceeded)
		}
	}
}

type commitWriter struct {
	http.ResponseWriter
	committed bool
}

func (cw *commitWriter) WriteHeader(statusCode int) {
	if !cw.committed {
		cw.committed = true
		cw.ResponseWriter.WriteHeader(statusCode)
	}
}

func (cw *commitWriter) Write(b []byte) (int, error) {
	if !cw.committed {
		cw.committed = true
	}
	return cw.ResponseWriter.Write(b)
}

func (cw *commitWriter) Flush() {
	if f, ok := cw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
