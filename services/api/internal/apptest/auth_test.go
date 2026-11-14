package apptest

import (
	"net/http"
	"strings"
	"testing"
)

func TestRegisterLoginLogout(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("Amani@Example.com")

	r := c.Do(http.MethodGet, "/api/v1/auth/session", nil)
	if r.Code != http.StatusOK {
		t.Fatalf("session: %d %s", r.Code, r.Body)
	}
	var s struct {
		User struct{ Email string } `json:"user"`
	}
	r.JSON(t, &s)
	if s.User.Email != "amani@example.com" {
		t.Fatalf("email not normalised: %q", s.User.Email)
	}
	if strings.Contains(string(r.Body), "password") {
		t.Fatal("password hash leaked in session response")
	}
	// Profile and preferences rows are created at registration.
	if n := app.QueryInt(t, `SELECT count(*) FROM professional_profiles`); n != 1 {
		t.Fatalf("profiles = %d", n)
	}

	if r := c.Do(http.MethodPost, "/api/v1/auth/logout", nil); r.Code != http.StatusNoContent {
		t.Fatalf("logout: %d %s", r.Code, r.Body)
	}
	if r := c.Do(http.MethodGet, "/api/v1/auth/session", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("session after logout: %d", r.Code)
	}

	if r := c.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "amani@example.com", "password": "a-strong-passphrase"}); r.Code != http.StatusOK {
		t.Fatalf("login: %d %s", r.Code, r.Body)
	}
	if n := app.QueryInt(t, `SELECT count(*) FROM audit_events WHERE action = 'auth.login' AND outcome = 'SUCCESS'`); n != 1 {
		t.Fatalf("login audit events = %d", n)
	}
}

func TestRegisterValidationAndDuplicates(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	r := c.Do(http.MethodPost, "/api/v1/auth/register", map[string]string{"email": "bad", "password": "short", "display_name": ""})
	if r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("expected 422, got %d", r.Code)
	}
	for _, f := range []string{"email", "password", "display_name"} {
		if !strings.Contains(string(r.Body), `"`+f+`"`) {
			t.Errorf("missing field error for %s: %s", f, r.Body)
		}
	}
	c.Register("dup@example.com")
	r = app.Client(t).Do(http.MethodPost, "/api/v1/auth/register", map[string]string{"email": "DUP@example.com", "password": "a-strong-passphrase", "display_name": "X"})
	if r.Code != http.StatusConflict {
		t.Fatalf("duplicate: %d %s", r.Code, r.Body)
	}
}

func TestLoginFailuresAreGenericAndRateLimited(t *testing.T) {
	app := New(t, Options{})
	app.Client(t).Register("user@example.com")
	c := app.Client(t)
	unknown := c.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "nobody@example.com", "password": "whatever-123"})
	wrong := c.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "user@example.com", "password": "wrong-password"})
	if unknown.Code != http.StatusUnauthorized || wrong.Code != http.StatusUnauthorized {
		t.Fatalf("codes: %d %d", unknown.Code, wrong.Code)
	}
	// Same message for unknown email and wrong password (no enumeration).
	strip := func(b []byte) string { return strings.Split(string(b), `"request_id"`)[0] }
	if strip(unknown.Body) != strip(wrong.Body) {
		t.Fatalf("responses differ:\n%s\n%s", unknown.Body, wrong.Body)
	}
	var last Response
	for i := 0; i < 10; i++ {
		last = c.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "user@example.com", "password": "wrong-password"})
	}
	if last.Code != http.StatusTooManyRequests {
		t.Fatalf("expected rate limiting, got %d", last.Code)
	}
}

func TestCSRFRequiredForStateChanges(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("csrf@example.com")
	r := c.DoNoCSRF(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{"headline": "x", "links": []any{}})
	if r.Code != http.StatusForbidden {
		t.Fatalf("expected 403 without CSRF token, got %d", r.Code)
	}
	saved := c.CSRF
	c.CSRF = "forged-token"
	r = c.Do(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{"headline": "x", "links": []any{}})
	c.CSRF = saved
	if r.Code != http.StatusForbidden {
		t.Fatalf("expected 403 with forged token, got %d", r.Code)
	}
	if r := c.Do(http.MethodPut, "/api/v1/profiles/me/basics", map[string]any{"headline": "x", "links": []any{}}); r.Code != http.StatusOK {
		t.Fatalf("with token: %d %s", r.Code, r.Body)
	}
}

func TestUnauthenticatedAccessRejected(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	for _, p := range []string{"/api/v1/profiles/me", "/api/v1/documents", "/api/v1/users/me"} {
		if r := c.Do(http.MethodGet, p, nil); r.Code != http.StatusUnauthorized {
			t.Errorf("%s: %d", p, r.Code)
		}
	}
}

func TestPasswordResetFlow(t *testing.T) {
	app := New(t, Options{})
	c := app.Client(t)
	c.Register("reset@example.com")

	anon := app.Client(t)
	if r := anon.Do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "unknown@example.com"}); r.Code != http.StatusAccepted {
		t.Fatalf("forgot unknown: %d", r.Code)
	}
	if _, ok := app.Mail.Last(); ok {
		t.Fatal("no email should be sent for unknown accounts")
	}
	if r := anon.Do(http.MethodPost, "/api/v1/auth/password/forgot", map[string]string{"email": "reset@example.com"}); r.Code != http.StatusAccepted {
		t.Fatalf("forgot: %d", r.Code)
	}
	msg, ok := app.Mail.Last()
	if !ok {
		t.Fatal("reset email not sent")
	}
	i := strings.Index(msg.Text, "#token=")
	if i < 0 {
		t.Fatalf("no token link in email: %s", msg.Text)
	}
	token := strings.Fields(msg.Text[i+len("#token="):])[0]

	if r := anon.Do(http.MethodPost, "/api/v1/auth/password/reset", map[string]string{"token": token, "password": "brand-new-passphrase"}); r.Code != http.StatusNoContent {
		t.Fatalf("reset: %d %s", r.Code, r.Body)
	}
	// Token is single use.
	if r := anon.Do(http.MethodPost, "/api/v1/auth/password/reset", map[string]string{"token": token, "password": "another-passphrase"}); r.Code != http.StatusUnprocessableEntity {
		t.Fatalf("reuse: %d", r.Code)
	}
	// Existing sessions were revoked.
	if r := c.Do(http.MethodGet, "/api/v1/auth/session", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("old session still valid: %d", r.Code)
	}
	if r := anon.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "reset@example.com", "password": "brand-new-passphrase"}); r.Code != http.StatusOK {
		t.Fatalf("login with new password: %d", r.Code)
	}
}

func TestChangePasswordRevokesOtherSessions(t *testing.T) {
	app := New(t, Options{})
	a := app.Client(t)
	a.Register("multi@example.com")
	b := app.Client(t)
	if r := b.Do(http.MethodPost, "/api/v1/auth/login", map[string]string{"email": "multi@example.com", "password": "a-strong-passphrase"}); r.Code != http.StatusOK {
		t.Fatal(r.Code)
	}
	r := a.Do(http.MethodPost, "/api/v1/auth/password/change", map[string]string{"current_password": "a-strong-passphrase", "new_password": "a-newer-passphrase"})
	if r.Code != http.StatusNoContent {
		t.Fatalf("change: %d %s", r.Code, r.Body)
	}
	if r := a.Do(http.MethodGet, "/api/v1/auth/session", nil); r.Code != http.StatusOK {
		t.Fatalf("current session should survive: %d", r.Code)
	}
	if r := b.Do(http.MethodGet, "/api/v1/auth/session", nil); r.Code != http.StatusUnauthorized {
		t.Fatalf("other session should be revoked: %d", r.Code)
	}
}
