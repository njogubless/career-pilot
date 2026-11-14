-- +goose Up
-- Professional facts carry provenance (docs/DATABASE_DESIGN.md §1):
--   source:       USER_ENTERED | CV_IMPORT | AI_INFERRED
--   verification: VERIFIED | UNVERIFIED | REJECTED
-- Only an explicit user action sets VERIFIED on imported or inferred facts.

CREATE TABLE professional_profiles (
    user_id          uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    headline         text NOT NULL DEFAULT '' CHECK (char_length(headline) <= 200),
    summary          text NOT NULL DEFAULT '' CHECK (char_length(summary) <= 5000),
    location_city    text NOT NULL DEFAULT '' CHECK (char_length(location_city) <= 120),
    location_country text CHECK (location_country ~ '^[A-Z]{2}$'),
    years_experience numeric(4,1) CHECK (years_experience >= 0 AND years_experience <= 80),
    links            jsonb NOT NULL DEFAULT '[]'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now(),
    updated_at       timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE work_experiences (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    employer     text NOT NULL CHECK (char_length(employer) BETWEEN 1 AND 200),
    title        text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    location     text NOT NULL DEFAULT '',
    start_date   date,
    end_date     date,
    is_current   boolean NOT NULL DEFAULT false,
    description  text NOT NULL DEFAULT '' CHECK (char_length(description) <= 5000),
    achievements jsonb NOT NULL DEFAULT '[]'::jsonb,
    source       text NOT NULL CHECK (source IN ('USER_ENTERED', 'CV_IMPORT', 'AI_INFERRED')),
    verification text NOT NULL CHECK (verification IN ('VERIFIED', 'UNVERIFIED', 'REJECTED')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (end_date IS NULL OR start_date IS NULL OR end_date >= start_date)
);
CREATE INDEX work_experiences_user_idx ON work_experiences (user_id, start_date DESC NULLS LAST);

CREATE TABLE education_records (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    institution  text NOT NULL CHECK (char_length(institution) BETWEEN 1 AND 200),
    degree       text NOT NULL DEFAULT '',
    field        text NOT NULL DEFAULT '',
    start_date   date,
    end_date     date,
    source       text NOT NULL CHECK (source IN ('USER_ENTERED', 'CV_IMPORT', 'AI_INFERRED')),
    verification text NOT NULL CHECK (verification IN ('VERIFIED', 'UNVERIFIED', 'REJECTED')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX education_records_user_idx ON education_records (user_id);

-- Skills are user-scoped free text with a normalised key for de-duplication.
-- A canonical skill catalogue (taxonomy) is linked in Phase 4.
CREATE TABLE profile_skills (
    id              uuid PRIMARY KEY,
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name            text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 100),
    normalized_name text NOT NULL,
    category        text NOT NULL DEFAULT '',
    level           text CHECK (level IN ('BEGINNER', 'INTERMEDIATE', 'ADVANCED', 'EXPERT')),
    years           numeric(4,1) CHECK (years >= 0 AND years <= 60),
    source          text NOT NULL CHECK (source IN ('USER_ENTERED', 'CV_IMPORT', 'AI_INFERRED')),
    verification    text NOT NULL CHECK (verification IN ('VERIFIED', 'UNVERIFIED', 'REJECTED')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, normalized_name)
);

CREATE TABLE certifications (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    issuer       text NOT NULL DEFAULT '',
    issued_date  date,
    expires_date date,
    credential_url text NOT NULL DEFAULT '',
    source       text NOT NULL CHECK (source IN ('USER_ENTERED', 'CV_IMPORT', 'AI_INFERRED')),
    verification text NOT NULL CHECK (verification IN ('VERIFIED', 'UNVERIFIED', 'REJECTED')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX certifications_user_idx ON certifications (user_id);

CREATE TABLE projects (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 200),
    description  text NOT NULL DEFAULT '' CHECK (char_length(description) <= 5000),
    url          text NOT NULL DEFAULT '',
    source       text NOT NULL CHECK (source IN ('USER_ENTERED', 'CV_IMPORT', 'AI_INFERRED')),
    verification text NOT NULL CHECK (verification IN ('VERIFIED', 'UNVERIFIED', 'REJECTED')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX projects_user_idx ON projects (user_id);

CREATE TABLE user_preferences (
    user_id             uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    target_roles        text[] NOT NULL DEFAULT '{}',
    industries          text[] NOT NULL DEFAULT '{}',
    employment_types    text[] NOT NULL DEFAULT '{}',
    preferred_locations text[] NOT NULL DEFAULT '{}',
    remote_mode         text NOT NULL DEFAULT 'REMOTE_ONLY' CHECK (remote_mode IN ('REMOTE_ONLY', 'HYBRID_OK', 'ONSITE_OK')),
    work_eligible_countries text[] NOT NULL DEFAULT '{}',
    min_salary          numeric(12,2) CHECK (min_salary >= 0),
    salary_currency     text CHECK (salary_currency ~ '^[A-Z]{3}$'),
    salary_period       text CHECK (salary_period IN ('YEAR', 'MONTH', 'HOUR')),
    needs_sponsorship   boolean,
    willing_to_relocate boolean,
    notice_period_days  integer CHECK (notice_period_days BETWEEN 0 AND 365),
    seniority_levels    text[] NOT NULL DEFAULT '{}',
    updated_at          timestamptz NOT NULL DEFAULT now(),
    CHECK ((min_salary IS NULL) = (salary_currency IS NULL) AND (min_salary IS NULL) = (salary_period IS NULL))
);

CREATE TABLE profile_documents (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind              text NOT NULL CHECK (kind IN ('CV', 'COVER_LETTER', 'EVIDENCE', 'GENERATED_CV')),
    original_filename text NOT NULL CHECK (char_length(original_filename) BETWEEN 1 AND 255),
    mime_type         text NOT NULL,
    size_bytes        bigint NOT NULL CHECK (size_bytes > 0),
    sha256            bytea NOT NULL,
    storage_key       text NOT NULL UNIQUE,
    created_at        timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX profile_documents_user_idx ON profile_documents (user_id, created_at DESC);

-- A CV extraction produces a *proposal*; nothing is written to the profile
-- until the user reviews and applies it.
CREATE TABLE profile_extractions (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    document_id    uuid NOT NULL REFERENCES profile_documents(id) ON DELETE CASCADE,
    status         text NOT NULL CHECK (status IN ('QUEUED', 'RUNNING', 'PENDING_REVIEW', 'APPLIED', 'DISCARDED', 'FAILED')),
    proposal       jsonb,
    error_code     text,
    error_message  text,
    provider       text,
    model          text,
    prompt_version text,
    input_tokens   integer,
    output_tokens  integer,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    applied_at     timestamptz
);
CREATE INDEX profile_extractions_user_idx ON profile_extractions (user_id, created_at DESC);

-- +goose Down
DROP TABLE profile_extractions;
DROP TABLE profile_documents;
DROP TABLE user_preferences;
DROP TABLE projects;
DROP TABLE certifications;
DROP TABLE profile_skills;
DROP TABLE education_records;
DROP TABLE work_experiences;
DROP TABLE professional_profiles;
