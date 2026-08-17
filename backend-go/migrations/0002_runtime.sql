-- 0002_runtime.sql
--
-- Second migration in the lexically-ordered sequence
-- (0001_config.sql -> 0002_runtime.sql -> 0003_rls.sql), applied in that
-- order by executing each file's raw SQL against Postgres (see
-- backend-go/tests/integration/rls_isolation_test.go).
--
-- Defines the platform's MUTABLE runtime/event-sourced state: sessions,
-- events, delegations, effect classes, orchestration plans, checkpoints,
-- snapshots, idempotency claims, and content access grants.
--
-- This is part A of this file. Two later tasks APPEND more tables here
-- (cost/billing, then oversight/security/memory) -- this file is
-- intentionally left open-ended and does not end with a closing marker.
--
-- Tables appear in FK-dependency order: `sessions` first (referenced by
-- most of what follows), then every table that references
-- sessions(session_id) or tenants(tenant_id).
--
-- Governance (binding, copied from the pinned bundle):
--   * Append-only: `events` is never updated/deleted; `seq` is monotonic
--     per session -- enforced here via UNIQUE(session_id, seq); strictly
--     monotonic insertion order is an application-code invariant.
--   * Three artifacts, not one: `condensation` is an EVENT TYPE (see the
--     `events.type` taxonomy below), not a table. `checkpoints`
--     (machine-facing resume) and `snapshots` (disposable projection
--     cache) are distinct tables and are never merged.
--   * Write-ahead: `idempotency_claims` rows are committed `in_flight`
--     before the effect leaves the process -- an application-code
--     invariant this schema supports but does not itself enforce.
--   * Identity binds, names do not: `events.tool_id` (and any other tool
--     reference in this file) is the qualified `tool_id` string from the
--     `tools` table (0001_config.sql, T010), never a bare name.
--   * security-and-owasp.instructions.md S1/S4: no hardcoded credentials
--     anywhere in this file.
--   * backing-services.instructions.md: forward-only, additive-first
--     migrations; foreign-key and frequently-filtered columns are
--     indexed below.
--
-- No RLS policies here -- row-level security is enabled in 0003_rls.sql
-- (T012) for every tenant-scoped table across all migration files,
-- including this one.
--
-- Migrations are forward-only and additive; there is no rollback/down file
-- for this project.

-- =============================================================================
-- sessions
-- =============================================================================

CREATE TABLE IF NOT EXISTS sessions (
    session_id               uuid PRIMARY KEY,
    session_key               text NOT NULL,
    tenant_id                 uuid NOT NULL REFERENCES tenants (tenant_id),
    user_id                   uuid NOT NULL REFERENCES users (user_id),
    audience_ref               text NULL,
    agent_id                  uuid NOT NULL,
    agent_version              int NOT NULL,
    harness_digest             bytea NOT NULL,
    forked_from_session_id     uuid NULL REFERENCES sessions (session_id),
    fork_seq                   bigint NULL,
    fork_overrides              jsonb NULL,
    data_label                 text NOT NULL DEFAULT 'internal'
                                CHECK (data_label IN ('public', 'internal', 'regulated')),
    route_model_id             text NOT NULL,
    route_reason                jsonb NOT NULL DEFAULT '{}'::jsonb,
    execution_class             text NOT NULL DEFAULT 'interactive'
                                CHECK (execution_class IN ('interactive', 'batch')),
    priority                   int NOT NULL DEFAULT 0,
    region                     text NOT NULL,
    parent_session_id           uuid NULL REFERENCES sessions (session_id),
    root_session_id             uuid NOT NULL,
    depth                      int NOT NULL DEFAULT 0,
    delegation_role             text NOT NULL DEFAULT 'root'
                                CHECK (delegation_role IN ('root', 'leaf')),
    plan_id                    uuid NULL,
    plan_version                int NULL,
    taint_state                 jsonb NOT NULL DEFAULT '{}'::jsonb,
    status                     text NOT NULL DEFAULT 'queued'
                                CHECK (status IN ('queued', 'running', 'suspended', 'terminal')),
    autonomy_level              text NOT NULL
                                CHECK (autonomy_level IN ('read_only', 'supervised', 'full')),
    terminal_reason             text NULL
                                CHECK (
                                    terminal_reason IS NULL OR terminal_reason IN (
                                        'completed', 'max_turns', 'cost_exhausted',
                                        'credit_exhausted', 'error', 'aborted',
                                        'prompt_too_long', 'hook_stopped',
                                        'approval_expired', 'input_expired'
                                    )
                                ),
    active_ms                  bigint NOT NULL DEFAULT 0,
    suspended_ms                bigint NOT NULL DEFAULT 0,
    surface_id                  text NULL,
    created_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_sessions_tenant_id ON sessions (tenant_id);

-- =============================================================================
-- events
--
-- Append-only: application code must never UPDATE or DELETE a row here.
-- `seq` is monotonic per session; UNIQUE(session_id, seq) both enforces
-- that no two events in the same session share a sequence number and
-- doubles as the index needed for ordered per-session reads.
--
-- `payload` is conceptually encrypted at the application layer -- this
-- migration stores ciphertext as jsonb/bytea and does not implement
-- encryption itself.
-- =============================================================================

CREATE TABLE IF NOT EXISTS events (
    event_id         uuid PRIMARY KEY,
    session_id        uuid NOT NULL REFERENCES sessions (session_id),
    tenant_id         uuid NOT NULL REFERENCES tenants (tenant_id),
    seq               bigint NOT NULL,
    schema_version     int NOT NULL,
    type              text NOT NULL CHECK (
                        type IN (
                            'thought', 'content', 'tool_use', 'tool_result',
                            'tool_receipt_ref', 'effect_claimed',
                            'effect_claim_resolved', 'condensation',
                            'context_pruned', 'checkpoint', 'user_message',
                            'input_requested', 'input_answered',
                            'input_expired', 'input_invalidated',
                            'approval_requested', 'approval_notified',
                            'approval_reminded', 'approval_escalated',
                            'approval_granted', 'approval_granted_modified',
                            'approval_denied', 'approval_expired',
                            'approval_invalidated',
                            'approval_resolution_refused',
                            'approval_mismatch', 'budget_decision',
                            'tool_loaded', 'memory_loaded',
                            'skill_activated', 'skill_capability_ignored',
                            'delivery_enqueued', 'delivery_delivered',
                            'delivery_failed', 'delivery_suppressed',
                            'taint_transition', 'sanitization_boundary',
                            'delegation_requested',
                            'delegation_target_selected',
                            'delegation_refused', 'delegation_returned',
                            'delegation_reaped', 'plan_started',
                            'plan_step_entered', 'plan_transition',
                            'plan_step_exited', 'plan_completed',
                            'content_access_granted', 'content_accessed',
                            'content_access_refused', 'error',
                            'stuck_suspected', 'forked', 'terminal',
                            'erasure'
                        )
                    ),
    payload           jsonb NOT NULL DEFAULT '{}'::jsonb,
    payload_digest     bytea NOT NULL,
    key_id            text NOT NULL,
    actor             text NOT NULL CHECK (actor IN ('model', 'tool', 'user', 'system')),
    tool_id           text NULL,
    pair_ref          uuid NULL,
    model_id          text NULL,
    trace_id          bytea NULL,
    span_id           bytea NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    UNIQUE (session_id, seq)
);

CREATE INDEX IF NOT EXISTS idx_events_tenant_id ON events (tenant_id);

-- =============================================================================
-- delegations
-- =============================================================================

CREATE TABLE IF NOT EXISTS delegations (
    delegation_id       uuid PRIMARY KEY,
    tenant_id           uuid NOT NULL REFERENCES tenants (tenant_id),
    parent_session_id    uuid NOT NULL REFERENCES sessions (session_id),
    child_session_id     uuid NULL REFERENCES sessions (session_id),
    root_session_id      uuid NOT NULL,
    depth               int NOT NULL,
    pair_ref            uuid NOT NULL,
    scope_snapshot        jsonb NOT NULL DEFAULT '{}'::jsonb,
    taint_at_spawn        jsonb NOT NULL DEFAULT '{}'::jsonb,
    taint_engaged         jsonb NULL,
    return_schema         jsonb NULL,
    acceptance           jsonb NULL,
    max_summary_tokens     int NOT NULL DEFAULT 2000,
    ceiling_amount        numeric(20, 10) NULL,
    outcome              text NULL
                          CHECK (
                              outcome IS NULL OR outcome IN (
                                  'accepted', 'rejected_schema',
                                  'rejected_acceptance', 'bound_exceeded',
                                  'child_error', 'reaped'
                              )
                          ),
    reap_reason           text NULL
                          CHECK (
                              reap_reason IS NULL OR reap_reason IN (
                                  'parent_terminal', 'parent_cancelled',
                                  'ceiling_exhausted'
                              )
                          ),
    created_at            timestamptz NOT NULL DEFAULT now(),
    closed_at             timestamptz NULL
);

CREATE INDEX IF NOT EXISTS idx_delegations_parent_session_id ON delegations (parent_session_id);
CREATE INDEX IF NOT EXISTS idx_delegations_tenant_id ON delegations (tenant_id);

-- =============================================================================
-- effect_classes
--
-- tenant_id is NULLABLE -- NULL denotes a platform seed shared across all
-- tenants, mirroring the tools.tenant_id NULL-for-global-built-in pattern
-- in 0001_config.sql.
-- =============================================================================

CREATE TABLE IF NOT EXISTS effect_classes (
    effect_class_id     text NOT NULL,
    tenant_id           uuid NULL REFERENCES tenants (tenant_id),
    version             int NOT NULL,
    status              text NOT NULL DEFAULT 'enabled'
                        CHECK (status IN ('draft', 'gated', 'enabled', 'retired')),
    irreversible          boolean NOT NULL DEFAULT false,
    high_value            boolean NOT NULL DEFAULT false,
    description           text NOT NULL DEFAULT '',
    created_at            timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (effect_class_id, version)
);

INSERT INTO effect_classes (effect_class_id, tenant_id, version, irreversible) VALUES
    ('platform/payment', NULL, 1, true),
    ('platform/delete', NULL, 1, true),
    ('platform/prod_change', NULL, 1, true),
    ('platform/external_send', NULL, 1, false),
    ('platform/other', NULL, 1, false)
ON CONFLICT (effect_class_id, version) DO NOTHING;

-- =============================================================================
-- orchestration_plans
-- =============================================================================

CREATE TABLE IF NOT EXISTS orchestration_plans (
    plan_id                uuid NOT NULL,
    tenant_id              uuid NOT NULL REFERENCES tenants (tenant_id),
    version                int NOT NULL,
    status                 text NOT NULL DEFAULT 'draft'
                           CHECK (status IN ('draft', 'gated', 'enabled', 'retired')),
    steps                  jsonb NOT NULL DEFAULT '[]'::jsonb,
    pinned_routes            jsonb NOT NULL DEFAULT '{}'::jsonb,
    cost_envelope_amount      numeric(20, 10) NULL,
    eval_run_id              uuid NULL,
    governance_signoff        jsonb NULL,
    created_at               timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (plan_id, version)
);

-- =============================================================================
-- checkpoints
--
-- Machine-facing resume state -- distinct from `snapshots` (a disposable
-- projection cache); the two are never merged (Three artifacts, not one).
-- =============================================================================

CREATE TABLE IF NOT EXISTS checkpoints (
    checkpoint_id          uuid PRIMARY KEY,
    session_id             uuid NOT NULL REFERENCES sessions (session_id),
    tenant_id              uuid NOT NULL REFERENCES tenants (tenant_id),
    last_seq               bigint NOT NULL,
    harness_digest           bytea NOT NULL,
    in_flight_claim_id        uuid NULL,
    reservation_id           uuid NULL,
    sandbox_handle           jsonb NULL,
    pending_oversight         jsonb NULL,
    provider_request_id       text NULL,
    open_delegations          jsonb NULL,
    created_at               timestamptz NOT NULL DEFAULT now()
);

-- =============================================================================
-- snapshots
--
-- Disposable projection cache -- distinct from `checkpoints`; the two are
-- never merged (Three artifacts, not one).
-- =============================================================================

CREATE TABLE IF NOT EXISTS snapshots (
    snapshot_id             uuid PRIMARY KEY,
    session_id              uuid NOT NULL REFERENCES sessions (session_id),
    tenant_id               uuid NOT NULL REFERENCES tenants (tenant_id),
    at_seq                  bigint NOT NULL,
    projection_version        int NOT NULL,
    state                   jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at                timestamptz NOT NULL DEFAULT now()
);

-- =============================================================================
-- idempotency_claims
--
-- Write-ahead: a claim is committed `in_flight` before the effect leaves
-- the process (application-code invariant). Dedup is tenant-scoped via
-- UNIQUE(tenant_id, idempotency_key) -- the correctness backstop.
-- =============================================================================

CREATE TABLE IF NOT EXISTS idempotency_claims (
    claim_id                uuid PRIMARY KEY,
    tenant_id               uuid NOT NULL REFERENCES tenants (tenant_id),
    session_id               uuid NOT NULL REFERENCES sessions (session_id),
    idempotency_key           text NOT NULL,
    pair_ref                 uuid NOT NULL,
    state                    text NOT NULL DEFAULT 'in_flight'
                             CHECK (state IN ('in_flight', 'completed', 'failed', 'abandoned')),
    resolution                text NULL
                             CHECK (
                                 resolution IS NULL OR resolution IN (
                                     'probe_confirmed', 'probe_absent', 'human_resolved'
                                 )
                             ),
    result_ref                uuid NULL,
    attempts                  int NOT NULL DEFAULT 0,
    created_at                 timestamptz NOT NULL DEFAULT now(),
    resolved_at                timestamptz NULL,
    UNIQUE (tenant_id, idempotency_key)
);

-- =============================================================================
-- content_access_grants
-- =============================================================================

CREATE TABLE IF NOT EXISTS content_access_grants (
    grant_id                 uuid PRIMARY KEY,
    tenant_id                uuid NOT NULL REFERENCES tenants (tenant_id),
    scope                    jsonb NOT NULL DEFAULT '{}'::jsonb,
    requester_user_id          uuid NOT NULL REFERENCES users (user_id),
    authorizer_user_id         uuid NOT NULL REFERENCES users (user_id),
    purpose                  text NOT NULL,
    expires_at                 timestamptz NOT NULL,
    status                   text NOT NULL DEFAULT 'active'
                             CHECK (status IN ('active', 'expired', 'revoked')),
    read_count                 int NOT NULL DEFAULT 0,
    created_at                  timestamptz NOT NULL DEFAULT now()
);
