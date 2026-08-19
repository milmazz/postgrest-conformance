-- Roles required by the conformance fixtures and cases. Idempotent: safe to
-- run against a shared cluster where the roles may already exist. Run this
-- file against the `postgres` maintenance database, before creating the
-- target database (see fixtures/README.md).
DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgrest_test_anonymous') THEN CREATE ROLE postgrest_test_anonymous; END IF; END $$;
DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgrest_test_default_role') THEN CREATE ROLE postgrest_test_default_role; END IF; END $$;
DO $$ BEGIN IF NOT EXISTS (SELECT FROM pg_roles WHERE rolname = 'postgrest_test_author') THEN CREATE ROLE postgrest_test_author; END IF; END $$;
