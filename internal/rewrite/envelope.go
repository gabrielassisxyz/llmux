package rewrite

import (
	"context"
	"errors"
	"net/http"

	"github.com/gabrielassisxyz/llmux/internal/catalog"
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
		code, rejected := envelopeRejection(body, ok)
		if !rejected {
			next(w, r)
			return
		}

		reqID, _ := proxy.RequestID(r.Context())
		proxy.WriteError(w, reqID, code)
		if writer != nil {
			_ = writer.RecordUnroutedRequest(r.Context(), code)
		}
	}
}

func envelopeRejection(body []byte, hasBody bool) (proxy.ErrorCode, bool) {
	if !hasBody {
		return proxy.ErrInvalidRequest, true
	}

	metadata, err := Scan(body)
	if err != nil {
		if errors.Is(err, ErrDepthExceeded) {
			return proxy.ErrJSONDepthExceeded, true
		}
		return proxy.ErrInvalidRequest, true
	}
	if _, found := catalog.Resolve(metadata.Model); !found {
		return proxy.ErrModelNotFound, true
	}
	return "", false
}
