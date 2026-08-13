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
			writeAuthError(w)
			return
		}

		authHeader := headers[0]
		parts := strings.Split(authHeader, " ")
		if len(parts) != 2 {
			writeAuthError(w)
			return
		}

		scheme := parts[0]
		token := parts[1]

		if !strings.EqualFold(scheme, "Bearer") || token == "" {
			writeAuthError(w)
			return
		}

		presentedDigest := sha256.Sum256([]byte(token))

		if subtle.ConstantTimeCompare(expectedDigest[:], presentedDigest[:]) != 1 {
			writeAuthError(w)
			return
		}

		next(w, r)
	}
}

func writeAuthError(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	WriteError(w, "", ErrInvalidAPIKey)
}
