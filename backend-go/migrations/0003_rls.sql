-- 0003_rls.sql
--
-- Third and final migration in the lexically-ordered sequence
-- (0001_config.sql -> 0002_runtime.sql -> 0003_rls.sql), applied in that
-- order by executing each file's raw SQL against Postgres (see
-- backend-go/tests/integration/rls_isolation_test.go).
--
-- Two independent deliverables:
--   1. Three indexes flagged by 0002_runtime.sql's review as missing on the
--      session-resume/audit hot path (checkpoints.session_id,
--      snapshots.session_id, idempotency_claims.session_id).
--   2. Row-Level Security policies for EVERY tenant-scoped table defined in
--      0001_config.sql and 0002_runtime.sql -- "tenant-scoped" meaning any
--      table with a tenant_id column, whether NOT NULL or nullable.
--
-- Governance (binding, copied from the pinned bundle):
--   * Tenant Is the First Dimension: isolation MUST be enforced at the DB
--     (RLS), never application ACLs alone. Isolation MUST survive
--     connection pooling -- tenant scoping is TRANSACTION-LOCAL (set via
--     `SELECT set_config('app.tenant_id', $1, true)`, i.e. SET LOCAL, never
--     plain SET -- see rls_isolation_test.go's "session-level SET is not
--     tenant-safe under pooled connections" subtest for why).
--   * FORCE ROW LEVEL SECURITY on every tenant-scoped table below -- FORCE
--     is mandatory so even the table owner (the role migrations run as, and
--     the role the application connects as) is subject to the policy; RLS
--     alone only restricts non-owner roles, which is not sufficient here.
--   * NULL context = DENY ALL, never allow-all: every policy reads the
--     tenant context via `current_setting('app.tenant_id', true)` with the
--     missing_ok=true argument. When no context is set, current_setting(...)
--     returns SQL NULL, and `tenant_id = NULL` is never TRUE in SQL
--     three-valued logic -- so an unset context denies every row in a
--     NOT-NULL-tenant table. This is why the pattern is never
--     `COALESCE(current_setting(...), 'some-default')`: a COALESCE default
--     would make an unset context resolve to "allow everything under that
--     default," which is the opposite of fail-closed.
--   * security-and-owasp.instructions.md A05/GO3: no string-built SQL. This
--     file is pure DDL with no runtime string interpolation; the
--     current_setting(...) calls below are static literals, not
--     concatenated from external input.
--
-- Migrations are forward-only and additive; there is no rollback/down file
-- for this project.

-- =============================================================================
-- Missing indexes (Task 2 review, deferred here)
--
-- checkpoints.session_id, snapshots.session_id, and idempotency_claims.
-- session_id are unindexed FK columns on the session-resume/audit hot path.
-- Added here, ahead of the RLS policies, so they land in the same
-- migration that closes out the review item.
-- =============================================================================

CREATE INDEX IF NOT EXISTS idx_checkpoints_session_id ON checkpoints (session_id);
CREATE INDEX IF NOT EXISTS idx_snapshots_session_id ON snapshots (session_id);
CREATE INDEX IF NOT EXISTS idx_idempotency_claims_session_id ON idempotency_claims (session_id);

-- =============================================================================
-- Row-Level Security policies
--
-- Table inventory (19 CREATE TABLE statements across 0001_config.sql and
-- 0002_runtime.sql):
--
--   0001_config.sql:
--     tenants              -- EXCLUDED: root of the isolation model itself;
--                             there is no "tenant of a tenant".
--     users                -- tenant_id NOT NULL -> standard policy.
--     agents               -- tenant_id NOT NULL -> standard policy.
--     tools                -- tenant_id NULL (global built-ins) -> NULL-aware policy.
--     models               -- EXCLUDED: genuinely global platform catalog, no tenant_id column.
--     price_books          -- EXCLUDED: genuinely global platform pricing, no tenant_id column.
--     meters               -- EXCLUDED: genuinely global platform catalog, no tenant_id column.
--     fx_rates             -- EXCLUDED: genuinely global platform data, no tenant_id column.
--     skills               -- tenant_id NOT NULL -> standard policy.
--     connectors           -- tenant_id NOT NULL -> standard policy.
--
--   0002_runtime.sql:
--     sessions               -- tenant_id NOT NULL -> standard policy.
--     events                 -- tenant_id NOT NULL -> standard policy.
--     delegations            -- tenant_id NOT NULL -> standard policy.
--     effect_classes         -- tenant_id NULL (platform seeds) -> NULL-aware policy.
--     orchestration_plans    -- tenant_id NOT NULL -> standard policy.
--     checkpoints            -- tenant_id NOT NULL -> standard policy.
--     snapshots              -- tenant_id NOT NULL -> standard policy.
--     idempotency_claims     -- tenant_id NOT NULL -> standard policy.
--     content_access_grants  -- tenant_id NOT NULL -> standard policy.
--
-- 14 tables get a policy below (12 standard + 2 NULL-aware); 5 tables are
-- deliberately excluded (tenants, models, price_books, meters, fx_rates) --
-- none of them has a tenant_id column, i.e. none is tenant-scoped at all.
-- =============================================================================

-- -----------------------------------------------------------------------------
-- Standard policy (tenant_id uuid NOT NULL)
--
-- Applies to: users, agents, skills, connectors, sessions, events,
-- delegations, orchestration_plans, checkpoints, snapshots,
-- idempotency_claims, content_access_grants.
--
-- FORCE ROW LEVEL SECURITY subjects the table owner to the policy too.
-- `USING` (no separate WITH CHECK) applies the same predicate to both reads
-- and writes, so a row can neither be read across tenants nor written under
-- someone else's tenant_id.
-- -----------------------------------------------------------------------------

ALTER TABLE users ENABLE ROW LEVEL SECURITY;
ALTER TABLE users FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON users
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE agents ENABLE ROW LEVEL SECURITY;
ALTER TABLE agents FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON agents
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE skills ENABLE ROW LEVEL SECURITY;
ALTER TABLE skills FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON skills
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE connectors ENABLE ROW LEVEL SECURITY;
ALTER TABLE connectors FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON connectors
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE sessions ENABLE ROW LEVEL SECURITY;
ALTER TABLE sessions FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON sessions
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE events ENABLE ROW LEVEL SECURITY;
ALTER TABLE events FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON events
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE delegations ENABLE ROW LEVEL SECURITY;
ALTER TABLE delegations FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON delegations
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE orchestration_plans ENABLE ROW LEVEL SECURITY;
ALTER TABLE orchestration_plans FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON orchestration_plans
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE checkpoints ENABLE ROW LEVEL SECURITY;
ALTER TABLE checkpoints FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON checkpoints
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE snapshots ENABLE ROW LEVEL SECURITY;
ALTER TABLE snapshots FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON snapshots
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE idempotency_claims ENABLE ROW LEVEL SECURITY;
ALTER TABLE idempotency_claims FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON idempotency_claims
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE content_access_grants ENABLE ROW LEVEL SECURITY;
ALTER TABLE content_access_grants FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON content_access_grants
  USING (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- -----------------------------------------------------------------------------
-- NULL-aware policy (tenant_id uuid NULL)
--
-- Applies to: tools, effect_classes.
--
-- Both tables mix tenant-owned rows with platform-seeded/global rows that
-- carry tenant_id IS NULL by design (global built-in tools; platform effect
-- classes such as 'platform/payment'). Those NULL-tenant rows must remain
-- visible to every tenant regardless of context, while a tenant's own rows
-- must still be denied to every OTHER tenant. FORCE ROW LEVEL SECURITY still
-- applies; the "NULL context = deny all" rule is unaffected for the
-- tenant-owned half of the table -- when no context is set,
-- current_setting(...) is NULL, so `tenant_id = NULL` is never TRUE and only
-- the `tenant_id IS NULL` disjunct can match, i.e. only global rows are
-- visible, never another tenant's rows.
-- -----------------------------------------------------------------------------

ALTER TABLE tools ENABLE ROW LEVEL SECURITY;
ALTER TABLE tools FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON tools
  USING (tenant_id IS NULL OR tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER TABLE effect_classes ENABLE ROW LEVEL SECURITY;
ALTER TABLE effect_classes FORCE ROW LEVEL SECURITY;
CREATE POLICY tenant_isolation ON effect_classes
  USING (tenant_id IS NULL OR tenant_id = current_setting('app.tenant_id', true)::uuid);

-- -----------------------------------------------------------------------------
-- Deliberately excluded (no tenant_id column -- not tenant-scoped):
--   tenants       -- the root of the isolation model; there is no
--                    "tenant of a tenant" to scope it by.
--   models        -- global platform model catalog, shared by all tenants.
--   price_books   -- global platform pricing catalog.
--   meters        -- global platform metering catalog.
--   fx_rates      -- global platform FX rate table.
-- -----------------------------------------------------------------------------
