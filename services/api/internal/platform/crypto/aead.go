package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"errors"
	"fmt"
)

// Sealer encrypts secrets at rest (OAuth tokens) with AES-256-GCM.
// Output layout: version byte | 12-byte nonce | ciphertext+tag. The version
// byte allows key rotation: Open accepts any configured key version.
type Sealer struct {
	current byte
	aeads   map[byte]cipher.AEAD
}

// NewSealer uses key as version 1. Additional (older) keys may be added for
// rotation with AddKey.
func NewSealer(key []byte) (*Sealer, error) {
	s := &Sealer{aeads: map[byte]cipher.AEAD{}}
	if err := s.AddKey(1, key); err != nil {
		return nil, err
	}
	s.current = 1
	return s, nil
}

// NewKeyring seals with the current key version and opens any listed version
// (retired keys stay readable until every value is re-sealed; see
// `careerpilot keys rotate`).
func NewKeyring(current byte, keys map[byte][]byte) (*Sealer, error) {
	if current == 0 {
		return nil, errors.New("key version must be 1-255")
	}
	if _, ok := keys[current]; !ok {
		return nil, fmt.Errorf("current key version %d is not in the keyring", current)
	}
	s := &Sealer{aeads: map[byte]cipher.AEAD{}, current: current}
	for v, k := range keys {
		if v == 0 {
			return nil, errors.New("key version must be 1-255")
		}
		if err := s.AddKey(v, k); err != nil {
			return nil, fmt.Errorf("key version %d: %w", v, err)
		}
	}
	return s, nil
}

// Current is the key version new values are sealed with.
func (s *Sealer) Current() byte { return s.current }

// Version reports which key version sealed a value (0 if malformed).
func Version(sealed []byte) byte {
	if len(sealed) == 0 {
		return 0
	}
	return sealed[0]
}

func (s *Sealer) AddKey(version byte, key []byte) error {
	if len(key) != 32 {
		return errors.New("encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	s.aeads[version] = aead
	return nil
}

// Seal encrypts plaintext. aad binds the ciphertext to its context (for
// example the owning user id) so it cannot be swapped between rows.
func (s *Sealer) Seal(plaintext, aad []byte) ([]byte, error) {
	aead := s.aeads[s.current]
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	out := append([]byte{s.current}, nonce...)
	return aead.Seal(out, nonce, plaintext, aad), nil
}

func (s *Sealer) Open(sealed, aad []byte) ([]byte, error) {
	if len(sealed) < 1 {
		return nil, errors.New("ciphertext too short")
	}
	aead, ok := s.aeads[sealed[0]]
	if !ok {
		return nil, fmt.Errorf("unknown key version %d", sealed[0])
	}
	ns := aead.NonceSize()
	if len(sealed) < 1+ns+aead.Overhead() {
		return nil, errors.New("ciphertext too short")
	}
	return aead.Open(nil, sealed[1:1+ns], sealed[1+ns:], aad)
}
