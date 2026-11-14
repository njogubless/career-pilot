package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/careerpilot/careerpilot/services/api/internal/platform/db"
)

type User struct {
	ID           uuid.UUID `json:"id"`
	Email        string    `json:"email"`
	DisplayName  string    `json:"display_name"`
	Status       string    `json:"-"`
	Role         string    `json:"role"`
	PasswordHash string    `json:"-"`
	CreatedAt    time.Time `json:"created_at"`
}

var errNotFound = errors.New("not found")

const userCols = `id, email::text, display_name, status, role, password_hash, created_at`

func scanUser(row pgx.Row) (User, error) {
	var u User
	err := row.Scan(&u.ID, &u.Email, &u.DisplayName, &u.Status, &u.Role, &u.PasswordHash, &u.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return User{}, errNotFound
	}
	return u, err
}

func insertUser(ctx context.Context, q db.Querier, u User) error {
	_, err := q.Exec(ctx, `INSERT INTO users (id, email, password_hash, display_name) VALUES ($1, $2, $3, $4)`,
		u.ID, u.Email, u.PasswordHash, u.DisplayName)
	return err
}

func userByEmail(ctx context.Context, q db.Querier, email string) (User, error) {
	return scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE email = $1`, email))
}

func userByID(ctx context.Context, q db.Querier, id uuid.UUID) (User, error) {
	return scanUser(q.QueryRow(ctx, `SELECT `+userCols+` FROM users WHERE id = $1`, id))
}

type sessionRow struct {
	ID            uuid.UUID
	UserID        uuid.UUID
	ExpiresAt     time.Time
	IdleExpiresAt time.Time
	LastSeenAt    time.Time
	Email         string
	DisplayName   string
	UserStatus    string
	Role          string
}

func insertSession(ctx context.Context, q db.Querier, id, userID uuid.UUID, tokenHash []byte, expires, idle time.Time, ip, ua string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO user_sessions (id, user_id, token_hash, expires_at, idle_expires_at, ip, user_agent)
		VALUES ($1, $2, $3, $4, $5, NULLIF($6, ''), NULLIF($7, ''))`,
		id, userID, tokenHash, expires, idle, ip, truncate(ua, 300))
	return err
}

// activeSession returns a session that is not revoked or expired.
func activeSession(ctx context.Context, q db.Querier, tokenHash []byte, now time.Time) (sessionRow, error) {
	var s sessionRow
	err := q.QueryRow(ctx, `
		SELECT s.id, s.user_id, s.expires_at, s.idle_expires_at, s.last_seen_at, u.email::text, u.display_name, u.status, u.role
		FROM user_sessions s JOIN users u ON u.id = s.user_id
		WHERE s.token_hash = $1 AND s.revoked_at IS NULL AND s.expires_at > $2 AND s.idle_expires_at > $2`,
		tokenHash, now).Scan(&s.ID, &s.UserID, &s.ExpiresAt, &s.IdleExpiresAt, &s.LastSeenAt, &s.Email, &s.DisplayName, &s.UserStatus, &s.Role)
	if errors.Is(err, pgx.ErrNoRows) {
		return sessionRow{}, errNotFound
	}
	return s, err
}

func touchSession(ctx context.Context, q db.Querier, id uuid.UUID, now, idle time.Time) error {
	_, err := q.Exec(ctx, `UPDATE user_sessions SET last_seen_at = $2, idle_expires_at = LEAST($3, expires_at) WHERE id = $1`, id, now, idle)
	return err
}

func revokeSession(ctx context.Context, q db.Querier, id, userID uuid.UUID) error {
	_, err := q.Exec(ctx, `UPDATE user_sessions SET revoked_at = now() WHERE id = $1 AND user_id = $2 AND revoked_at IS NULL`, id, userID)
	return err
}

// revokeAllSessions revokes every session for the user except keep (if non-nil).
func revokeAllSessions(ctx context.Context, q db.Querier, userID uuid.UUID, keep *uuid.UUID) (int64, error) {
	tag, err := q.Exec(ctx, `UPDATE user_sessions SET revoked_at = now()
		WHERE user_id = $1 AND revoked_at IS NULL AND ($2::uuid IS NULL OR id <> $2)`, userID, keep)
	return tag.RowsAffected(), err
}

func insertResetToken(ctx context.Context, q db.Querier, id, userID uuid.UUID, hash []byte, expires time.Time) error {
	_, err := q.Exec(ctx, `INSERT INTO password_reset_tokens (id, user_id, token_hash, expires_at) VALUES ($1, $2, $3, $4)`,
		id, userID, hash, expires)
	return err
}

// consumeResetToken atomically marks a valid token used and returns its user.
func consumeResetToken(ctx context.Context, q db.Querier, hash []byte, now time.Time) (uuid.UUID, error) {
	var userID uuid.UUID
	err := q.QueryRow(ctx, `
		UPDATE password_reset_tokens SET used_at = $2
		WHERE token_hash = $1 AND used_at IS NULL AND expires_at > $2
		RETURNING user_id`, hash, now).Scan(&userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, errNotFound
	}
	return userID, err
}

func updatePassword(ctx context.Context, q db.Querier, userID uuid.UUID, hash string) error {
	_, err := q.Exec(ctx, `UPDATE users SET password_hash = $2, updated_at = now() WHERE id = $1`, userID, hash)
	return err
}

func updateDisplayName(ctx context.Context, q db.Querier, userID uuid.UUID, name string) error {
	_, err := q.Exec(ctx, `UPDATE users SET display_name = $2, updated_at = now() WHERE id = $1`, userID, name)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// SetRole changes a user's role (operator CLI only; never exposed over HTTP).
func SetRole(ctx context.Context, q db.Querier, email, role string) error {
	tag, err := q.Exec(ctx, `UPDATE users SET role = $2, updated_at = now() WHERE email = $1`, strings.ToLower(strings.TrimSpace(email)), role)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return errNotFound
	}
	return nil
}
