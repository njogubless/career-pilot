-- +goose Up
-- Operator role for source curation (admin endpoints).
ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'user' CHECK (role IN ('user', 'admin'));

-- Curated public job boards (INTEGRATION_STRATEGY §1). Global, not user-owned.
CREATE TABLE job_sources (
    id                   uuid PRIMARY KEY,
    kind                 text NOT NULL CHECK (kind IN ('GREENHOUSE', 'LEVER', 'ASHBY', 'REMOTEOK')),
    identifier           text NOT NULL CHECK (identifier ~ '^[A-Za-z0-9][A-Za-z0-9._-]{0,99}$'),
    display_name         text NOT NULL,
    enabled              boolean NOT NULL DEFAULT true,
    min_interval_minutes integer NOT NULL DEFAULT 60 CHECK (min_interval_minutes >= 15),
    last_run_at          timestamptz,
    last_success_at      timestamptz,
    last_error           text,
    consecutive_failures integer NOT NULL DEFAULT 0,
    created_at           timestamptz NOT NULL DEFAULT now(),
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (kind, identifier)
);

-- Normalised job postings. Unknown information stays NULL / 'UNKNOWN'.
CREATE TABLE jobs (
    id                 uuid PRIMARY KEY,
    source_id          uuid NOT NULL REFERENCES job_sources(id) ON DELETE CASCADE,
    external_id        text NOT NULL,
    canonical_url      text NOT NULL,
    apply_url          text NOT NULL,
    title              text NOT NULL,
    company_name       text NOT NULL,
    department         text NOT NULL DEFAULT '',
    location_raw       text NOT NULL DEFAULT '',
    location_countries text[] NOT NULL DEFAULT '{}',
    remote_status      text NOT NULL CHECK (remote_status IN ('REMOTE', 'HYBRID', 'ONSITE', 'UNKNOWN')),
    -- For remote roles: where applicants may be located, when stated.
    remote_scope       text NOT NULL DEFAULT 'UNKNOWN' CHECK (remote_scope IN ('WORLDWIDE', 'COUNTRIES', 'REGION', 'UNKNOWN', 'NOT_REMOTE')),
    remote_countries   text[] NOT NULL DEFAULT '{}',
    remote_region      text NOT NULL DEFAULT '',
    employment_type    text NOT NULL DEFAULT 'UNKNOWN',
    salary_min         numeric(14,2),
    salary_max         numeric(14,2),
    salary_currency    text CHECK (salary_currency ~ '^[A-Z]{3}$'),
    salary_period      text CHECK (salary_period IN ('YEAR', 'MONTH', 'HOUR')),
    salary_annual_min  numeric(14,2),
    salary_annual_max  numeric(14,2),
    salary_text        text NOT NULL DEFAULT '',
    description_html   text NOT NULL DEFAULT '',
    description_text   text NOT NULL DEFAULT '',
    tags               text[] NOT NULL DEFAULT '{}',
    posted_at          timestamptz,
    first_seen_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at       timestamptz NOT NULL DEFAULT now(),
    expired_at         timestamptz,
    content_hash       bytea NOT NULL,
    dedupe_key         text NOT NULL,
    duplicate_of       uuid REFERENCES jobs(id) ON DELETE SET NULL,
    repost_of          uuid REFERENCES jobs(id) ON DELETE SET NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    search             tsvector GENERATED ALWAYS AS (
        setweight(to_tsvector('english', coalesce(title, '')), 'A') ||
        setweight(to_tsvector('english', coalesce(company_name, '')), 'B') ||
        setweight(to_tsvector('english', coalesce(left(description_text, 20000), '')), 'C')
    ) STORED,
    UNIQUE (source_id, external_id),
    CHECK (salary_max IS NULL OR salary_min IS NULL OR salary_max >= salary_min)
);
CREATE INDEX jobs_active_posted_idx ON jobs (posted_at DESC NULLS LAST, id) WHERE expired_at IS NULL AND duplicate_of IS NULL;
CREATE INDEX jobs_dedupe_idx ON jobs (dedupe_key);
CREATE INDEX jobs_source_seen_idx ON jobs (source_id, last_seen_at);
CREATE INDEX jobs_search_idx ON jobs USING gin (search);
CREATE INDEX jobs_location_countries_idx ON jobs USING gin (location_countries);
CREATE INDEX jobs_remote_countries_idx ON jobs USING gin (remote_countries);

CREATE TABLE discovery_runs (
    id          uuid PRIMARY KEY,
    source_id   uuid NOT NULL REFERENCES job_sources(id) ON DELETE CASCADE,
    trigger     text NOT NULL CHECK (trigger IN ('SCHEDULED', 'MANUAL')),
    status      text NOT NULL CHECK (status IN ('RUNNING', 'SUCCEEDED', 'FAILED', 'SKIPPED')),
    started_at  timestamptz NOT NULL DEFAULT now(),
    finished_at timestamptz,
    fetched     integer NOT NULL DEFAULT 0,
    created     integer NOT NULL DEFAULT 0,
    updated     integer NOT NULL DEFAULT 0,
    unchanged   integer NOT NULL DEFAULT 0,
    expired     integer NOT NULL DEFAULT 0,
    reposted    integer NOT NULL DEFAULT 0,
    invalid     integer NOT NULL DEFAULT 0,
    error       text
);
CREATE INDEX discovery_runs_source_idx ON discovery_runs (source_id, started_at DESC);
CREATE INDEX discovery_runs_started_idx ON discovery_runs (started_at DESC);

CREATE TABLE saved_searches (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 80),
    filters    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, name)
);

-- +goose Down
DROP TABLE saved_searches;
DROP TABLE discovery_runs;
DROP TABLE jobs;
DROP TABLE job_sources;
ALTER TABLE users DROP COLUMN role;
