package storage

import (
	"crypto/rand"
	"encoding/hex"
)

// NewImageID returns a random 8-char lowercase-hex ID in "aaaa_bbbb" form.
// Collision probability for 1M IDs is roughly 1 in 36 billion.
func NewImageID() string {
	var buf [4]byte
	_, _ = rand.Read(buf[:])
	hexStr := hex.EncodeToString(buf[:])
	return hexStr[:4] + "_" + hexStr[4:]
}
