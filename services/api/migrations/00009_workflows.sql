-- +goose Up
-- Phase 8: agent orchestration (ADR-002, ADR-024). Workflows are persisted
-- state machines; River executes one task at a time.

CREATE TABLE workflow_executions (
    id             uuid PRIMARY KEY,
    user_id        uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    template       text NOT NULL CHECK (template IN ('DISCOVER_AND_RANK', 'DISCOVER_PREPARE_APPROVE')),
    -- The user's own words, and how they became a plan.
    objective      text NOT NULL DEFAULT '' CHECK (char_length(objective) <= 2000),
    interpretation text NOT NULL CHECK (interpretation IN ('STRUCTURED', 'AI')),
    params         jsonb NOT NULL,
    plan_version   text NOT NULL,
    status         text NOT NULL CHECK (status IN ('PENDING', 'RUNNING', 'NEEDS_INPUT', 'AWAITING_APPROVAL', 'PAUSED',
                       'SUCCEEDED', 'PARTIALLY_SUCCEEDED', 'FAILED', 'CANCELLED')),
    status_reason  text NOT NULL DEFAULT '' CHECK (char_length(status_reason) <= 1000),
    questions      jsonb NOT NULL DEFAULT '[]'::jsonb,
    report         jsonb,
    created_at     timestamptz NOT NULL DEFAULT now(),
    started_at     timestamptz,
    completed_at   timestamptz,
    updated_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX workflow_executions_user_idx ON workflow_executions (user_id, created_at DESC);
CREATE INDEX workflow_executions_active_idx ON workflow_executions (status)
    WHERE status IN ('PENDING', 'RUNNING', 'NEEDS_INPUT', 'AWAITING_APPROVAL', 'PAUSED');

-- Task contract (master prompt §15): id, user, parent workflow, type, input,
-- expected output, agent, permissions, status, retry policy, timeout,
-- timestamps, result or failure.
CREATE TABLE workflow_tasks (
    id                   uuid PRIMARY KEY,
    workflow_id          uuid NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
    user_id              uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    seq                  integer NOT NULL,
    type                 text NOT NULL,
    agent                text NOT NULL,
    tool                 text NOT NULL,
    input                jsonb NOT NULL,
    expected_output      text NOT NULL,
    required_permissions text[] NOT NULL DEFAULT '{}',
    status               text NOT NULL CHECK (status IN ('PENDING', 'READY', 'RUNNING', 'WAITING_APPROVAL', 'NEEDS_INPUT',
                             'SUCCEEDED', 'FAILED', 'SKIPPED', 'CANCELLED')),
    depends_on           uuid[] NOT NULL DEFAULT '{}',
    -- Runs once its dependencies are finished, whether or not they succeeded (reports).
    run_always           boolean NOT NULL DEFAULT false,
    attempts             integer NOT NULL DEFAULT 0,
    max_attempts         integer NOT NULL CHECK (max_attempts BETWEEN 1 AND 10),
    timeout_seconds      integer NOT NULL CHECK (timeout_seconds BETWEEN 1 AND 3600),
    result               jsonb,
    error_code           text,
    error_message        text CHECK (char_length(error_message) <= 2000),
    created_at           timestamptz NOT NULL DEFAULT now(),
    started_at           timestamptz,
    completed_at         timestamptz,
    updated_at           timestamptz NOT NULL DEFAULT now(),
    UNIQUE (workflow_id, seq)
);
CREATE INDEX workflow_tasks_workflow_idx ON workflow_tasks (workflow_id, seq);

-- Human approval checkpoints. The decision is bound to the payload the user saw.
CREATE TABLE approval_requests (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workflow_id  uuid NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
    task_id      uuid NOT NULL UNIQUE REFERENCES workflow_tasks(id) ON DELETE CASCADE,
    action       text NOT NULL,
    summary      text NOT NULL,
    payload      jsonb NOT NULL,
    payload_hash bytea NOT NULL,
    status       text NOT NULL CHECK (status IN ('PENDING', 'APPROVED', 'REJECTED', 'EXPIRED', 'CANCELLED')),
    decision     jsonb,
    expires_at   timestamptz NOT NULL,
    decided_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX approval_requests_user_idx ON approval_requests (user_id, created_at DESC);
CREATE INDEX approval_requests_pending_idx ON approval_requests (expires_at) WHERE status = 'PENDING';

-- Every tool invocation by an agent, including refusals.
CREATE TABLE agent_tool_calls (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    workflow_id   uuid NOT NULL REFERENCES workflow_executions(id) ON DELETE CASCADE,
    task_id       uuid NOT NULL REFERENCES workflow_tasks(id) ON DELETE CASCADE,
    agent         text NOT NULL,
    tool          text NOT NULL,
    attempt       integer NOT NULL,
    outcome       text NOT NULL CHECK (outcome IN ('SUCCEEDED', 'FAILED', 'DENIED', 'NEEDS_INPUT', 'WAITING_APPROVAL')),
    error_code    text,
    error_message text CHECK (char_length(error_message) <= 2000),
    summary       jsonb NOT NULL DEFAULT '{}'::jsonb,
    duration_ms   integer NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX agent_tool_calls_workflow_idx ON agent_tool_calls (workflow_id, created_at);

-- +goose Down
DROP TABLE agent_tool_calls;
DROP TABLE approval_requests;
DROP TABLE workflow_tasks;
DROP TABLE workflow_executions;
