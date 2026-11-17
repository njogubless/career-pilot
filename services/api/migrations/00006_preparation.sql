-- +goose Up
-- Phase 5: application preparation (ADR-021).
-- One tailored document per (user, job, kind); every change is a new,
-- immutable version. Approval pins a specific version.

CREATE TABLE application_documents (
    id                  uuid PRIMARY KEY,
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id              uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    kind                text NOT NULL CHECK (kind IN ('CV', 'COVER_LETTER', 'APPLICATION_ANSWERS')),
    -- Inputs the user gives the generator: free-text notes (treated as
    -- user-asserted facts for this document) and, for answers, the questions.
    notes               text NOT NULL DEFAULT '' CHECK (char_length(notes) <= 4000),
    questions           jsonb NOT NULL DEFAULT '[]'::jsonb,
    current_version_id  uuid,
    approved_version_id uuid,
    approved_at         timestamptz,
    created_at          timestamptz NOT NULL DEFAULT now(),
    updated_at          timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, job_id, kind)
);
CREATE INDEX application_documents_user_idx ON application_documents (user_id, updated_at DESC);

CREATE TABLE application_document_versions (
    id                uuid PRIMARY KEY,
    document_id       uuid NOT NULL REFERENCES application_documents(id) ON DELETE CASCADE,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    version_no        integer NOT NULL CHECK (version_no > 0),
    origin            text NOT NULL CHECK (origin IN ('GENERATED', 'TEMPLATE', 'USER_EDIT', 'RESTORED')),
    restored_from     uuid REFERENCES application_document_versions(id) ON DELETE SET NULL,
    content           jsonb NOT NULL,
    content_hash      bytea NOT NULL,
    -- Hash of the fact sheet the version was built or checked against.
    facts_hash        bytea NOT NULL,
    validation        jsonb,
    validation_status text NOT NULL CHECK (validation_status IN ('PASSED', 'WARNINGS', 'FAILED', 'NOT_VALIDATED')),
    questions         jsonb NOT NULL DEFAULT '[]'::jsonb,
    warnings          jsonb NOT NULL DEFAULT '[]'::jsonb,
    provider          text,
    model             text,
    prompt_version    text,
    input_tokens      integer,
    output_tokens     integer,
    created_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (document_id, version_no)
);
CREATE INDEX application_document_versions_doc_idx ON application_document_versions (document_id, version_no DESC);

ALTER TABLE application_documents
    ADD CONSTRAINT application_documents_current_fk FOREIGN KEY (current_version_id)
        REFERENCES application_document_versions(id) DEFERRABLE INITIALLY DEFERRED,
    ADD CONSTRAINT application_documents_approved_fk FOREIGN KEY (approved_version_id)
        REFERENCES application_document_versions(id) DEFERRABLE INITIALLY DEFERRED;

-- Versions are history: they may be deleted with their document, never edited.
-- +goose StatementBegin
CREATE FUNCTION application_document_versions_immutable() RETURNS trigger AS $$
BEGIN
    -- ON DELETE SET NULL of restored_from is the only permitted change.
    IF NEW.restored_from IS NULL AND OLD.restored_from IS NOT NULL
       AND (to_jsonb(NEW) - 'restored_from') = (to_jsonb(OLD) - 'restored_from') THEN
        RETURN NEW;
    END IF;
    RAISE EXCEPTION 'application document versions are immutable';
END;
$$ LANGUAGE plpgsql;
-- +goose StatementEnd
CREATE TRIGGER application_document_versions_no_update
    BEFORE UPDATE ON application_document_versions
    FOR EACH ROW EXECUTE FUNCTION application_document_versions_immutable();

CREATE TABLE application_document_approvals (
    id                  uuid PRIMARY KEY,
    document_id         uuid NOT NULL REFERENCES application_documents(id) ON DELETE CASCADE,
    version_id          uuid NOT NULL REFERENCES application_document_versions(id) ON DELETE CASCADE,
    user_id             uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Fresh validation at approval time, against the then-current profile.
    validation          jsonb NOT NULL,
    acknowledged_issues jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at          timestamptz NOT NULL DEFAULT now(),
    revoked_at          timestamptz
);
CREATE INDEX application_document_approvals_doc_idx ON application_document_approvals (document_id, created_at DESC);

CREATE TABLE application_document_generations (
    id            uuid PRIMARY KEY,
    document_id   uuid NOT NULL REFERENCES application_documents(id) ON DELETE CASCADE,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    status        text NOT NULL CHECK (status IN ('QUEUED', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    version_id    uuid REFERENCES application_document_versions(id) ON DELETE SET NULL,
    error_code    text,
    error_message text,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);
-- At most one generation in flight per document.
CREATE UNIQUE INDEX application_document_generations_active_idx
    ON application_document_generations (document_id) WHERE status IN ('QUEUED', 'RUNNING');

-- +goose Down
DROP TABLE application_document_generations;
DROP TABLE application_document_approvals;
ALTER TABLE application_documents DROP CONSTRAINT application_documents_current_fk,
    DROP CONSTRAINT application_documents_approved_fk;
DROP TABLE application_document_versions;
DROP FUNCTION application_document_versions_immutable();
DROP TABLE application_documents;
