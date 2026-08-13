package idgen

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
)

// Generator produces proxy-owned identifiers from secure entropy.
type Generator struct {
	entropy io.Reader
}

// NewGenerator constructs an identifier generator with the supplied entropy source.
func NewGenerator(entropy io.Reader) Generator {
	return Generator{entropy: entropy}
}

// SecureGenerator constructs an identifier generator backed by crypto/rand.
func SecureGenerator() Generator {
	return NewGenerator(rand.Reader)
}

func (generator Generator) LogicalRequestID() (string, error) {
	return generator.newID()
}

func (generator Generator) AttemptID() (string, error) {
	return generator.newID()
}

func (generator Generator) RecordID() (string, error) {
	return generator.newID()
}

func (generator Generator) newID() (string, error) {
	var bytes [16]byte
	if _, err := io.ReadFull(generator.entropy, bytes[:]); err != nil {
		return "", fmt.Errorf("read identifier entropy: %w", err)
	}
	return hex.EncodeToString(bytes[:]), nil
}
