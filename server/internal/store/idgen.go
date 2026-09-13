package store

import (
	"crypto/rand"
	"encoding/base64"
)

// NewID returns a random URL-safe string of exactly n characters.
func NewID(n int) string {
	// base64url encodes 3 bytes into 4 characters; over-allocate then trim.
	raw := make([]byte, (n*3/4)+3)
	if _, err := rand.Read(raw); err != nil {
		panic(err) // crypto/rand failing is unrecoverable
	}
	enc := base64.RawURLEncoding.EncodeToString(raw)
	return enc[:n]
}
