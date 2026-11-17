-- +goose Up
-- Phase 4 — matching (ADR-010, ADR-019, ADR-020).
-- Job analyses and embeddings are global (jobs are shared across users) and
-- are recomputed when the job's content hash or the analyser version changes.

CREATE TABLE job_analyses (
    job_id           uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    method           text NOT NULL CHECK (method IN ('DETERMINISTIC', 'LLM')),
    content_hash     bytea NOT NULL,          -- jobs.content_hash that was analysed
    analyzer_version text NOT NULL,
    analysis         jsonb NOT NULL,
    provider         text,
    model            text,
    input_tokens     integer,
    output_tokens    integer,
    created_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (job_id, method)
);
-- Skill-overlap candidate retrieval: analysis -> 'skill_slugs' is a flat array.
CREATE INDEX job_analyses_skill_slugs_idx ON job_analyses USING gin ((analysis -> 'skill_slugs'));

CREATE TABLE job_embeddings (
    job_id       uuid PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    model        text NOT NULL,
    content_hash bytea NOT NULL,
    embedding    vector(384) NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX job_embeddings_hnsw_idx ON job_embeddings USING hnsw (embedding vector_cosine_ops);

-- User-requested LLM ("deep") analyses: progress shown in the UI.
CREATE TABLE job_analysis_requests (
    job_id       uuid PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    status       text NOT NULL CHECK (status IN ('QUEUED', 'RUNNING', 'SUCCEEDED', 'FAILED')),
    error        text,
    requested_by uuid REFERENCES users(id) ON DELETE SET NULL,
    updated_at   timestamptz NOT NULL DEFAULT now()
);

-- Canonical skill link for profile skills, set by the matching pipeline.
-- NULL slug with a version means "checked, not in the catalogue".
ALTER TABLE profile_skills ADD COLUMN skill_slug text;
ALTER TABLE profile_skills ADD COLUMN skill_catalogue_version text;

-- Per-user derived matching inputs, cached by input hash.
CREATE TABLE match_profiles (
    user_id              uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    text_skills_hash     text NOT NULL,
    text_skills          jsonb NOT NULL DEFAULT '[]'::jsonb,
    catalogue_version    text NOT NULL,
    embedding_input_hash text,
    embedding_model      text,
    embedding            vector(384),
    updated_at           timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE job_match_results (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id            uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    algorithm_version text NOT NULL,
    overall_score     numeric(5,2) NOT NULL CHECK (overall_score BETWEEN 0 AND 100),
    eligibility       text NOT NULL CHECK (eligibility IN ('ELIGIBLE', 'INELIGIBLE', 'UNKNOWN')),
    confidence        text NOT NULL CHECK (confidence IN ('HIGH', 'MEDIUM', 'LOW')),
    analysis_method   text NOT NULL CHECK (analysis_method IN ('DETERMINISTIC', 'LLM')),
    result            jsonb NOT NULL,          -- full breakdown, checks, skills, signals, explanation
    weights           jsonb NOT NULL,
    profile_hash      text NOT NULL,
    job_hash          text NOT NULL,
    computed_at       timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, job_id, algorithm_version)
);
CREATE INDEX job_match_results_rank_idx ON job_match_results (user_id, algorithm_version, overall_score DESC);

CREATE TABLE match_feedback (
    id                uuid PRIMARY KEY,
    user_id           uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    job_id            uuid NOT NULL REFERENCES jobs(id) ON DELETE CASCADE,
    rating            text NOT NULL CHECK (rating IN ('UP', 'DOWN')),
    reason            text CHECK (reason IN ('GOOD_FIT', 'SKILLS_MISMATCH', 'WRONG_SENIORITY', 'LOCATION', 'COMPENSATION',
                                             'NOT_INTERESTED', 'SCORE_TOO_HIGH', 'SCORE_TOO_LOW', 'OTHER')),
    note              text NOT NULL DEFAULT '' CHECK (char_length(note) <= 500),
    dismissed         boolean NOT NULL DEFAULT false,
    algorithm_version text,
    score_at_feedback numeric(5,2),
    created_at        timestamptz NOT NULL DEFAULT now(),
    updated_at        timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, job_id)
);

-- One row per user: progress and honesty about when matches were computed.
CREATE TABLE match_states (
    user_id           uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    status            text NOT NULL CHECK (status IN ('QUEUED', 'RUNNING', 'READY', 'FAILED')),
    algorithm_version text,
    candidates        integer NOT NULL DEFAULT 0,
    scored            integer NOT NULL DEFAULT 0,
    profile_hash      text,
    error             text,
    started_at        timestamptz,
    completed_at      timestamptz,
    updated_at        timestamptz NOT NULL DEFAULT now()
);

-- +goose Down
DROP TABLE match_states;
DROP TABLE match_feedback;
DROP TABLE job_match_results;
DROP TABLE match_profiles;
ALTER TABLE profile_skills DROP COLUMN skill_catalogue_version;
ALTER TABLE profile_skills DROP COLUMN skill_slug;
DROP TABLE job_analysis_requests;
DROP TABLE job_embeddings;
DROP TABLE job_analyses;
