package apptest

import (
	"bytes"
	"context"
	"testing"

	"github.com/careerpilot/careerpilot/services/api/internal/gmail"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
)

func TestKeyRotationResealsStoredTokens(t *testing.T) {
	app := New(t, Options{})
	ctx := context.Background()
	oldKey, newKey, lostKey := bytes.Repeat([]byte{7}, 32), bytes.Repeat([]byte{8}, 32), bytes.Repeat([]byte{9}, 32)
	v1, _ := crypto.NewKeyring(1, map[byte][]byte{1: oldKey})
	lost, _ := crypto.NewKeyring(5, map[byte][]byte{5: lostKey})

	a, b := app.Client(t), app.Client(t)
	a.Register("keys-a@example.com")
	b.Register("keys-b@example.com")
	ua, ub := userIDByEmail(t, app, "keys-a@example.com"), userIDByEmail(t, app, "keys-b@example.com")
	insert := func(s *crypto.Sealer, uid [16]byte, token string) {
		acc, _ := s.Seal([]byte("access-"+token), uid[:])
		ref, _ := s.Seal([]byte("refresh-"+token), uid[:])
		app.Exec(t, `INSERT INTO email_accounts (user_id, provider, email, scopes, access_token_enc, refresh_token_enc, access_expires_at, status)
			VALUES ($1, 'GMAIL', 'x@example.com', '{}', $2, $3, now() + interval '1 hour', 'CONNECTED')`, uid, acc, ref)
	}
	insert(v1, ua, "a")
	insert(lost, ub, "b") // sealed with a key that is no longer configured

	ring, err := crypto.NewKeyring(2, map[byte][]byte{1: oldKey, 2: newKey})
	if err != nil {
		t.Fatal(err)
	}
	res, err := gmail.RotateKeys(ctx, app.Pool, ring)
	if err != nil {
		t.Fatal(err)
	}
	if res.Resealed != 1 || res.Unreadable != 1 || res.Current != 0 {
		t.Fatalf("unexpected result %+v", res)
	}
	// Re-sealed with v2, still bound to the user, opens without the old key.
	var acc, ref []byte
	if err := app.Pool.QueryRow(ctx, `SELECT access_token_enc, refresh_token_enc FROM email_accounts WHERE user_id = $1`, ua).Scan(&acc, &ref); err != nil {
		t.Fatal(err)
	}
	newOnly, _ := crypto.NewKeyring(2, map[byte][]byte{2: newKey})
	if pt, err := newOnly.Open(ref, ua[:]); err != nil || string(pt) != "refresh-a" || crypto.Version(acc) != 2 {
		t.Fatalf("resealed token: %q %v v%d", pt, err, crypto.Version(acc))
	}
	if _, err := newOnly.Open(ref, ub[:]); err == nil {
		t.Fatal("token opened with another user's binding")
	}
	// Idempotent: a second pass changes nothing.
	if res, err := gmail.RotateKeys(ctx, app.Pool, ring); err != nil || res.Resealed != 0 || res.Current != 1 || res.Unreadable != 1 {
		t.Fatalf("second pass %+v %v", res, err)
	}
	st, err := gmail.KeyVersions(ctx, app.Pool)
	if err != nil {
		t.Fatal(err)
	}
	got := map[byte]int64{}
	for _, s := range st {
		if s.Table == "email_accounts" {
			got[s.Version] = s.Count
		}
	}
	if got[2] != 1 || got[5] != 1 {
		t.Fatalf("key versions %+v", st)
	}
}
