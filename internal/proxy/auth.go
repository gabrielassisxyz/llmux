package proxy

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"
)

// RequireBearerAuth returns a middleware that validates the Authorization header
// using a constant-time digest comparison against the expected SHA-256 digest.
func RequireBearerAuth(expectedDigest [32]byte, next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		headers := r.Header.Values("Authorization")
		if len(headers) != 1 {
			writeAuthError(w, r)
			return
		}

		authHeader := headers[0]
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 {
			writeAuthError(w, r)
			return
		}

		scheme := parts[0]
		token := parts[1]

		if !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeAuthError(w, r)
			return
		}

		presentedDigest := sha256.Sum256([]byte(token))

		if subtle.ConstantTimeCompare(expectedDigest[:], presentedDigest[:]) != 1 {
			writeAuthError(w, r)
			return
		}

		next(w, r)
	}
}

// writeAuthError answers a rejected credential. It reads the request ID from
// the context so that, when authentication runs after identifier assignment,
// the rejection carries the identifier the client was already given.
func writeAuthError(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	reqID, _ := RequestID(r.Context())
	WriteError(w, reqID, ErrInvalidAPIKey)
}
