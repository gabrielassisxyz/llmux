package rewrite

import (
	"net/http"
	"strings"

	"github.com/gabrielassisxyz/llmux/internal/proxy"
)

// RequireIdentityEncoding rejects a request whose Content-Encoding is not
// absent or exactly "identity". A compressed body cannot be patched and
// retried while preserving the raw body contract, so this check runs on the
// header alone, before the body reader and before the scanner ever see the
// request. It sits on the identity side of the boundary: the request ID is
// already assigned, so the rejection carries it and appends one
// unrouted_request row.
func RequireIdentityEncoding(writer UnroutedRequestWriter, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !identityOrAbsentEncoding(r) {
			reqID, _ := proxy.RequestID(r.Context())
			proxy.WriteError(w, reqID, proxy.ErrUnsupportedContentEncoding)
			if writer != nil {
				_ = writer.RecordUnroutedRequest(r.Context(), proxy.ErrUnsupportedContentEncoding)
			}
			return
		}
		next(w, r)
	}
}

// identityOrAbsentEncoding reports whether the request's Content-Encoding
// is either absent or exactly "identity".
func identityOrAbsentEncoding(r *http.Request) bool {
	values := r.Header.Values("Content-Encoding")
	if len(values) == 0 {
		return true
	}
	return len(values) == 1 && strings.EqualFold(strings.TrimSpace(values[0]), "identity")
}
