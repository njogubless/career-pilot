// Package crypto provides token generation/hashing and password hashing.
package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
)

// NewToken returns a URL-safe random token with 256 bits of entropy.
func NewToken() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

// HashToken returns the SHA-256 of a token. Tokens are high-entropy, so a
// fast hash is appropriate (unlike passwords).
func HashToken(token string) []byte {
	h := sha256.Sum256([]byte(token))
	return h[:]
}

// EqualHash compares two hashes in constant time.
func EqualHash(a, b []byte) bool { return subtle.ConstantTimeCompare(a, b) == 1 }
