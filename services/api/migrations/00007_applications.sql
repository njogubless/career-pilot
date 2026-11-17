-- +goose Up
-- Phase 6: application management and execution (ADR-022).

-- Per-user automation controls. Checked before every submission start and,
-- from Phase 8, before every agent action. A missing row means the defaults.
CREATE TABLE automation_settings (
    user_id                uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    mode                   text NOT NULL DEFAULT 'HUMAN_APPROVED'
                           CHECK (mode IN ('DISCOVERY_ONLY', 'HUMAN_APPROVED', 'CONTROLLED')),
    paused                 boolean NOT NULL DEFAULT false,
    paused_at              timestamptz,
    daily_submission_limit integer NOT NULL DEFAULT 20 CHECK (daily_submission_limit BETWEEN 1 AND 200),
    updated_at             timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE applications (
    id                      uuid PRIMARY KEY,
    user_id                 uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Jobs are expired, never deleted; RESTRICT keeps the history intact.
    job_id                  uuid NOT NULL REFERENCES jobs(id) ON DELETE RESTRICT,
    status                  text NOT NULL CHECK (status IN ('DISCOVERED', 'SHORTLISTED', 'PREPARING', 'AWAITING_APPROVAL',
                                'APPROVED', 'SUBMITTED', 'ACKNOWLEDGED', 'INTERVIEWING', 'ASSESSMENT', 'OFFER', 'REJECTED',
                                'WITHDRAWN', 'FAILED')),
    channel                 text CHECK (channel IN ('ASSISTED', 'EMAIL')),
    -- The approved package, pinned at approval time. Pinned versions cannot
    -- be deleted while an application references them.
    cv_version_id           uuid REFERENCES application_document_versions(id) ON DELETE RESTRICT,
    cover_letter_version_id uuid REFERENCES application_document_versions(id) ON DELETE RESTRICT,
    answers_version_id      uuid REFERENCES application_document_versions(id) ON DELETE RESTRICT,
    approved_at             timestamptz,
    submitted_at            timestamptz,
    notes                   text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 5000),
    created_at              timestamptz NOT NULL DEFAULT now(),
    updated_at              timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, job_id),
    CHECK ((status IN ('APPROVED', 'FAILED')) <= (cv_version_id IS NOT NULL AND channel IS NOT NULL)),
    CHECK ((status IN ('SUBMITTED', 'ACKNOWLEDGED', 'INTERVIEWING', 'ASSESSMENT', 'OFFER')) <= (submitted_at IS NOT NULL))
);
CREATE INDEX applications_user_idx ON applications (user_id, updated_at DESC);

CREATE TABLE application_status_history (
    id             uuid PRIMARY KEY,
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    from_status    text,
    to_status      text NOT NULL,
    actor          text NOT NULL CHECK (actor IN ('USER', 'AGENT', 'SYSTEM')),
    reason         text NOT NULL DEFAULT '' CHECK (char_length(reason) <= 1000),
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX application_status_history_app_idx ON application_status_history (application_id, created_at);
CREATE INDEX application_status_history_user_idx ON application_status_history (user_id, created_at DESC);

CREATE TABLE submission_attempts (
    id              uuid PRIMARY KEY,
    application_id  uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    channel         text NOT NULL CHECK (channel IN ('ASSISTED', 'EMAIL')),
    actor           text NOT NULL CHECK (actor IN ('USER', 'AGENT', 'SYSTEM')),
    status          text NOT NULL CHECK (status IN ('PENDING', 'SUCCEEDED', 'FAILED', 'CANCELLED')),
    idempotency_key text NOT NULL CHECK (char_length(idempotency_key) BETWEEN 8 AND 100),
    failure_reason  text CHECK (failure_reason IN ('SITE_ERROR', 'POSTING_CLOSED', 'VERIFICATION_REQUIRED', 'INELIGIBLE', 'OTHER')),
    error_message   text CHECK (char_length(error_message) <= 1000),
    started_at      timestamptz NOT NULL DEFAULT now(),
    finished_at     timestamptz,
    UNIQUE (user_id, idempotency_key)
);
-- One attempt in flight, and at most one successful submission, per application.
CREATE UNIQUE INDEX submission_attempts_pending_idx ON submission_attempts (application_id) WHERE status = 'PENDING';
CREATE UNIQUE INDEX submission_attempts_success_idx ON submission_attempts (application_id) WHERE status = 'SUCCEEDED';
CREATE INDEX submission_attempts_user_day_idx ON submission_attempts (user_id, started_at DESC);

CREATE TABLE submission_evidence (
    id             uuid PRIMARY KEY,
    application_id uuid NOT NULL REFERENCES applications(id) ON DELETE CASCADE,
    attempt_id     uuid NOT NULL REFERENCES submission_attempts(id) ON DELETE CASCADE,
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    type           text NOT NULL CHECK (type IN ('USER_ATTESTED', 'EMAIL_SENT', 'EMAIL_CONFIRMATION', 'ATS_CONFIRMATION')),
    reference      text NOT NULL DEFAULT '' CHECK (char_length(reference) <= 500),
    note           text NOT NULL DEFAULT '' CHECK (char_length(note) <= 2000),
    document_id    uuid REFERENCES profile_documents(id) ON DELETE SET NULL,
    occurred_at    timestamptz NOT NULL,
    created_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX submission_evidence_app_idx ON submission_evidence (application_id);

-- History and evidence are append-only records.
-- +goose StatementBegin
CREATE FUNCTION append_only_guard() RETURNS trigger AS $$
BEGIN
    -- ON DELETE SET NULL of an evidence attachment is the only permitted change.
    -- (Nested IF: PL/pgSQL does not short-circuit, and other tables have no document_id.)
    IF TG_TABLE_NAME = 'submission_evidence' THEN
        IF (to_jsonb(NEW) ->> 'document_id') IS NULL AND (to_jsonb(OLD) ->> 'document_id') IS NOT NULL
           AND (to_jsonb(NEW) - 'document_id') = (to_jsonb(OLD) - 'document_id') THEN
            RETURN NEW;
        END IF;
    END IF;
    RAISE EXCEPTION '% is append-only', TG_TABLE_NAME;
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER application_status_history_append_only BEFORE UPDATE ON application_status_history
    FOR EACH ROW EXECUTE FUNCTION append_only_guard();
CREATE TRIGGER submission_evidence_append_only BEFORE UPDATE ON submission_evidence
    FOR EACH ROW EXECUTE FUNCTION append_only_guard();

-- +goose Down
DROP TABLE submission_evidence;
DROP TABLE submission_attempts;
DROP TABLE application_status_history;
DROP FUNCTION append_only_guard();
DROP TABLE applications;
DROP TABLE automation_settings;
