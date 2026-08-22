package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearerAuth returns a middleware that validates the Authorization
// header using a constant-time digest comparison against the expected
// SHA-256 digest. On rejection it answers with the request identifier and
// records one unrouted_request row, so a rejected credential leaves the same
// durable evidence every other local rejection does.
func RequireBearerAuth(writer UnroutedRequestWriter, expectedDigest [32]byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			writeAuthError(w, r, writer)
			return
		}

		authHeader := headers[0]
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 {
			writeAuthError(w, r, writer)
			return
		}

		scheme := parts[0]
		token := parts[1]

		if !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeAuthError(w, r, writer)
			return
		}

		presentedDigest := sha256.Sum256([]byte(token))

		if subtle.ConstantTimeCompare(expectedDigest[:], presentedDigest[:]) != 1 {
			writeAuthError(w, r, writer)
			return
		}

		next(w, r)
	}
}

// writeAuthError answers a rejected credential. It reads the request ID from
// the context so that, when authentication runs after identifier assignment,
// the rejection carries the identifier the client was already given, and it
// records the rejection when a writer is supplied.
func writeAuthError(w http.ResponseWriter, r *http.Request, writer UnroutedRequestWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	reqID, _ := RequestID(r.Context())
	WriteError(w, reqID, ErrInvalidAPIKey)
	if writer != nil {
		_ = writer.RecordUnroutedRequest(r.Context(), ErrInvalidAPIKey)
	}
}
