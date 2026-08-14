package rewrite

import (
	"context"
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/proxy"
)

// UnroutedRequestWriter records a routing-envelope rejection without retaining request content.
type UnroutedRequestWriter interface {
	RecordUnroutedRequest(context.Context, proxy.ErrorCode) error
}

// RequireScannedEnvelope validates the body attached by RequireBoundedBody before passing a request downstream.
func RequireScannedEnvelope(writer UnroutedRequestWriter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, ok := RequestBody(r.Context())
		if ok {
			_, err := Scan(body)
			if err == nil {
				next(w, r)
				return
			}
		}

		reqID, _ := proxy.RequestID(r.Context())
		proxy.WriteError(w, reqID, proxy.ErrInvalidRequest)
		if writer != nil {
			_ = writer.RecordUnroutedRequest(r.Context(), proxy.ErrInvalidRequest)
		}
	}
}
