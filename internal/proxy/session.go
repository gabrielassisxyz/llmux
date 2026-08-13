package proxy

import (
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"io"
	"net/http"
)

const sessionKeyVersion byte = 1

const sessionHeaderMaxBytes = 256

// SessionKey is the versioned, fixed-size affinity key retained after header validation.
type SessionKey [sha256.Size + 1]byte

// SessionKeyFromRequest validates X-Session-ID and derives its versioned HMAC digest.
// A nil key means the request supplied no affinity header.
func SessionKeyFromRequest(request *http.Request, affinityHMACKey []byte) (*SessionKey, error) {
	values := request.Header.Values("X-Session-ID")
	if len(values) == 0 || values[0] == "" {
		return nil, nil
	}
	if len(values) != 1 || len(values[0]) > sessionHeaderMaxBytes {
		return nil, errors.New("invalid session header")
	}

	mac := hmac.New(sha256.New, affinityHMACKey)
	if _, err := io.WriteString(mac, values[0]); err != nil {
		return nil, err
	}

	var key SessionKey
	key[0] = sessionKeyVersion
	copy(key[1:], mac.Sum(nil))
	return &key, nil
}
