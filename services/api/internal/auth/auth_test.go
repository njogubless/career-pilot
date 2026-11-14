package auth

import (
	"testing"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
)

func TestNormalizeEmail(t *testing.T) {
	ok := map[string]string{" A@Example.COM ": "a@example.com", "first.last+tag@sub.example.org": "first.last+tag@sub.example.org"}
	for in, want := range ok {
		if got, valid := normalizeEmail(in); !valid || got != want {
			t.Errorf("normalizeEmail(%q) = %q,%v", in, got, valid)
		}
	}
	for _, bad := range []string{"", "a", "a@b", "Name <a@b.com>", "a@b.com, c@d.com"} {
		if _, valid := normalizeEmail(bad); valid {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestValidatePassword(t *testing.T) {
	if validatePassword("short") == "" || validatePassword("password1234") == "" {
		t.Fatal("weak password accepted")
	}
	if validatePassword("a-strong-passphrase") != "" {
		t.Fatal("strong password rejected")
	}
}

func TestCSRFTokenBoundToSessionAndSecret(t *testing.T) {
	s1 := &Service{cfg: Settings{Secret: []byte("secret-one")}}
	s2 := &Service{cfg: Settings{Secret: []byte("secret-two")}}
	a, b := ids.New(), ids.New()
	if s1.CSRFToken(a) == s1.CSRFToken(b) {
		t.Fatal("token must differ per session")
	}
	if s1.CSRFToken(a) == s2.CSRFToken(a) {
		t.Fatal("token must depend on the secret")
	}
	if !s1.validCSRF(a, s1.CSRFToken(a)) || s1.validCSRF(a, "") || s1.validCSRF(b, s1.CSRFToken(a)) {
		t.Fatal("validation wrong")
	}
}
