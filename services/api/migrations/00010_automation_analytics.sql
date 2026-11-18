-- +goose Up
-- Phase 9: scheduled objectives, notifications, career reports (ADR-025).

CREATE TABLE workflow_schedules (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (char_length(name) BETWEEN 1 AND 120),
    objective     text NOT NULL DEFAULT '' CHECK (char_length(objective) <= 2000),
    template      text NOT NULL CHECK (template IN ('DISCOVER_AND_RANK', 'DISCOVER_PREPARE_APPROVE')),
    params        jsonb NOT NULL,
    cadence       text NOT NULL CHECK (cadence IN ('DAILY', 'WEEKLY')),
    weekday       integer CHECK (weekday BETWEEN 0 AND 6), -- 0 = Sunday; WEEKLY only
    hour          integer NOT NULL CHECK (hour BETWEEN 0 AND 23),
    timezone      text NOT NULL,
    enabled       boolean NOT NULL DEFAULT true,
    next_run_at   timestamptz NOT NULL,
    last_run_at   timestamptz,
    last_outcome  text CHECK (last_outcome IN ('STARTED', 'SKIPPED_PAUSED', 'SKIPPED_OVERLAP', 'SKIPPED_LIMIT', 'FAILED')),
    last_message  text CHECK (char_length(last_message) <= 500),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    CHECK ((cadence = 'WEEKLY') = (weekday IS NOT NULL))
);
CREATE INDEX workflow_schedules_due_idx ON workflow_schedules (next_run_at) WHERE enabled;

-- A scheduled run is tied to its schedule slot: firing twice for the same
-- slot cannot create two workflows.
ALTER TABLE workflow_executions
    ADD COLUMN schedule_id uuid REFERENCES workflow_schedules(id) ON DELETE SET NULL,
    ADD COLUMN scheduled_for timestamptz;
CREATE UNIQUE INDEX workflow_executions_schedule_slot_idx ON workflow_executions (schedule_id, scheduled_for)
    WHERE schedule_id IS NOT NULL;

CREATE TABLE notification_preferences (
    user_id         uuid PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    high_match      boolean NOT NULL DEFAULT true,
    min_match_score integer NOT NULL DEFAULT 80 CHECK (min_match_score BETWEEN 50 AND 100),
    approvals       boolean NOT NULL DEFAULT true,
    workflow_events boolean NOT NULL DEFAULT true,
    replies         boolean NOT NULL DEFAULT true,
    follow_ups      boolean NOT NULL DEFAULT true,
    weekly_report   boolean NOT NULL DEFAULT true,
    updated_at      timestamptz NOT NULL DEFAULT now()
);

-- In-app notifications, derived from persisted records. (user, dedupe_key)
-- is unique, so the same event never notifies twice.
CREATE TABLE notifications (
    id         uuid PRIMARY KEY,
    user_id    uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('HIGH_MATCH', 'APPROVAL_REQUESTED', 'WORKFLOW_NEEDS_INPUT', 'WORKFLOW_FINISHED',
                   'REPLY_RECEIVED', 'MESSAGE_BOUNCED', 'FOLLOW_UP_READY', 'REPORT_READY', 'SCHEDULE_SKIPPED')),
    dedupe_key text NOT NULL CHECK (char_length(dedupe_key) <= 200),
    title      text NOT NULL CHECK (char_length(title) <= 200),
    body       text NOT NULL DEFAULT '' CHECK (char_length(body) <= 1000),
    link       text NOT NULL DEFAULT '' CHECK (char_length(link) <= 300),
    read_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (user_id, dedupe_key)
);
CREATE INDEX notifications_user_idx ON notifications (user_id, created_at DESC);
CREATE INDEX notifications_unread_idx ON notifications (user_id) WHERE read_at IS NULL;

CREATE TABLE career_reports (
    id           uuid PRIMARY KEY,
    user_id      uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    period_start date NOT NULL,
    period_end   date NOT NULL,
    trigger      text NOT NULL CHECK (trigger IN ('SCHEDULED', 'MANUAL')),
    data         jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CHECK (period_end >= period_start)
);
CREATE UNIQUE INDEX career_reports_scheduled_idx ON career_reports (user_id, period_start, period_end) WHERE trigger = 'SCHEDULED';
CREATE INDEX career_reports_user_idx ON career_reports (user_id, created_at DESC);

-- +goose Down
DROP TABLE career_reports;
DROP TABLE notifications;
DROP TABLE notification_preferences;
DROP INDEX workflow_executions_schedule_slot_idx;
ALTER TABLE workflow_executions DROP COLUMN scheduled_for, DROP COLUMN schedule_id;
DROP TABLE workflow_schedules;
