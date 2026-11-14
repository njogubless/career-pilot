// Package auth implements registration, login, server-side sessions, CSRF
// protection and password recovery (ADR-003).
package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/mail"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/careerpilot/careerpilot/services/api/internal/audit"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/crypto"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/httpx"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ids"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/mailer"
	"github.com/careerpilot/careerpilot/services/api/internal/platform/ratelimit"
)

type Settings struct {
	IdleTTL     time.Duration
	AbsoluteTTL time.Duration
	ResetTTL    time.Duration
	AppBaseURL  string
	Secret      []byte // HMAC key for CSRF token derivation
	Argon2      crypto.Argon2Params
}

type Service struct {
	pool    *pgxpool.Pool
	limiter ratelimit.Limiter
	mail    mailer.Mailer
	cfg     Settings
	log     *slog.Logger
	now     func() time.Time
}

func NewService(pool *pgxpool.Pool, limiter ratelimit.Limiter, m mailer.Mailer, cfg Settings, log *slog.Logger) *Service {
	if cfg.ResetTTL == 0 {
		cfg.ResetTTL = 30 * time.Minute
	}
	return &Service{pool: pool, limiter: limiter, mail: m, cfg: cfg, log: log, now: time.Now}
}

// RequestMeta carries client details used for rate limiting and auditing.
type RequestMeta struct {
	IP        string
	UserAgent string
}

// NewSession is returned once at login; Token goes into the HttpOnly cookie.
type NewSession struct {
	Token     string
	CSRFToken string
	ExpiresAt time.Time
	User      User
}

var (
	errInvalidCredentials = httpx.NewError(httpx.CodeUnauthenticated, "The email or password is incorrect.")
	errRateLimited        = httpx.NewError(httpx.CodeRateLimited, "Too many attempts. Please wait and try again.")
)

// CSRFToken derives the CSRF token for a session. It is stable for the
// session's lifetime and cannot be computed without the server secret.
func (s *Service) CSRFToken(sessionID uuid.UUID) string {
	mac := hmac.New(sha256.New, s.cfg.Secret)
	mac.Write([]byte("csrf:"))
	mac.Write(sessionID[:])
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func (s *Service) validCSRF(sessionID uuid.UUID, token string) bool {
	return token != "" && hmac.Equal([]byte(s.CSRFToken(sessionID)), []byte(token))
}

// ---- Validation ----

type RegisterInput struct {
	Email       string `json:"email"`
	Password    string `json:"password"`
	DisplayName string `json:"display_name"`
}

func normalizeEmail(raw string) (string, bool) {
	e := strings.TrimSpace(raw)
	if len(e) == 0 || len(e) > 254 {
		return "", false
	}
	addr, err := mail.ParseAddress(e)
	if err != nil || addr.Address != e || !strings.Contains(e[strings.LastIndex(e, "@"):], ".") {
		return "", false
	}
	return strings.ToLower(e), true
}

var commonPasswords = map[string]bool{
	"password1234": true, "passwordpassword": true, "1234567890": true, "qwertyuiop": true,
	"123456789012": true, "iloveyou1234": true, "letmein12345": true, "administrator": true,
	"welcome12345": true, "careerpilot1": true, "careerpilot123": true, "0123456789": true,
}

func validatePassword(p string) string {
	n := utf8.RuneCountInString(p)
	switch {
	case n < 10:
		return "Use at least 10 characters."
	case n > 256:
		return "Use at most 256 characters."
	case commonPasswords[strings.ToLower(p)]:
		return "This password is too common."
	}
	return ""
}

func validateDisplayName(n string) (string, string) {
	n = strings.TrimSpace(n)
	if l := utf8.RuneCountInString(n); l < 1 || l > 120 {
		return "", "Enter a name between 1 and 120 characters."
	}
	return n, ""
}

// ---- Operations ----

func (s *Service) allow(ctx context.Context, key string, limit int, window time.Duration) error {
	ok, _, err := s.limiter.Allow(ctx, key, limit, window)
	if err != nil {
		// Fail closed would lock everyone out if Redis blips; fail open but log.
		s.log.Error("rate limiter unavailable", "err", err)
		return nil
	}
	if !ok {
		return errRateLimited
	}
	return nil
}

func (s *Service) Register(ctx context.Context, in RegisterInput, meta RequestMeta) (NewSession, error) {
	if err := s.allow(ctx, "register:ip:"+meta.IP, 10, time.Hour); err != nil {
		return NewSession{}, err
	}
	details := map[string]string{}
	email, ok := normalizeEmail(in.Email)
	if !ok {
		details["email"] = "Enter a valid email address."
	}
	if msg := validatePassword(in.Password); msg != "" {
		details["password"] = msg
	}
	name, msg := validateDisplayName(in.DisplayName)
	if msg != "" {
		details["display_name"] = msg
	}
	if len(details) > 0 {
		return NewSession{}, httpx.Validation(details)
	}

	hash, err := crypto.HashPassword(in.Password, s.cfg.Argon2)
	if err != nil {
		return NewSession{}, err
	}
	u := User{ID: ids.New(), Email: email, DisplayName: name, Status: "ACTIVE", Role: "user", CreatedAt: s.now()}
	u.PasswordHash = hash

	var sess NewSession
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := insertUser(ctx, tx, u); err != nil {
			if db.IsUniqueViolation(err, "users_email_key") {
				return &httpx.Error{Code: httpx.CodeConflict, Message: "An account with this email already exists.",
					Details: map[string]string{"email": "Already registered. Sign in or reset your password."}}
			}
			return fmt.Errorf("insert user: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO professional_profiles (user_id) VALUES ($1)`, u.ID); err != nil {
			return fmt.Errorf("create profile: %w", err)
		}
		if _, err := tx.Exec(ctx, `INSERT INTO user_preferences (user_id) VALUES ($1)`, u.ID); err != nil {
			return fmt.Errorf("create preferences: %w", err)
		}
		var err error
		sess, err = s.createSession(ctx, tx, u, meta)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &u.ID, Action: "auth.register", Outcome: audit.Success, IP: meta.IP})
	})
	return sess, err
}

func (s *Service) createSession(ctx context.Context, q db.Querier, u User, meta RequestMeta) (NewSession, error) {
	now := s.now()
	id := ids.New()
	token := crypto.NewToken()
	expires := now.Add(s.cfg.AbsoluteTTL)
	idle := now.Add(s.cfg.IdleTTL)
	if err := insertSession(ctx, q, id, u.ID, crypto.HashToken(token), expires, idle, meta.IP, meta.UserAgent); err != nil {
		return NewSession{}, fmt.Errorf("insert session: %w", err)
	}
	return NewSession{Token: token, CSRFToken: s.CSRFToken(id), ExpiresAt: expires, User: u}, nil
}

type LoginInput struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

func (s *Service) Login(ctx context.Context, in LoginInput, meta RequestMeta) (NewSession, error) {
	email, _ := normalizeEmail(in.Email)
	if err := s.allow(ctx, "login:ip:"+meta.IP, 30, 15*time.Minute); err != nil {
		return NewSession{}, err
	}
	if email != "" {
		if err := s.allow(ctx, "login:email:"+email, 8, 15*time.Minute); err != nil {
			return NewSession{}, err
		}
	}
	if email == "" || in.Password == "" || len(in.Password) > 1024 {
		crypto.DummyVerify(in.Password)
		return NewSession{}, errInvalidCredentials
	}

	u, err := userByEmail(ctx, s.pool, email)
	switch {
	case errors.Is(err, errNotFound):
		crypto.DummyVerify(in.Password)
		_ = audit.Record(ctx, s.pool, audit.Event{Action: "auth.login", Outcome: audit.Failure, IP: meta.IP,
			Metadata: map[string]any{"reason": "unknown_email"}})
		return NewSession{}, errInvalidCredentials
	case err != nil:
		return NewSession{}, fmt.Errorf("lookup user: %w", err)
	}
	ok, err := crypto.VerifyPassword(in.Password, u.PasswordHash)
	if err != nil {
		return NewSession{}, fmt.Errorf("verify password: %w", err)
	}
	if !ok || u.Status != "ACTIVE" {
		reason := "bad_password"
		if ok {
			reason = "inactive_account"
		}
		_ = audit.Record(ctx, s.pool, audit.Event{UserID: &u.ID, Action: "auth.login", Outcome: audit.Failure, IP: meta.IP,
			Metadata: map[string]any{"reason": reason}})
		return NewSession{}, errInvalidCredentials
	}

	var sess NewSession
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		var err error
		if sess, err = s.createSession(ctx, tx, u, meta); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &u.ID, Action: "auth.login", Outcome: audit.Success, IP: meta.IP})
	})
	return sess, err
}

// Authenticate resolves a session token to a principal, sliding the idle
// expiry at most once per minute to limit write load.
func (s *Service) Authenticate(ctx context.Context, token string) (Principal, error) {
	if token == "" || len(token) > 200 {
		return Principal{}, errNotFound
	}
	now := s.now()
	row, err := activeSession(ctx, s.pool, crypto.HashToken(token), now)
	if err != nil {
		return Principal{}, err
	}
	if row.UserStatus != "ACTIVE" {
		return Principal{}, errNotFound
	}
	if now.Sub(row.LastSeenAt) > time.Minute {
		if err := touchSession(ctx, s.pool, row.ID, now, now.Add(s.cfg.IdleTTL)); err != nil {
			s.log.Warn("touch session failed", "err", err)
		}
	}
	return Principal{UserID: row.UserID, SessionID: row.ID, Email: row.Email, DisplayName: row.DisplayName, Role: row.Role}, nil
}

func (s *Service) Logout(ctx context.Context, p Principal, meta RequestMeta) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := revokeSession(ctx, tx, p.SessionID, p.UserID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &p.UserID, Action: "auth.logout", Outcome: audit.Success, IP: meta.IP})
	})
}

// LogoutAll revokes every session for the user, including the current one.
func (s *Service) LogoutAll(ctx context.Context, p Principal, meta RequestMeta) error {
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		n, err := revokeAllSessions(ctx, tx, p.UserID, nil)
		if err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &p.UserID, Action: "auth.logout_all", Outcome: audit.Success, IP: meta.IP,
			Metadata: map[string]any{"sessions_revoked": n}})
	})
}

// ForgotPassword always succeeds from the caller's perspective so responses
// do not reveal whether an email is registered.
func (s *Service) ForgotPassword(ctx context.Context, rawEmail string, meta RequestMeta) error {
	if err := s.allow(ctx, "forgot:ip:"+meta.IP, 10, time.Hour); err != nil {
		return err
	}
	email, ok := normalizeEmail(rawEmail)
	if !ok {
		return httpx.Validation(map[string]string{"email": "Enter a valid email address."})
	}
	if err := s.allow(ctx, "forgot:email:"+email, 3, time.Hour); err != nil {
		return nil // silently drop: do not leak rate state per email
	}
	u, err := userByEmail(ctx, s.pool, email)
	if errors.Is(err, errNotFound) || (err == nil && u.Status != "ACTIVE") {
		return nil
	}
	if err != nil {
		return fmt.Errorf("lookup user: %w", err)
	}
	token := crypto.NewToken()
	err = db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := insertResetToken(ctx, tx, ids.New(), u.ID, crypto.HashToken(token), s.now().Add(s.cfg.ResetTTL)); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &u.ID, Action: "auth.password_reset_requested", Outcome: audit.Success, IP: meta.IP})
	})
	if err != nil {
		return err
	}
	link := s.cfg.AppBaseURL + "/reset-password#token=" + token // fragment: never sent to servers/proxies in Referer or logs
	msg := mailer.Message{
		To:      u.Email,
		Subject: "Reset your CareerPilot password",
		Text: fmt.Sprintf("Hello %s,\n\nA password reset was requested for your CareerPilot account. "+
			"Open this link within %d minutes to choose a new password:\n\n%s\n\n"+
			"If you did not request this, you can ignore this email; your password will not change.\n",
			u.DisplayName, int(s.cfg.ResetTTL.Minutes()), link),
	}
	if err := s.mail.Send(ctx, msg); err != nil {
		s.log.Error("password reset email failed", "err", err)
	}
	return nil
}

type ResetInput struct {
	Token    string `json:"token"`
	Password string `json:"password"`
}

func (s *Service) ResetPassword(ctx context.Context, in ResetInput, meta RequestMeta) error {
	if err := s.allow(ctx, "reset:ip:"+meta.IP, 20, time.Hour); err != nil {
		return err
	}
	if msg := validatePassword(in.Password); msg != "" {
		return httpx.Validation(map[string]string{"password": msg})
	}
	hash, err := crypto.HashPassword(in.Password, s.cfg.Argon2)
	if err != nil {
		return err
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		userID, err := consumeResetToken(ctx, tx, crypto.HashToken(in.Token), s.now())
		if errors.Is(err, errNotFound) {
			return httpx.NewError(httpx.CodeValidationFailed, "This reset link is invalid or has expired. Request a new one.")
		}
		if err != nil {
			return err
		}
		if err := updatePassword(ctx, tx, userID, hash); err != nil {
			return err
		}
		if _, err := revokeAllSessions(ctx, tx, userID, nil); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &userID, Action: "auth.password_reset", Outcome: audit.Success, IP: meta.IP})
	})
}

type ChangePasswordInput struct {
	CurrentPassword string `json:"current_password"`
	NewPassword     string `json:"new_password"`
}

// ChangePassword verifies the current password and revokes all other sessions.
func (s *Service) ChangePassword(ctx context.Context, p Principal, in ChangePasswordInput, meta RequestMeta) error {
	if err := s.allow(ctx, "chpw:user:"+p.UserID.String(), 10, time.Hour); err != nil {
		return err
	}
	if msg := validatePassword(in.NewPassword); msg != "" {
		return httpx.Validation(map[string]string{"new_password": msg})
	}
	u, err := userByID(ctx, s.pool, p.UserID)
	if err != nil {
		return err
	}
	ok, err := crypto.VerifyPassword(in.CurrentPassword, u.PasswordHash)
	if err != nil {
		return err
	}
	if !ok {
		return httpx.Validation(map[string]string{"current_password": "The current password is incorrect."})
	}
	hash, err := crypto.HashPassword(in.NewPassword, s.cfg.Argon2)
	if err != nil {
		return err
	}
	return db.InTx(ctx, s.pool, func(tx pgx.Tx) error {
		if err := updatePassword(ctx, tx, p.UserID, hash); err != nil {
			return err
		}
		if _, err := revokeAllSessions(ctx, tx, p.UserID, &p.SessionID); err != nil {
			return err
		}
		return audit.Record(ctx, tx, audit.Event{UserID: &p.UserID, Action: "auth.password_changed", Outcome: audit.Success, IP: meta.IP})
	})
}

func (s *Service) UpdateDisplayName(ctx context.Context, p Principal, raw string) (User, error) {
	name, msg := validateDisplayName(raw)
	if msg != "" {
		return User{}, httpx.Validation(map[string]string{"display_name": msg})
	}
	if err := updateDisplayName(ctx, s.pool, p.UserID, name); err != nil {
		return User{}, err
	}
	return userByID(ctx, s.pool, p.UserID)
}

func (s *Service) User(ctx context.Context, id uuid.UUID) (User, error) {
	return userByID(ctx, s.pool, id)
}
