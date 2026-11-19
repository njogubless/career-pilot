-- Least-privilege database roles for production (ADR-026, KI-007).
--
-- Run once as a superuser (or the managed-database admin) on the CareerPilot
-- database, replacing the passwords, BEFORE the first `careerpilot migrate up`:
--
--   psql "$ADMIN_URL" -v owner_password="'...'" -v app_password="'...'" -f deploy/postgres/roles.sql
--
-- careerpilot_owner  owns the schema; used ONLY by the migrate release step.
-- careerpilot_app    used by the API and worker: reads and writes data, but
--                    cannot change the schema, and can only append to
--                    audit_events (no UPDATE/DELETE; account deletion uses the
--                    SECURITY DEFINER function audit_forget_user).
--
-- Then point DATABASE_URL for migrate at careerpilot_owner, and for api and
-- worker at careerpilot_app (MIGRATE_ON_START=false).
--
-- Postgres row-level security is NOT enabled (ADR-026): user isolation is
-- enforced and tested in the application. These grants limit the damage of a
-- compromised application credential (no schema changes, no audit erasure).

\set ON_ERROR_STOP on

SELECT format('CREATE ROLE careerpilot_owner LOGIN PASSWORD %s', :'owner_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'careerpilot_owner') \gexec
SELECT format('CREATE ROLE careerpilot_app LOGIN PASSWORD %s', :'app_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'careerpilot_app') \gexec

-- The schema belongs to the owner; nobody else may create objects in it.
ALTER SCHEMA public OWNER TO careerpilot_owner;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT USAGE ON SCHEMA public TO careerpilot_app;
SELECT format('GRANT CONNECT, TEMPORARY ON DATABASE %I TO careerpilot_app', current_database()) \gexec
SELECT format('GRANT CREATE ON DATABASE %I TO careerpilot_owner', current_database()) \gexec

-- Existing objects (when applied to a database that already has the schema,
-- e.g. after a restore): hand them to the owner, then grant the app access.
DO $$
DECLARE r record;
BEGIN
  FOR r IN SELECT tablename FROM pg_tables WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER TABLE public.%I OWNER TO careerpilot_owner', r.tablename);
  END LOOP;
  FOR r IN SELECT sequencename FROM pg_sequences WHERE schemaname = 'public' LOOP
    EXECUTE format('ALTER SEQUENCE public.%I OWNER TO careerpilot_owner', r.sequencename);
  END LOOP;
  FOR r IN SELECT p.oid::regprocedure AS fn FROM pg_proc p JOIN pg_namespace n ON n.oid = p.pronamespace
           LEFT JOIN pg_depend d ON d.objid = p.oid AND d.deptype = 'e'
           WHERE n.nspname = 'public' AND d.objid IS NULL LOOP
    EXECUTE format('ALTER FUNCTION %s OWNER TO careerpilot_owner', r.fn);
  END LOOP;
END $$;

GRANT SELECT, INSERT, UPDATE, DELETE ON ALL TABLES IN SCHEMA public TO careerpilot_app;
GRANT USAGE, SELECT ON ALL SEQUENCES IN SCHEMA public TO careerpilot_app;
GRANT EXECUTE ON ALL FUNCTIONS IN SCHEMA public TO careerpilot_app;

-- Future objects created by migrations get the same grants automatically.
ALTER DEFAULT PRIVILEGES FOR ROLE careerpilot_owner IN SCHEMA public
  GRANT SELECT, INSERT, UPDATE, DELETE ON TABLES TO careerpilot_app;
ALTER DEFAULT PRIVILEGES FOR ROLE careerpilot_owner IN SCHEMA public
  GRANT USAGE, SELECT ON SEQUENCES TO careerpilot_app;
ALTER DEFAULT PRIVILEGES FOR ROLE careerpilot_owner IN SCHEMA public
  GRANT EXECUTE ON FUNCTIONS TO careerpilot_app;

-- The audit trail is append-only for the application. Re-run this file after
-- migrations if audit_events is ever recreated.
DO $$
BEGIN
  IF to_regclass('public.audit_events') IS NOT NULL THEN
    REVOKE UPDATE, DELETE, TRUNCATE ON public.audit_events FROM careerpilot_app;
  END IF;
END $$;
