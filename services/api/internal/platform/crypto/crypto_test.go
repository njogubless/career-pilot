package crypto

import (
	"bytes"
	"strings"
	"testing"
)

var fastParams = Argon2Params{Memory: 8 * 1024, Time: 1, Threads: 1}

func TestPasswordRoundTrip(t *testing.T) {
	h, err := HashPassword("correct horse battery", fastParams)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=8192,t=1,p=1$") {
		t.Fatalf("unexpected encoding %q", h)
	}
	ok, err := VerifyPassword("correct horse battery", h)
	if err != nil || !ok {
		t.Fatalf("verify failed: ok=%v err=%v", ok, err)
	}
	ok, _ = VerifyPassword("wrong", h)
	if ok {
		t.Fatal("wrong password accepted")
	}
}

func TestPasswordHashesAreSalted(t *testing.T) {
	a, _ := HashPassword("same", fastParams)
	b, _ := HashPassword("same", fastParams)
	if a == b {
		t.Fatal("hashes must differ due to salt")
	}
}

func TestVerifyRejectsMalformed(t *testing.T) {
	for _, h := range []string{"", "plain", "$argon2i$v=19$m=1,t=1,p=1$aa$bb", "$argon2id$v=19$m=x$aa$bb"} {
		if _, err := VerifyPassword("x", h); err == nil {
			t.Errorf("expected error for %q", h)
		}
	}
}

func TestTokens(t *testing.T) {
	a, b := NewToken(), NewToken()
	if a == b || len(a) < 40 {
		t.Fatalf("weak tokens: %q %q", a, b)
	}
	if !EqualHash(HashToken(a), HashToken(a)) || EqualHash(HashToken(a), HashToken(b)) {
		t.Fatal("hash comparison wrong")
	}
}

func TestSealerRoundTripAndBinding(t *testing.T) {
	key := make([]byte, 32)
	key[0] = 7
	s, err := NewSealer(key)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := s.Seal([]byte("refresh-token"), []byte("user-1"))
	if err != nil {
		t.Fatal(err)
	}
	if pt, err := s.Open(ct, []byte("user-1")); err != nil || string(pt) != "refresh-token" {
		t.Fatalf("open: %q %v", pt, err)
	}
	if _, err := s.Open(ct, []byte("user-2")); err == nil {
		t.Fatal("ciphertext must be bound to its owner")
	}
	ct2, _ := s.Seal([]byte("refresh-token"), []byte("user-1"))
	if string(ct) == string(ct2) {
		t.Fatal("nonces must differ")
	}
	ct[len(ct)-1] ^= 1
	if _, err := s.Open(ct, []byte("user-1")); err == nil {
		t.Fatal("tampering must be detected")
	}
	if _, err := NewSealer([]byte("short")); err == nil {
		t.Fatal("short key accepted")
	}
}

func TestKeyringRotation(t *testing.T) {
	oldKey, newKey := bytes.Repeat([]byte{1}, 32), bytes.Repeat([]byte{2}, 32)
	old, err := NewKeyring(1, map[byte][]byte{1: oldKey})
	if err != nil {
		t.Fatal(err)
	}
	aad := []byte("user-1")
	sealed, err := old.Seal([]byte("refresh-token"), aad)
	if err != nil || Version(sealed) != 1 {
		t.Fatalf("seal v1: %v %d", err, Version(sealed))
	}
	// New current key, old one retired but readable.
	ring, err := NewKeyring(2, map[byte][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ring.Open(sealed, aad)
	if err != nil || string(pt) != "refresh-token" {
		t.Fatalf("open retired: %v", err)
	}
	resealed, err := ring.Seal(pt, aad)
	if err != nil || Version(resealed) != 2 {
		t.Fatalf("reseal: %v %d", err, Version(resealed))
	}
	// Once the retired key is removed, only re-sealed values open.
	newOnly, _ := NewKeyring(2, map[byte][]byte{2: newKey})
	if _, err := newOnly.Open(sealed, aad); err == nil {
		t.Fatal("v1 value opened without its key")
	}
	if _, err := newOnly.Open(resealed, aad); err != nil {
		t.Fatalf("v2 value: %v", err)
	}
	if _, err := NewKeyring(3, map[byte][]byte{2: newKey}); err == nil {
		t.Fatal("current version missing from keyring accepted")
	}
	if _, err := NewKeyring(0, map[byte][]byte{0: newKey}); err == nil {
		t.Fatal("version 0 accepted")
	}
}
