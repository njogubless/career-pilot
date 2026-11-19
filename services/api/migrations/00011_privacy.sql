-- +goose Up
-- Phase 10: account deletion (ADR-027).
--
-- Every user-owned table cascades from users. audit_events deliberately has
-- no foreign key (the security record outlives the account), so deletion
-- pseudonymises it instead: the user id stays (it no longer maps to anyone),
-- while the IP address and free-form metadata are removed.
--
-- SECURITY DEFINER lets the least-privilege application role run this one
-- narrow update without holding UPDATE on audit_events (deploy/postgres/roles.sql).

-- +goose StatementBegin
CREATE FUNCTION audit_forget_user(uid uuid) RETURNS bigint
LANGUAGE sql SECURITY DEFINER SET search_path = public AS $$
    WITH forgotten AS (
        UPDATE audit_events SET ip = NULL, metadata = '{}'::jsonb
        WHERE user_id = uid AND (ip IS NOT NULL OR metadata <> '{}'::jsonb)
        RETURNING 1
    )
    SELECT count(*) FROM forgotten
$$;
-- +goose StatementEnd

-- Pending River jobs keep only ids in their arguments; deleting their rows
-- makes those jobs finish as no-ops.

-- Production least-privilege roles (deploy/postgres/roles.sql): keep the
-- audit trail append-only for the application role even when the roles were
-- created before the schema (default privileges would otherwise grant it
-- UPDATE/DELETE). A no-op where the role does not exist (development).
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'careerpilot_app') THEN
        REVOKE UPDATE, DELETE, TRUNCATE ON audit_events FROM careerpilot_app;
    END IF;
END $$;
-- +goose StatementEnd

-- +goose Down
DROP FUNCTION audit_forget_user(uuid);
