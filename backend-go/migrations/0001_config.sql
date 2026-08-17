-- 0001_config.sql
--
-- First migration in the lexically-ordered sequence
-- (0001_config.sql -> 0002_runtime.sql -> 0003_rls.sql), applied in that
-- order by executing each file's raw SQL against Postgres (see
-- backend-go/tests/integration/rls_isolation_test.go).
--
-- Defines the platform's IMMUTABLE config entities: tenants, users, agents,
-- tools, models, price_books, meters, fx_rates, skills, connectors. These
-- rows are written once per version and never updated in place -- a config
-- change is a new versioned row, never an UPDATE to an existing one (except
-- where a field is explicitly a mutable status/enum, e.g. skills.status).
--
-- Tables appear in FK-dependency order: tenants first, then every table
-- that references tenants(tenant_id).
--
-- No RLS policies here -- row-level security is enabled in 0003_rls.sql
-- (T012) so migrations can be applied incrementally without locking
-- ourselves out mid-sequence. This file only establishes tenant_id columns
-- with the correct nullability per table.
--
-- Migrations are forward-only and additive; there is no rollback/down file
-- for this project.

-- =============================================================================
-- tenants
-- =============================================================================

CREATE TABLE IF NOT EXISTS tenants (
    tenant_id        uuid PRIMARY KEY,
    name             text NOT NULL,
    region           text NOT NULL,
    retention_days   int NOT NULL DEFAULT 90 CHECK (retention_days > 0),
    deployment_tier  text NOT NULL CHECK (deployment_tier IN ('saas', 'single_tenant', 'byoc', 'hybrid')),
    identity_config  jsonb NOT NULL DEFAULT '{}'::jsonb,
    rbac_map         jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at       timestamptz NOT NULL DEFAULT now()
);

-- =============================================================================
-- users
-- =============================================================================

CREATE TABLE IF NOT EXISTS users (
    user_id           uuid PRIMARY KEY,
    tenant_id         uuid NOT NULL REFERENCES tenants (tenant_id),
    external_subject  text NOT NULL,
    roles             text[] NOT NULL DEFAULT '{}',
    created_at        timestamptz NOT NULL DEFAULT now()
);

-- =============================================================================
-- agents
--
-- Note: data-model.md's Agent also has tool_profile_id/tool_profile_version --
-- intentionally omitted here (Tool Profile is a later, out-of-scope task); no
-- tool_profiles table is created in this migration either.
-- =============================================================================

CREATE TABLE IF NOT EXISTS agents (
    agent_id        uuid NOT NULL,
    tenant_id       uuid NOT NULL REFERENCES tenants (tenant_id),
    version         int NOT NULL,
    bootstrap       text NOT NULL,
    autonomy_level  text NOT NULL CHECK (autonomy_level IN ('read_only', 'supervised', 'full')),
    prompt_mode     text NOT NULL DEFAULT 'full' CHECK (prompt_mode IN ('full', 'task', 'minimal', 'none')),
    created_at      timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (agent_id, version)
);

-- =============================================================================
-- tools
--
-- tenant_id is NULLABLE here -- NULL denotes a global built-in tool shared
-- across all tenants; a non-NULL value scopes the tool to a single tenant.
--
-- returns_untrusted / reads_private_data / mutates_external all default to
-- TRUE (fail-closed): a newly-registered tool is treated as fully tainted on
-- every leg until explicitly proven otherwise. concurrency_safe defaults to
-- 'exclusive', the most conservative execution mode, for the same reason.
-- =============================================================================

CREATE TABLE IF NOT EXISTS tools (
    tool_id               text PRIMARY KEY,
    namespace             text NOT NULL,
    name                  text NOT NULL,
    tool_version          text NOT NULL,
    source_ref            text NOT NULL,
    descriptor_digest     bytea NOT NULL,
    tenant_id             uuid NULL REFERENCES tenants (tenant_id),
    description           text NOT NULL DEFAULT '',
    input_schema          jsonb NOT NULL DEFAULT '{}'::jsonb,
    output_schema         jsonb NULL,
    disclosure            text NOT NULL DEFAULT 'resident' CHECK (disclosure IN ('resident', 'deferred')),
    capability            text NOT NULL DEFAULT 'read_only' CHECK (capability IN ('read_only', 'mutating')),
    concurrency_safe      text NOT NULL DEFAULT 'exclusive' CHECK (concurrency_safe IN ('read_only', 'concurrency_safe', 'exclusive')),
    returns_untrusted     boolean NOT NULL DEFAULT true,
    reads_private_data    boolean NOT NULL DEFAULT true,
    mutates_external      boolean NOT NULL DEFAULT true,
    effect_class_id       text NULL,
    idempotency_key_spec  jsonb NULL,
    connector_id          uuid NULL,
    catalog_scan_status   text NOT NULL DEFAULT 'pending' CHECK (catalog_scan_status IN ('pending', 'clean', 'flagged', 'rejected')),
    scan_policy_version   text NOT NULL DEFAULT 'v1',
    scanned_at            timestamptz NOT NULL DEFAULT now(),
    UNIQUE (tenant_id, namespace, name, tool_version)
);

-- =============================================================================
-- models
-- =============================================================================

CREATE TABLE IF NOT EXISTS models (
    model_id             text PRIMARY KEY,
    provider             text NOT NULL CHECK (provider IN ('anthropic', 'openai_compatible', 'bedrock', 'vertex', 'cli', 'self_hosted')),
    pinned_snapshot       text NOT NULL,
    eval_run_id           uuid NULL,
    capability_floor      int NOT NULL DEFAULT 0,
    data_labels_allowed   text[] NOT NULL DEFAULT '{}',
    regions_allowed       text[] NOT NULL DEFAULT '{}',
    adapter_id            text NULL
);

-- =============================================================================
-- price_books (T010b)
--
-- Authored directly in the generalized shape: keyed on
-- (price_book_version, meter_id, subject_id, modifier) rather than a
-- token-only predecessor.
-- =============================================================================

CREATE TABLE IF NOT EXISTS price_books (
    price_book_version  text NOT NULL,
    meter_id             text NOT NULL,
    subject_id            text NOT NULL,
    modifier              text NOT NULL DEFAULT 'none',
    tier_lower            numeric NULL,
    tier_upper            numeric NULL,
    tier_basis            text NOT NULL DEFAULT 'per_request' CHECK (tier_basis IN ('per_request', 'per_period')),
    currency              char(3) NOT NULL,
    rate                  numeric(20, 10) NOT NULL,
    list_rate             numeric(20, 10) NOT NULL,
    effective_from        timestamptz NOT NULL,
    PRIMARY KEY (price_book_version, meter_id, subject_id, modifier)
);

-- =============================================================================
-- meters (T010b)
-- =============================================================================

CREATE TABLE IF NOT EXISTS meters (
    meter_id     text NOT NULL,
    version       int NOT NULL,
    unit          text NOT NULL,
    accrual       text NOT NULL CHECK (accrual IN ('discrete', 'duration', 'level')),
    reservable    boolean NOT NULL,
    billable      boolean NOT NULL DEFAULT true,
    PRIMARY KEY (meter_id, version)
);

-- =============================================================================
-- fx_rates (T010b)
-- =============================================================================

CREATE TABLE IF NOT EXISTS fx_rates (
    fx_version       text NOT NULL,
    from_currency     char(3) NOT NULL,
    to_currency       char(3) NOT NULL,
    rate              numeric(20, 10) NOT NULL,
    effective_from    timestamptz NOT NULL,
    PRIMARY KEY (fx_version, from_currency, to_currency)
);

-- =============================================================================
-- skills
--
-- Note: data-model.md's Skill also has bundle_digest/origin/executable/
-- publisher_ref/signature/pinned_upstream_version/trust_tier/
-- declared_tool_ids/suite_id/eval_run_id/catalog_scan_status -- these belong
-- to a later task (skills/catalog trust, out of this batch's scope) and are
-- intentionally omitted; a later migration adds them without breaking this
-- one (expand/contract).
-- =============================================================================

CREATE TABLE IF NOT EXISTS skills (
    skill_id      uuid NOT NULL,
    tenant_id     uuid NOT NULL REFERENCES tenants (tenant_id),
    name          text NOT NULL,
    description   text NOT NULL DEFAULT '',
    body          text NOT NULL DEFAULT '',
    version       int NOT NULL,
    status        text NOT NULL DEFAULT 'proposed' CHECK (status IN ('proposed', 'approved', 'promoted')),
    created_at    timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (skill_id, version)
);

-- =============================================================================
-- connectors
--
-- secret_handle stores a vault handle only, never a raw credential.
-- =============================================================================

CREATE TABLE IF NOT EXISTS connectors (
    connector_id    uuid PRIMARY KEY,
    tenant_id       uuid NOT NULL REFERENCES tenants (tenant_id),
    kind            text NOT NULL,
    secret_handle   text NOT NULL,
    scope           jsonb NOT NULL DEFAULT '{}'::jsonb,
    auth_kind       text NOT NULL CHECK (auth_kind IN ('tenant_service', 'per_user_oauth')),
    token_audience  text NULL,
    transport       text NOT NULL DEFAULT 'http' CHECK (transport IN ('http', 'stdio'))
);
