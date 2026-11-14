// Package ids generates time-ordered UUIDv7 identifiers.
package ids

import "github.com/google/uuid"

// New returns a new UUIDv7. It panics only if the system RNG fails, which is
// unrecoverable for a security-sensitive service anyway.
func New() uuid.UUID { return uuid.Must(uuid.NewV7()) }

// Parse parses a UUID string; ok is false for malformed input.
func Parse(s string) (uuid.UUID, bool) {
	id, err := uuid.Parse(s)
	return id, err == nil
}
