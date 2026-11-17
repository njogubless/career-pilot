-- +goose Up
-- Phase 7: recruiter outreach and Gmail (ADR-023).

-- One connected mailbox per user. Tokens are AES-256-GCM sealed and bound to
-- the user id; they never leave the API process in plaintext.
CREATE TABLE email_accounts (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    provider          text NOT NULL CHECK (provider IN ('GMAIL')),
    email             citext NOT NULL,
    scopes            text[] NOT NULL,
    access_token_enc  bytea NOT NULL,
    refresh_token_enc bytea NOT NULL,
    access_expires_at timestamptz NOT NULL,
    status            text NOT NULL CHECK (status IN ('CONNECTED', 'NEEDS_RECONNECT')),
    last_error        text,
    connected_at      timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- Short-lived OAuth state (CSRF protection + PKCE verifier).
CREATE TABLE oauth_states (
    state_hash    bytea PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    verifier_enc  bytea NOT NULL,
    expires_at    timestamptz NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE outreach_settings (
    user_id                 uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    daily_send_limit        integer NOT NULL DEFAULT 10 CHECK (daily_send_limit BETWEEN 1 AND 50),
    recipient_cooldown_days integer NOT NULL DEFAULT 3 CHECK (recipient_cooldown_days BETWEEN 1 AND 60),
    follow_ups_enabled      boolean NOT NULL DEFAULT true,
    follow_up_after_days    integer NOT NULL DEFAULT 5 CHECK (follow_up_after_days BETWEEN 2 AND 30),
    max_follow_ups          integer NOT NULL DEFAULT 1 CHECK (max_follow_ups BETWEEN 0 AND 2),
    signature               text NOT NULL DEFAULT '' CHECK (char_length(signature) <= 500),
    updated_at              timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE recruiter_contacts (
    id                 uuid PRIMARY KEY,
    user_id            uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name               text NOT NULL DEFAULT '' CHECK (char_length(name) <= 200),
    title              text NOT NULL DEFAULT '' CHECK (char_length(title) <= 200),
    company            text NOT NULL DEFAULT '' CHECK (char_length(company) <= 200),
    job_id             uuid REFERENCES jobs(id) ON DELETE SET NULL,
    profile_url        text NOT NULL DEFAULT '' CHECK (char_length(profile_url) <= 500),
    email              citext CHECK (char_length(email) <= 320),
    -- Where the address came from; never guessed.
    source             text NOT NULL CHECK (source IN ('USER_PROVIDED', 'JOB_POSTING')),
    source_evidence    text NOT NULL DEFAULT '' CHECK (char_length(source_evidence) <= 500),
    -- UNVERIFIED: not checked. DOMAIN_OK: the domain accepts mail (MX). INVALID: bad
    -- syntax or no mail domain. BOUNCED: a message to it bounced.
    email_verification text NOT NULL DEFAULT 'UNVERIFIED'
                       CHECK (email_verification IN ('UNVERIFIED', 'DOMAIN_OK', 'INVALID', 'BOUNCED')),
    verified_at        timestamptz,
    confidence         text NOT NULL CHECK (confidence IN ('HIGH', 'MEDIUM', 'LOW')),
    do_not_contact     boolean NOT NULL DEFAULT false,
    do_not_contact_reason text NOT NULL DEFAULT '' CHECK (char_length(do_not_contact_reason) <= 500),
    notes              text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 2000),
    discovered_at      timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX recruiter_contacts_email_idx ON recruiter_contacts (user_id, email) WHERE email IS NOT NULL;
CREATE INDEX recruiter_contacts_user_idx ON recruiter_contacts (user_id, updated_at DESC);

CREATE TABLE outreach_messages (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    contact_id        uuid NOT NULL REFERENCES recruiter_contacts(id) ON DELETE RESTRICT,
    job_id            uuid REFERENCES jobs(id) ON DELETE SET NULL,
    application_id    uuid REFERENCES applications(id) ON DELETE SET NULL,
    parent_id         uuid REFERENCES outreach_messages(id) ON DELETE SET NULL,
    kind              text NOT NULL CHECK (kind IN ('INTRODUCTION', 'FOLLOW_UP')),
    -- DRAFT → APPROVED → SENDING → SENT | FAILED | UNKNOWN; DRAFT/APPROVED → CANCELLED.
    status            text NOT NULL CHECK (status IN ('DRAFT', 'APPROVED', 'SENDING', 'SENT', 'FAILED', 'UNKNOWN', 'CANCELLED')),
    origin            text NOT NULL CHECK (origin IN ('AI', 'TEMPLATE', 'USER', 'FOLLOW_UP_SCHEDULER')),
    to_email          citext NOT NULL,
    subject           text NOT NULL CHECK (char_length(subject) BETWEEN 1 AND 200),
    body              text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 10000),
    -- Approved CV version attached (must be an approved document version).
    attachment_version_id uuid REFERENCES application_document_versions(id) ON DELETE RESTRICT,
    validation        jsonb,
    -- Approval is bound to the exact content: editing invalidates it.
    approved_hash     bytea,
    approved_at       timestamptz,
    idempotency_key   text,
    rfc_message_id    text,
    gmail_message_id  text,
    gmail_thread_id   text,
    sent_at           timestamptz,
    error_message     text CHECK (char_length(error_message) <= 1000),
    replied_at        timestamptz,
    prompt_version    text,
    model             text,
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, idempotency_key),
    CHECK ((status = 'SENT') <= (gmail_message_id IS NOT NULL AND sent_at IS NOT NULL)),
    CHECK ((status IN ('APPROVED', 'SENDING', 'SENT', 'FAILED', 'UNKNOWN')) <= (approved_hash IS NOT NULL))
);
CREATE INDEX outreach_messages_user_idx ON outreach_messages (user_id, updated_at DESC);
CREATE INDEX outreach_messages_thread_idx ON outreach_messages (gmail_thread_id) WHERE gmail_thread_id IS NOT NULL;
CREATE INDEX outreach_messages_sent_idx ON outreach_messages (user_id, sent_at DESC) WHERE status = 'SENT';

-- Replies are recorded as metadata only: no message bodies are stored.
CREATE TABLE outreach_replies (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id       uuid NOT NULL REFERENCES outreach_messages(id) ON DELETE CASCADE,
    application_id   uuid REFERENCES applications(id) ON DELETE SET NULL,
    gmail_message_id text NOT NULL,
    from_address     text NOT NULL CHECK (char_length(from_address) <= 320),
    is_bounce        boolean NOT NULL DEFAULT false,
    received_at      timestamptz NOT NULL,
    created_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, gmail_message_id)
);
CREATE INDEX outreach_replies_message_idx ON outreach_replies (message_id);

CREATE TABLE outreach_follow_ups (
    id               uuid PRIMARY KEY,
    user_id          uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    message_id       uuid NOT NULL REFERENCES outreach_messages(id) ON DELETE CASCADE,
    sequence         integer NOT NULL CHECK (sequence BETWEEN 1 AND 2),
    due_at           timestamptz NOT NULL,
    status           text NOT NULL CHECK (status IN ('SCHEDULED', 'DRAFTED', 'CANCELLED')),
    draft_id         uuid REFERENCES outreach_messages(id) ON DELETE SET NULL,
    cancelled_reason text,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (message_id, sequence)
);
CREATE INDEX outreach_follow_ups_due_idx ON outreach_follow_ups (due_at) WHERE status = 'SCHEDULED';

-- +goose Down
DROP TABLE outreach_follow_ups;
DROP TABLE outreach_replies;
DROP TABLE outreach_messages;
DROP TABLE recruiter_contacts;
DROP TABLE outreach_settings;
DROP TABLE oauth_states;
DROP TABLE email_accounts;
