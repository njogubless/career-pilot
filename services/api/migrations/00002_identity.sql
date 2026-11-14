-- +goose Up
CREATE TABLE users (
    id                uuid PRIMARY KEY,
    email             citext NOT NULL UNIQUE,
    password_hash     text NOT NULL,
    display_name      text NOT NULL CHECK (char_length(display_name) BETWEEN 1 AND 120),
    status            text NOT NULL DEFAULT 'ACTIVE' CHECK (status IN ('ACTIVE', 'DISABLED', 'PENDING_DELETION')),
    email_verified_at timestamptz,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Opaque session tokens; only SHA-256 hashes are stored (ADR-003).
CREATE TABLE user_sessions (
    id              uuid PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash      bytea NOT NULL UNIQUE,
    expires_at      timestamptz NOT NULL,
    idle_expires_at timestamptz NOT NULL,
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    ip              text,
    user_agent      text,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX user_sessions_user_idx ON user_sessions (user_id) WHERE revoked_at IS NULL;

CREATE TABLE password_reset_tokens (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    token_hash bytea NOT NULL UNIQUE,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX password_reset_tokens_user_idx ON password_reset_tokens (user_id);

-- Append-only audit trail. user_id is nullable for events without a known
-- user (e.g. failed login for an unknown email); no FK so records survive
-- account deletion only as anonymised rows (user_id set NULL by app on delete).
CREATE TABLE audit_events (
    id          uuid PRIMARY KEY,
    user_id     uuid,
    actor       text NOT NULL CHECK (actor IN ('USER', 'AGENT', 'SYSTEM')),
    action      text NOT NULL,
    subject_type text,
    subject_id  uuid,
    outcome     text NOT NULL CHECK (outcome IN ('SUCCESS', 'FAILURE', 'DENIED')),
    metadata    jsonb NOT NULL DEFAULT '{}'::jsonb,
    ip          text,
    request_id  text,
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX audit_events_user_idx ON audit_events (user_id, created_at DESC);

-- +goose Down
DROP TABLE audit_events;
DROP TABLE password_reset_tokens;
DROP TABLE user_sessions;
DROP TABLE users;
