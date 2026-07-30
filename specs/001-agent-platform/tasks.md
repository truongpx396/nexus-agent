---
description: "Task list for implementing the Production-Grade AI Agent Platform"
---

# Tasks: Production-Grade AI Agent Platform

**Input**: Design documents from `/specs/001-agent-platform/`

**Prerequisites**: [plan.md](plan.md) (required), [spec.md](spec.md) (user stories),
[research.md](research.md), [data-model.md](data-model.md),
[contracts/](contracts/), [quickstart.md](quickstart.md)

**Tests**: Included. The spec mandates contract tests (kernel ABI, control/data-plane,
run-API), multi-tenant isolation / resume / cost-ceiling / HITL integration tests, and
an eval set that gates every prompt/tool/model/skill change in CI (FR-042, FR-043,
FR-044). These test tasks are therefore first-class, not optional.

Two rules govern how they run (FR-097): correctness tests execute against the
**deterministic recorded/fake provider** (T015a) — never a live model, which would
be flaky and billed — and the transcript-hygiene invariants are **property-tested**
over generated event sequences (T033b), because they are total invariants over all
histories rather than a set of examples. Live-model calls are confined to the eval
suite. The eval gate itself is **Foundational** (T026e–T026g), not a later phase:
it must exist before the first behavior-bearing slice, or the window where changes
have the largest effect sizes goes unmeasured.

**Organization**: Tasks are grouped by user story (P1–P3) so each story is an
independently testable increment aligned with the delivery phases in the plan
(kernel → surfaces → trust → cost/observability → memory/skills → config → scale →
consumer surfaces/personal connectors).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US9 (maps to spec user stories); Setup/Foundational/Polish carry no story label
- **[ID]**: A **stable identifier, not an ordering.** Tasks inserted into an existing
  phase take a suffixed id (`T094a`) and a phase inserted later takes the next free
  block, so ids are not monotonic in file order — Phase 11 (US9) holds T159–T170 while
  Polish and Scale keep T143–T158. Execution order comes from the phase sequence and
  the Dependencies section; ids are referenced by other tasks and must not be renumbered.

- All paths are repository-relative and follow the monorepo layout in plan.md

## Path Conventions

- Go backend: `backend-go/` (`cmd/`, `kernel/`, `internal/`, `migrations/`, `tests/`)
- Python helper: `ml-python/src/`, `ml-python/tests/`
- Web surface: `frontend/src/`, `frontend/tests/`
- Deploy assets: `deploy/`

---

## Phase 1: Setup (Shared Infrastructure)

**Purpose**: Monorepo scaffolding, toolchains, and local dev harness

- [ ] T001 Create the monorepo directory tree per plan.md (`backend-go/{cmd/control-plane,cmd/runtime-worker,cmd/surface-gateway,kernel,internal,migrations,tests}`, `ml-python/src`, `frontend/src`, `deploy/`) with a top-level `README.md` and `Makefile` stub
- [ ] T002 Initialize the Go module and workspace in `backend-go/go.mod` (Go 1.23) with baseline deps (`net/http`, gRPC, `pgx`, `go-redis`, OpenTelemetry SDK)
- [ ] T003 [P] Initialize the Python 3.12 helper project in `ml-python/pyproject.toml` (pytest, LLM-as-judge deps) with `ml-python/src/__init__.py`
- [ ] T004 [P] Initialize the React 19 + Vite + Tailwind + React Query web surface in `frontend/package.json` and `frontend/vite.config.ts`
- [ ] T005 [P] Configure Go linting/formatting in `backend-go/.golangci.yml` and `gofmt`/`goimports` via the `Makefile`
- [ ] T006 [P] Configure Python lint/format (ruff + black) in `ml-python/pyproject.toml` and TS lint (eslint + prettier) in `frontend/.eslintrc.cjs`
- [ ] T007 [P] Author `docker-compose.yml` at repo root bringing up Postgres and Redis for local dev (referenced by quickstart.md)
- [ ] T008 [P] Add `Makefile` targets (`migrate`, `seed-tenant`, `run-control-plane`, `run-worker`, `evals`, `test`) as stubs wiring the quickstart commands
- [ ] T009 [P] Add CI workflow skeleton in `.github/workflows/ci.yml` running Go tests, Python tests, and the eval gate placeholder

---

## Phase 2: Foundational (Blocking Prerequisites)

**Purpose**: Data layer, kernel/harness interface seams, and cross-cutting infra that
ALL user stories build on. No user-story work begins until this phase completes.

**⚠️ CRITICAL**: Blocks every user story below.

This phase carries the decisions that are **expensive to retrofit and cheap to make
now** — the event envelope and taxonomy, transaction-local tenant scoping, the
encryption/erasure model, the audit chain, the pre-spend budget gate, the
deterministic provider, and the eval gate. Everything later in this file is
additive against them; none of it requires migrating the event log, the audit
chain, or the encryption model. That is what makes the MVP cut line in plan.md
safe (see "MVP cut line" — infrastructure is deferred, seams are not).

### Data model & tenant isolation (data-model.md)

- [ ] T010 Author the Postgres migration framework and base schema for immutable config tables (`Tenant`, `User`, `Agent`, `Tool`, `Model`, `PriceBook`, `Skill`, `Connector`) in `backend-go/migrations/0001_config.sql` — `Tool` carries a nullable `tenant_id` (NULL only for global built-ins) plus taint declarations (`returns_untrusted` / `reads_private_data` / `mutates_external`, all defaulting TRUE), `concurrency` class, `effect_class`, `idempotency_key_spec` (FR-011, FR-087), and the catalog admission scan fields `catalog_scan_status` / `scan_policy_version` / `scanned_at` (FR-113); `Model` carries `pinned_snapshot` and `regions_allowed` (FR-078, FR-091); `Connector` carries `token_audience` (FR-114)
- [ ] T010a [P] Author the versioned, effective-dated `PriceBook` schema + loader (per-model, per-token-class rates; cost never computed from constants in code) in `backend-go/migrations/0001_config.sql` and `backend-go/internal/cost/pricebook.go` (FR-084)
- [ ] T011 Author the append-only runtime-state migration (`Session`, `Event`, `Checkpoint`, `Cost Record`, `Budget`, `BudgetReservation`, `Memory`, `Approval`, `ApprovalPolicy`, `InputRequest`, `Audit Receipt`, `Audit Anchor`, `Encryption Key`, `Sandbox`) in `backend-go/migrations/0002_runtime.sql` — `Event` carries `schema_version`, `payload_digest`, `key_id`, and `actor`; `Session` carries pinned `agent_version`, `autonomy_level`, `data_label`, `route_model_id`/`route_reason`, `execution_class`/`priority`, `region`, and `taint_state`; `Approval` carries `approved_input_digest`, `idempotency_key`, `kind`/`member_digests`, `context_mode`, `assignee_ref`, `resolution_token_hash`, and `invalidation_reason`; `Audit Receipt` carries `kind` + nullable `approval_id`; `Cost Record` splits input tokens by class and references `price_book_version` (FR-016, FR-085, FR-086, FR-088, FR-103–FR-112). These columns are a **foundational seam**: retrofitting an argument digest onto approvals already written is exactly the migration the append-only log exists to avoid
- [ ] T011b **Add the observability and state-integrity seams to the runtime schema** in `backend-go/migrations/0002_runtime.sql`: `Event` carries `trace_id`/`span_id` (the bidirectional trace↔log join, FR-119); `Session` carries `harness_digest`, `forked_from_session_id`/`fork_seq`/`fork_overrides`, and the `active_ms`/`suspended_ms` split (FR-129, FR-128, FR-120); `Checkpoint` carries `harness_digest`, `in_flight_claim_id`, `reservation_id`, `sandbox_handle`, `pending_oversight`, `provider_request_id`, `open_delegations` (FR-126); new `Snapshot` (`at_seq`, `projection_version`, encrypted `state`), `IdempotencyClaim` (`idempotency_key`, `state`, `resolution`, `attempts`), and `ContentAccessGrant` (`scope`, `requester_user_id`, `authorizer_user_id`, `purpose`, `expires_at`) tables (FR-126, FR-127, FR-118); `CostRecord` carries `harness_digest`, `reservation_id`, `outbox_state` (FR-124). **Foundational for the same reason as T011a**: a join key, a config digest, and a claim table cannot be retrofitted onto events already written
- [ ] T011a **Add the delegation-chain seam to the runtime schema** — `Session` and `Cost Record` carry `parent_session_id` / `root_session_id` / `depth` (plus `Session.delegation_role`, `plan_id`/`plan_version`), and `Audit Receipt` carries `root_session_id` + `delegation_path` bound into its `digest` — in `backend-go/migrations/0002_runtime.sql`. **Foundational even though delegation itself is deferred**: retrofitting a chain onto historical cost records and receipts is precisely the event-log migration the append-only design exists to avoid (FR-101, FR-081)
- [ ] T012 Add Postgres row-level-security policies keyed on `tenant_id` for every tenant-scoped table — including `Tool` where `tenant_id IS NOT NULL` (zero rows without tenant context) in `backend-go/migrations/0003_rls.sql` (FR-011, FR-038, FR-039)
- [ ] T012a **Establish the expand/contract migration discipline** (additive-first, destructive cleanup deferred a release; every migration verified in CI against the immediately preceding application version so a rolling deploy never needs two schemas at once) in `backend-go/migrations/README.md` and `.github/workflows/ci.yml` (FR-094, FR-026)
- [ ] T013 [P] Define immutable config domain types (`Tenant`, `User`, `Agent`, `Tool` incl. `TaintDeclaration`, `Model`, `PriceBook`, `Skill`, `Connector`) in `backend-go/internal/tenancy/model.go` and `backend-go/internal/tools/model.go` (FR-087)
- [ ] T014 [P] Define append-only runtime types (`Session`, `Event`, `Checkpoint`, `CostRecord`, `Budget`, `BudgetReservation`, `Memory`, `Approval`, `ApprovalPolicy`, `InputRequest`, `AuditReceipt`, `AuditAnchor`, `EncryptionKey`, `Sandbox`) with the **full** typed `Event.type` taxonomy (model output, tool, context, `user_message`, the full approval lifecycle incl. `notified`/`reminded`/`escalated`/`granted_modified`/`invalidated`/`resolution_refused`/`mismatch`, the input-request lifecycle, taint transitions, `error`, `terminal`, `erasure`), the event `schema_version` envelope, and the 9-value `TerminalReason` enum (…`approval_expired`, `input_expired`) in `backend-go/kernel/types.go` (FR-085, FR-086, FR-004, FR-110)
- [ ] T014b [P] Define the separated durable-state types — `Condensation` (model-facing, FR-015), `Checkpoint` (machine-facing resume, FR-024), `Snapshot` (disposable projection cache, FR-126), `IdempotencyClaim` (`in_flight`/`completed`/`failed`/`abandoned`, FR-127), `ContentAccessGrant` (FR-118), `HarnessDigest` (FR-129) — plus the added event types `effect_claimed` · `effect_claim_resolved` · `content_access_granted` · `content_accessed` · `content_access_refused` · `forked`, in `backend-go/kernel/types.go` and `backend-go/internal/reliability/state.go` (FR-085, FR-126–FR-129)
- [ ] T014a [P] Implement the event **envelope versioning + upcasting registry** (read an event written under any prior `schema_version` into the current in-memory shape; replay across schema change is covered by a test) in `backend-go/kernel/eventversion.go` (FR-086)

### Kernel/harness interface seams (contracts/kernel-abi.md)

- [ ] T015 [P] Declare the `Provider` interface + normalized `Chunk` stream contract — with `usage` split into `input_uncached` / `input_cache_read` / `input_cache_write` / `output_tokens`, and an opaque `reasoning` chunk — in `backend-go/internal/provider/provider.go` (FR-016, FR-027, FR-064)
- [ ] T015a [P] Implement the **deterministic recorded/fake `Provider`** satisfying the same contract (scripted turns plus truncation, stall, malformed-stream, and failover paths; fixtures versioned with the contract) so the correctness suite is reproducible and never bills a live model, in `backend-go/internal/provider/fake/fake.go` and `backend-go/internal/provider/fake/fixtures/` (FR-097)
- [ ] T016 [P] Declare the `Tool` interface (self-describing, per-invocation checks, **required `TaintDeclaration` defaulting to all three legs true**, `effectClass`) in `backend-go/internal/tools/tool.go` (FR-007, FR-008, FR-009, FR-011, FR-087)
- [ ] T016c [P] Declare the `Persistence` interface (`append` / `checkpoint` / `snapshot` / `hydrate` / `claim` / `resolveClaim`) in `backend-go/internal/reliability/persistence.go` and the `Telemetry` interface (allowlisted span attributes, fixed metric label sets, exemplars) in `backend-go/internal/observability/telemetry.go` (FR-117–FR-127)
- [ ] T016a [P] Declare the `RunControl` interface (`steer` / `cancel` / `resume` / `replay` / `fork` / `tightenAutonomy` — ratchet only, no widening operation exists; `replay` is pure, `fork` creates a new run with external effects disabled, FR-128), the `Oversight` interface (`requestApproval` / `requestInput` / `invalidate`), and the `BudgetGate` interface (`reserve` / `reconcile`) in `backend-go/kernel/control.go`, `backend-go/kernel/oversight.go`, and `backend-go/internal/cost/gate.go` (FR-005, FR-083, FR-103–FR-111)
- [ ] T016b [P] Declare the `Delegation` interface (`delegate` / `reap`, `DelegationSpec` with subset-only `scope` and no scope-widening model-facing parameter, `DelegationResult` with `taint_engaged` + typed `outcome`, `ReapReason`) per contracts/kernel-abi.md in `backend-go/kernel/delegation.go` (FR-098, FR-099, FR-100)
- [ ] T017 [P] Declare the `Memory` interface in `backend-go/internal/memory/memory.go` (FR-019)
- [ ] T018 [P] Declare the `Workspace`/`Sandbox` interface in `backend-go/internal/sandbox/workspace.go` (FR-047)
- [ ] T019 [P] Declare the `Surface` adapter interface in `backend-go/internal/surfaces/surface.go` (FR-001, FR-028, FR-031)

### Cross-cutting infrastructure

- [ ] T020 [P] Implement tenant context propagation + RLS scoping using a **transaction-local** `SET LOCAL app.tenant_id` (or `SET ROLE LOCAL`) issued inside each transaction — session-level `SET` is prohibited and MUST be blocked by a lint/assertion, because the production PgBouncer tier runs in transaction-pooling mode and reassigns a connection between tenants between statements — in `backend-go/internal/tenancy/context.go` (FR-039)
- [ ] T020a [P] Implement per-tenant envelope encryption for event payloads and other customer content (`EncryptionKey` custody in vault/KMS, platform-managed and BYOK/CMK modes, `payload_digest` computed over plaintext so the audit chain survives redaction) in `backend-go/internal/security/envelope.go` (FR-089, FR-080)
- [ ] T021 [P] Implement the Postgres event-log store (append event with `schema_version` + digest + `key_id`, read by session `seq`, upcast on read) in `backend-go/internal/queue/eventlog.go` (FR-006, FR-086)
- [ ] T022 [P] Implement the abstract durable-queue port with a NATS JetStream default adapter (persisted-consumer redelivery for re-queue-from-checkpoint) in `backend-go/internal/queue/queue.go`, plus a broker-agnostic Redis session-key serial lock (per-session serial, cross-session concurrent) in `backend-go/internal/queue/sessionlock.go` (FR-041, FR-046)
- [ ] T023 [P] Implement structured error handling + typed failure taxonomy skeleton in `backend-go/internal/reliability/errors.go`
- [ ] T024 [P] Implement OpenTelemetry span bootstrap (structure-only, no content) + logging config in `backend-go/internal/observability/otel.go` (FR-040)
- [ ] T024a [P] Implement the **content-free export path**: a deny-by-default attribute-key allowlist span/log processor (an unlisted key is *dropped*, not truncated), a bounded max attribute-value length, and reduction of free-text provider/error strings to typed classes + digests before export, in `backend-go/internal/observability/allowlist.go` (FR-117). No configuration, env var, or debug flag may admit content — there is deliberately no equivalent of a `LOG_PROMPTS` switch, because one voids the FR-080 erasure attestation
- [ ] T024b [P] Implement the **versioned telemetry attribute model** + exporter-side mapping to a pinned `gen_ai.*` convention version, with dual-emit across a convention rename, in `backend-go/internal/observability/attributes.go` (FR-121)
- [ ] T024c [P] Implement **W3C trace-context propagation** into sandbox exec, connector/MCP calls, the provider request, and child sessions (correlation identifiers only — never tenant identity or content) in `backend-go/internal/observability/propagation.go` (FR-123)
- [ ] T025 [P] Implement runtime configuration loader (tenant/agent/config read at runtime, never forked) in `backend-go/internal/tenancy/config.go` (FR-050)
- [ ] T026 Implement the versioned control-plane ↔ data-plane handshake per contracts/control-data-plane.md — including the enumerated **structure-only egress list** and the `audit_sink_mode` / `telemetry_sink_mode` = `upstream` | `local` switch so a customer-boundary deployment can emit nothing at all — in `backend-go/internal/queue/controlplane.go` (FR-030, FR-091)

### Cost, audit & erasure foundations (must exist before behavior is tuned)

- [ ] T026a Implement the **pre-spend budget gate**: atomic per-tenant/per-task reservation counters in Redis with TTL-bounded holds, refusal → `cost_exhausted` *before* the model call, and reconciliation releasing the unused remainder — plus a worker-local hard per-run budget enforced synchronously so enforcement never depends on a cross-plane round trip — in `backend-go/internal/cost/reservation.go` (FR-083, FR-017)
- [ ] T026b Implement the **hash-chained audit receipt** writer (`chain_seq` + `prev_digest` + digest over arg/result **digests**, signed by a sign-only KMS/HSM key the data plane cannot read) and the periodic **chain anchor** publisher in `backend-go/internal/audit/chain.go` and `backend-go/internal/audit/anchor.go` (FR-081, FR-040)
- [ ] T026c [P] Implement the scheduled **audit-chain verifier** (walks the chain, proves continuity, sequence completeness, signature validity, and agreement with the latest anchor; alerts on any break or gap) in `backend-go/internal/audit/verify.go` (FR-081, SC-015)
- [ ] T026d Implement the **erasure / crypto-shredding** path (destroy the tenant/subject content key, append a typed `erasure` event + receipt, never delete or rewrite an event row) and the DSAR **access** export enumerating data held for a subject across events, memory, cost records, and connector authorizations, in `backend-go/internal/security/erasure.go` (FR-080, SC-014)

### Foundational evals & test harness (Constitution IX — before any behavior-bearing slice)

- [ ] T026e [P] Implement the eval runner over a versioned ~20-real-case set with end-state checks in `ml-python/src/evals/runner.py` (FR-043) *(moved earlier from US4 — the gate must precede the changes it grades)*
- [ ] T026f [P] Implement the LLM-as-judge rubric scorer + held-out grader protection in `ml-python/src/judge/rubric.py` (FR-043)
- [ ] T026g Wire the eval gate into CI (≥90% pass AND zero regressions vs baseline) in `.github/workflows/ci.yml` and `ml-python/src/evals/gate.py` (FR-042, FR-043)

### Foundational contract tests

- [ ] T027 [P] Contract test asserting the kernel ABI interfaces compile with ≥1 stub impl each — including the fake `Provider`, a `TaintDeclaration`-bearing `Tool`, `RunControl`, `BudgetGate`, and `Delegation` in `backend-go/tests/contract/kernel_abi_test.go`
- [ ] T028 [P] Contract test for the control/data-plane versioned handshake, including that only enumerated structure-only fields cross the boundary and that `audit_sink_mode=local` emits nothing upstream, in `backend-go/tests/contract/control_data_plane_test.go` (FR-030, FR-091)
- [ ] T029 [P] Integration test asserting RLS returns zero cross-tenant rows **executed through the production PgBouncer transaction-pooling tier** (testcontainers Postgres + PgBouncer), including an interleaved two-tenant workload on a shared pooled connection; a variant asserting session-level `SET` leaks documents *why* the transaction-local form is mandatory in `backend-go/tests/integration/rls_isolation_test.go` (FR-039, SC-013)
- [ ] T029a [P] Integration test: an event written under an older `schema_version` still replays correctly after a schema change (upcasting registry) in `backend-go/tests/integration/event_versioning_test.go` (FR-086)
- [ ] T029b [P] Integration test: audit-chain verification detects injected tampering — record modification, deletion, and reordering — and passes on an untampered chain in `backend-go/tests/integration/audit_chain_test.go` (FR-081, SC-015)
- [ ] T029c [P] Integration test: erasure destroys the key, renders payloads unrecoverable, and leaves the event sequence replayable and the audit chain verifying (no row deleted) in `backend-go/tests/integration/erasure_test.go` (FR-080, SC-014)
- [ ] T029d [P] Integration test: a concurrent burst of sessions in one tenant against a nearly-exhausted budget never exceeds the ceiling (pre-spend reservation), and reserved-but-unused budget is fully released with no drift over a sustained run in `backend-go/tests/integration/budget_reservation_test.go` (FR-083, SC-016)

- [ ] T029e [P] Integration test — **telemetry carries no content**: content-shaped values injected at every span-, metric-, and log-producing call site are 100% dropped by the allowlist before reaching an in-memory exporter, and a post-erasure sweep of the exporter finds no plaintext derived from the erased subject in `backend-go/tests/integration/telemetry_content_free_test.go` (FR-117, SC-033)
- [ ] T029f [P] Integration test — **trace ↔ log join**: every span resolves to the exact event range it covers and every event resolves to its trace, in both directions, including for a run that suspended and a run whose worker was killed mid-turn in `backend-go/tests/integration/trace_event_join_test.go` (FR-119, FR-120, SC-035)

**Checkpoint**: Foundation ready — data layer with transaction-local RLS proven through the pooler, encryption/erasure, chained audit, pre-spend budget gate, deterministic provider, a content-free telemetry path, and a live eval gate. User stories can now begin, and every change from here is measured.

---

## Phase 3: User Story 1 - Complete a real task through a reliable agent (Priority: P1) 🎯 MVP

**Goal**: A single reliable kernel loop (observe → think → act) that classifies each
model response into a typed union, pairs every `tool_use` with a `tool_result`, stops
on a per-task cost ceiling, and ends in a typed terminal reason — verified against
acceptance criteria, never self-declared.

**Independent Test**: Submit a multi-turn tool-using task; confirm paired tool
results, a small cost ceiling forces `cost_exhausted`, and every run ends with a typed
terminal reason (per quickstart.md Scenario 1).

### Tests for User Story 1 ⚠️ (write first, ensure they FAIL)

- [ ] T030 [P] [US1] Contract test for `POST /v1/runs` + `GET /v1/runs/{id}` + `/events` per run-api.openapi.yaml in `backend-go/tests/contract/run_api_test.go`
- [ ] T031 [P] [US1] Integration test: multi-turn tool-using run pairs every `tool_use` with a `tool_result` (synthetic on error) in `backend-go/tests/integration/loop_pairing_test.go` (FR-003)
- [ ] T031a [P] [US1] Integration test: the pre-model-call hygiene pass drops orphan `tool_result`s, backfills a synthetic result for any unpaired `tool_use`, and never sends malformed history to the provider in `backend-go/tests/integration/loop_hygiene_test.go` (FR-060)
- [ ] T032 [P] [US1] Integration test: per-task cost ceiling breach terminates with `cost_exhausted` in `backend-go/tests/integration/cost_ceiling_test.go` (FR-017)
- [ ] T033 [P] [US1] Unit test: response classifier returns `TOOL_CALLS`/`CONTENT`/`EMPTY` (no string matching) in `backend-go/kernel/classify_test.go` (FR-002)
- [ ] T033b [P] [US1] **Property-based** test: over generated event sequences (interleaved tool calls, cancels, errors, truncations, resumes), every `tool_use` always has exactly one paired `tool_result` before the next model call and no orphan result survives the hygiene pass — a total invariant, so property-tested rather than sampled by examples, in `backend-go/kernel/invariant_property_test.go` (FR-003, FR-060, FR-097)
- [ ] T033c [P] [US1] Integration test: cancelling an in-flight run terminates it with `aborted`, backfills a synthetic `tool_result` for any outstanding `tool_use`, and returns the best partial artifact — proving every terminal reason has a producer — in `backend-go/tests/integration/cancel_run_test.go` (FR-004, FR-005, FR-067, SC-020)
- [ ] T033d [P] [US1] Integration test: per-turn cost records split input tokens into uncached / cache-read / cache-write, resolve to a `price_book_version`, and recompute `cost_usd` identically from the price book — so the >90% cache-read gate is measured, not estimated — in `backend-go/tests/integration/cost_measurement_test.go` (FR-016, FR-084, SC-017)
- [ ] T033a [P] [US1] Unit test: every model call carries a bounded `max_tokens` + stop sequences and the model's own reply is schema/grammar-constrained (no conversational filler) in `backend-go/internal/provider/output_controls_test.go` (FR-073)
- [ ] T034 [P] [US1] Integration test: built-in filesystem tools are workspace-restricted (path-escape/`..` denied, no cross-tenant access), outputs capped/paginated, contents flagged untrusted in `backend-go/tests/integration/fs_tools_test.go` (FR-056)
- [ ] T035 [P] [US1] Integration test: the built-in shell tool applies per-invocation parsed-input safety (`ls` allowed, `rm -rf /` denied), a per-command timeout, and runs only in the sandbox in `backend-go/tests/integration/shell_tool_test.go` (FR-057)
- [ ] T035a [P] [US1] Integration test: a turn's tool calls are partitioned into a parallel concurrency-safe batch and serial exclusive calls, an exclusive call never runs concurrently, and results are returned in submission order (fail-closed to serial when metadata absent) in `backend-go/tests/integration/tool_concurrency_test.go` (FR-061)
- [ ] T035h [P] [US1] Integration test — **crash mid-effect**: a worker killed *between* dispatching a state-changing call and recording its result leaves an `in_flight` claim that resume resolves by probe or human escalation, never by re-execution and never by silent discard; an unresolvable claim raises the signal instead of being swept in `backend-go/tests/integration/inflight_claim_test.go` (FR-127, SC-036)
- [ ] T035b [P] [US1] Integration test: a re-issued state-changing tool call (retry / at-least-once redelivery / resume-from-checkpoint) executes its external effect exactly once via a durable tenant-scoped idempotency key in `backend-go/tests/integration/tool_idempotency_test.go` (FR-071)
- [ ] T035c [P] [US1] Integration test: the normalized provider contract persists and round-trips opaque `reasoning_content` (replayed on a subsequent tool-call-referencing turn so the provider does not reject the history, kept out of user-visible output, treated as untrusted) in `backend-go/tests/integration/reasoning_roundtrip_test.go` (FR-064)
- [ ] T035d [P] [US1] Integration test: mid-run steering input alters an in-flight run (a `POST /v1/runs/{id}/input` message is incorporated on the next turn without restarting the run) in `backend-go/tests/integration/mid_run_steering_test.go` (FR-005)
- [ ] T035e [P] [US1] Integration test: the Gate-3 hybrid safety classifier resolves a deterministic-rule case (e.g. `ls`) with **zero** model calls, routes an ambiguous parsed command to the model-based leg, and on a simulated model-classifier timeout/error fails closed to `ASK` (never `ALLOW`) for every pending invocation in `backend-go/tests/integration/hybrid_classifier_test.go` (FR-009, FR-116)

### Implementation for User Story 1

- [ ] T036 [P] [US1] Implement the response classifier (typed union over parsed model output) in `backend-go/kernel/classify.go` (FR-002)
- [ ] T037 [P] [US1] Implement the terminal-reason resolver (exhaustive `TerminalReason` enum) in `backend-go/kernel/terminal.go` (FR-004)
- [ ] T038 [US1] Implement the async-generator kernel step loop (observe → think → act, dispatch on classification) in `backend-go/kernel/loop.go` (FR-001, FR-002; depends on T036, T037)
- [ ] T039 [US1] Enforce the `tool_use`→`tool_result` invariant with synthetic results on cancel/error before the next model call in `backend-go/kernel/invariant.go` (FR-003)
- [ ] T039a [US1] Implement the pre-model-call loop-hygiene pass (drop orphan `tool_result`s, backfill missing `tool_result`s, prune/condense stale tool observations) in `backend-go/kernel/hygiene.go` (FR-060; depends on T039)
- [ ] T040 [P] [US1] Implement a first concrete `Provider` adapter (Anthropic-native or CLI-subprocess fallback) with normalized chunk streaming in `backend-go/internal/provider/anthropic.go` (FR-027)
- [ ] T040a [US1] Extend the normalized provider contract to persist and round-trip opaque `reasoning_content` (replayed on tool-call-referencing turns, kept out of user-visible output, treated as untrusted) in `backend-go/internal/provider/reasoning.go` (FR-064; depends on T015, T040)
- [ ] T040b [P] [US1] Implement per-provider tool JSON-schema normalization (strip/rewrite keywords a backend rejects, e.g. `pattern`/`minLength`/`$ref`) in `backend-go/internal/provider/schema.go` (FR-065)
- [ ] T040c [P] [US1] Implement output-side generation controls (bounded default `max_tokens` + stop sequences + schema/grammar-constrained reply decoding + terse-reasoning style) in `backend-go/internal/provider/output_controls.go` (FR-073)
- [ ] T041 [P] [US1] Implement the tool execution pipeline (validate → permission → execute → result-budget → telemetry) + self-registering registry + `buildTool` factory in `backend-go/internal/tools/registry.go` (FR-007, FR-011)
- [ ] T041a [US1] Implement per-turn tool-call concurrency partitioning (read-only/concurrency-safe/exclusive metadata → parallel-safe batch + serial exclusive calls, results yielded in submission order, fail-closed to serial) in `backend-go/internal/tools/concurrency.go` (FR-061, FR-008; depends on T041)
- [ ] T041b [P] [US1] Implement deferred tool disclosure + `tool_search` (advertise name+description for deferred tools, load full schema on demand to keep the cache-stable prefix small) in `backend-go/internal/tools/deferred.go` (FR-062, FR-013)
- [ ] T041c [US1] Implement per-effect idempotency-key derivation + durable tenant-scoped dedup in the tool execution pipeline (state-changing tools/connectors execute exactly once under retry/redelivery/resume) in `backend-go/internal/tools/idempotency.go` (FR-071; extends T041)
- [ ] T041e [US1] Make the claim **write-ahead** (pipeline steps 9b/10a): commit the `IdempotencyClaim` durably in `in_flight` *before* the effect leaves the process, close it on a recorded outcome, short-circuit a `completed` claim to its stored result, and on resume resolve an `in_flight` claim by provider probe where supported or by typed human escalation where not — never by re-execution and never by discarding it; a claim unresolved beyond its bound raises an operational signal in `backend-go/internal/tools/idempotency.go` and `backend-go/internal/reliability/claim_resolve.go` (FR-127; extends T041c)
- [ ] T041d [US1] Implement the Gate-3 hybrid safety classifier: a deterministic rule pass (allow/deny/blocklist over parsed input) resolves the common case in-process with no external call; input the rule pass cannot classify falls through to a model-based classifier carrying its own bounded timeout that fails closed to `ASK` (never `ALLOW`) on timeout, error, or an unparseable verdict. Wire it as the mechanism behind FR-009's per-invocation check (consumed by the shell tool of T043 and any future parsed-input-judged tool), in `backend-go/internal/security/safety_classifier.go` (FR-009, FR-116; extends T041)
- [ ] T042 [P] [US1] Implement built-in workspace-restricted filesystem tools (`file_list`/`file_read`/`file_search`/`file_write`/`file_edit`, poka-yoke absolute paths, capped/paginated output, contents treated as untrusted) in `backend-go/internal/tools/builtin/fs.go` (FR-056)
- [ ] T043 [P] [US1] Implement the built-in shell / code-execution tool (sandbox-scoped with hard resource limits + network default-deny per FR-059, per-invocation parsed-input safety via the T041d hybrid classifier, allow/blocklist, per-command timeout, fail-closed) in `backend-go/internal/tools/builtin/shell.go` (FR-057, FR-059, FR-116; depends on T041d)
- [ ] T044 [P] [US1] Implement per-turn token/cost metering attributed to task+tenant — recording **uncached / cache-read / cache-write input and output tokens separately**, pricing them through the versioned price book (T010a), and stamping `price_book_version` on every cost record — wired to the pre-spend reservation gate (T026a) rather than a post-hoc ceiling check, in `backend-go/internal/cost/meter.go` (FR-016, FR-017, FR-083, FR-084)
- [ ] T045 [US1] Implement acceptance-criteria verification (no self-declared success; end-state checks) in `backend-go/kernel/verify.go` (FR-044)
- [ ] T045a [US1] Enforce run-determinant pinning: resolve and **pin `agent_version` at run start**, holding it for the run's life so a concurrent deploy cannot shift behavior or bust the cache-stable prefix mid-run, and persist `data_label`, `route_model_id`/`route_reason`, `execution_class`/`priority`, and `region` on the session so routing is auditable and replayable and load-shedding has a field to read, in `backend-go/kernel/pinning.go` (FR-088, FR-013, FR-049)
- [ ] T045b [P] [US1] Integration test: a run started before an agent-version deploy completes on the pinned version (behavior and prompt prefix unchanged mid-run) while a run started after uses the new one in `backend-go/tests/integration/agent_pinning_test.go` (FR-088, FR-026)
- [ ] T046 [US1] Wire the runtime-worker entrypoint (pull session → run kernel loop → append events) in `backend-go/cmd/runtime-worker/main.go`
- [ ] T047 [US1] Implement the run-submission REST surface (`POST /v1/runs`, `GET /v1/runs/{id}`, `GET /v1/runs/{id}/events` SSE with `Last-Event-ID`/`from_seq` resume so a dropped subscriber catches up without loss or duplication) in `backend-go/cmd/surface-gateway/runs.go` (run-api.openapi.yaml, FR-031)
- [ ] T048 [US1] Implement the run-lifecycle operations — mid-run steering (`POST /v1/runs/{id}/input`, delivered to the running session's steering queue under its serial lock and appended as a `user_message` event), **cancel** (`POST /v1/runs/{id}/cancel`, the only producer of `aborted`, honoring the paired-result invariant and returning the partial artifact), **resume** (`POST /v1/runs/{id}/resume` from the last checkpoint), and **fork** (`POST /v1/runs/{id}/fork` — a *new* run from a chosen seq with declared overrides and external effects disabled, attributed to the initiating human, FR-128) — in `backend-go/cmd/surface-gateway/input.go` and `backend-go/kernel/control.go` (FR-005, FR-004, FR-024)

**Checkpoint**: User Story 1 is a standalone MVP — a reliable, cost-bounded, typed-terminal agent loop reachable via the REST surface.

---

## Phase 4: User Story 2 - Reach the same agent from many surfaces (Priority: P2)

**Goal**: The same kernel reachable from CLI, chat, web, REST/gRPC, email, and cron —
each a thin adapter translating only I/O, with identical control flow and guarantees,
streaming/polling long runs instead of blocking a connection.

**Independent Test**: Run the same task via ≥3 surfaces (CLI, API, chat) and confirm
identical control flow, safety/cost guarantees, and no per-surface fork (quickstart.md Scenario 2).

### Tests for User Story 2 ⚠️

- [ ] T049 [P] [US2] Integration test: identical control flow + terminal reason across CLI, API, and chat adapters in `backend-go/tests/integration/multi_surface_test.go` (FR-028)
- [ ] T050 [P] [US2] Integration test: long run streams/polls without a blocked connection in `backend-go/tests/integration/streaming_test.go` (FR-031)

### Implementation for User Story 2

- [ ] T051 [US2] Implement the shared surface-gateway dispatch that maps every adapter to one run model in `backend-go/cmd/surface-gateway/main.go` (FR-001, FR-028)
- [ ] T052 [P] [US2] Implement the CLI surface adapter in `backend-go/internal/surfaces/cli.go`
- [ ] T053 [P] [US2] Implement the chat (Slack/Teams) surface adapter in `backend-go/internal/surfaces/chat.go`
- [ ] T054 [P] [US2] Implement the email surface adapter in `backend-go/internal/surfaces/email.go`
- [ ] T055 [P] [US2] Implement the cron/scheduled-trigger surface adapter in `backend-go/internal/surfaces/cron.go`
- [ ] T056 [P] [US2] Implement the gRPC run surface in `backend-go/internal/surfaces/grpc.go` (FR-028)
- [ ] T057 [P] [US2] Implement the React web surface: run submission + SSE/WS event stream + polling in `frontend/src/services/runs.ts` and `frontend/src/pages/Run.tsx` (FR-031)
- [ ] T058 [US2] Implement stream/poll progress emission shared by all surfaces (structure-only run events over the JetStream pub/sub plane, no blocked connection) in `backend-go/internal/surfaces/emit.go` (FR-031)

**Checkpoint**: The same agent is reachable identically from multiple surfaces; US1 still works.

---

## Phase 5: User Story 3 - Operate safely with enterprise trust (Priority: P2)

**Goal**: Federated identity (per-tenant OIDC issuer, token validation, and just-in-time
user provisioning — the platform never issues credentials itself), per-tenant isolation at
the data layer, attributable immutable audit, vault-injected secrets (model sees only a
handle), delegated identity, the Rule of Two, and scoped human approval that fails closed on
timeout (`approval_expired`).

**Independent Test**: A first-time sign-in JIT-provisions a `User` while invalid/expired/
wrong-issuer tokens are rejected; run two tenants concurrently and prove zero cross-tenant
access; every mutating action has an audit receipt; a high-impact action blocks pending
approval and expires as denial (quickstart.md Scenario 3).

### Tests for User Story 3 ⚠️

- [ ] T059 [P] [US3] Integration test: two-tenant concurrent run, cross-tenant read returns zero rows in `backend-go/tests/integration/tenant_isolation_test.go` (FR-039)
- [ ] T060 [P] [US3] Integration test: every mutating action emits a tamper-evident audit receipt binding user+tenant+tool+args+result+timestamp in `backend-go/tests/integration/audit_receipt_test.go` (FR-040)
- [ ] T061 [P] [US3] Integration test: unanswered approval expires as denial → run ends `approval_expired`, gated action does not proceed in `backend-go/tests/integration/approval_timeout_test.go` (FR-036)
- [ ] T061a [P] [US3] Integration test — **approval binds arguments**: after a grant, an argument substituted by retry, resume-from-checkpoint, at-least-once redelivery, re-plan, or a crafted payload is refused with `approval_mismatch` and produces **zero** external effects; and the approved digest, executed digest, and idempotency key resolve to one artifact for every gated invocation in `backend-go/tests/integration/approval_binding_test.go` (FR-103, FR-071, SC-024)
- [ ] T061b [P] [US3] Integration test — **approver authorization**: resolutions by an agent principal, by the run's initiator on an irreversible class, without `approve:<effect_class>`, with a replayed/forged token, or without a required step-up assertion are all refused, audited as `approval_resolution_refused`, and produce zero effects; and no approval request presents identifiers alone in `backend-go/tests/integration/approval_authz_test.go` (FR-105, FR-104, SC-025)
- [ ] T061c [P] [US3] Integration test — **invalidation**: cancelling, terminating, reaping, ceiling-breaching, or steering a run holding a pending approval invalidates it before the terminal event, delivers the paired synthetic `tool_result`, and a decision arriving afterwards performs nothing and is recorded as refused in `backend-go/tests/integration/approval_invalidation_test.go` (FR-106, FR-003, SC-026)
- [ ] T061d [P] [US3] Integration test — **decision vocabulary + fatigue bounds**: a `grant_modified` executes the approver's input under a recomputed digest without telling the agent it ran unmodified; a denial's rationale reaches the loop; and a batch / plan pre-authorization collapses N same-class invocations into one decision while refusing any invocation outside its enumerated member set in `backend-go/tests/integration/approval_policy_test.go` (FR-107, FR-109, SC-027)
- [ ] T061e [P] [US3] Integration test — **elicitation**: an input request suspends the run at zero ongoing token cost, validates the answer against its schema, resolves on expiry as declared (recorded default assumption, or `input_expired` termination returning the partial artifact), and can never satisfy a high-impact gate in `backend-go/tests/integration/input_request_test.go` (FR-110, FR-004, SC-028)
- [ ] T061f [P] [US3] Integration test — **the gate holds adversarially**: injected content attempting to suppress or simulate consent, a mid-run autonomy-widening attempt from every path (model output, tool result, steering message, hook, delegation parameter), and a standing scope / batch / pre-authorization attempting to skip the per-invocation safety check or the Rule of Two are all refused and audited; these cases are held-out graders the agent cannot edit in `backend-go/tests/integration/approval_redteam_test.go` (FR-111, FR-112, SC-005)
- [ ] T062 [P] [US3] Integration test: Rule of Two blocks the third leg of the lethal trifecta unattended, decides from declared per-tool taint metadata, fails closed when a tool's declaration is missing, and clears taint only across an audited sanitization boundary in `backend-go/tests/integration/rule_of_two_test.go` (FR-033, FR-087)
- [ ] T063 [P] [US3] Integration test: built-in web fetch/search is egress domain-allowlisted (blocked domain denied) and returned content is treated as untrusted under the Rule of Two in `backend-go/tests/integration/web_tool_test.go` (FR-058)
- [ ] T064 [P] [US3] Unit test: secret handle never appears in prompt/transcript in `backend-go/internal/security/secrets_test.go` (FR-034)
- [ ] T064a [P] [US3] Integration test: the egress sanitizer strips leaked `<tool_call>`/`<think>` fragments and stutter, and the credential scrubber redacts secret-shaped tokens from model/tool output before delivery in `backend-go/tests/integration/output_sanitizer_test.go` (FR-068)
- [ ] T064b [P] [US3] Integration test: the input guard flags/blocks prompt-injection patterns (instruction-override, role-reassignment, delimiter escape) per mode and fails closed on a high-severity match in `backend-go/tests/integration/input_guard_test.go` (FR-069)
- [ ] T064c [P] [US3] Integration test: an MCP server runs isolated, its content is treated as untrusted under the Rule of Two and never executed as inline shell, and it gets no host/cross-tenant/non-allowlisted network access in `backend-go/tests/integration/mcp_isolation_test.go` (FR-070)
- [ ] T064d [P] [US3] Integration test: a tool/connector/MCP descriptor whose name/description/schema carries an injected instruction is refused catalog admission (`catalog_scan_status = rejected`, never enumerable to the model) both at first registration and on a version bump; a clean descriptor is admitted with its scan result and policy version recorded on the governance sign-off in `backend-go/tests/integration/catalog_scan_test.go` (FR-113, FR-096)
- [ ] T065 [P] [US3] Integration test: first-time sign-in JIT-provisions a `User`, and invalid/expired/wrong-issuer tokens are rejected in `backend-go/tests/integration/auth_provisioning_test.go` (FR-029, FR-035)

### Implementation for User Story 3

- [ ] T066 [P] [US3] Implement the secrets vault client injecting credentials at tool-execution time (model sees a handle), per-tenant isolated in `backend-go/internal/security/secrets.go` (FR-034)
- [ ] T067 [P] [US3] Implement delegated-identity (act-as calling user) enforcement at the tool boundary in `backend-go/internal/security/identity.go` (FR-035)
- [ ] T068 [P] [US3] Implement layered fail-closed defense (channel allowlist, autonomy mode, workspace restriction, shell allow/blocklist) in `backend-go/internal/security/defense.go` (FR-032)
- [ ] T068a [P] [US3] Implement the egress output sanitizer (strip leaked `<tool_call>`/`<think>` markup, echoed system framing, duplicated stutter) + credential scrubber (redact secret-shaped tokens) applied to all model/tool output before delivery or persistence in `backend-go/internal/security/sanitize.go` (FR-068)
- [ ] T069 [P] [US3] Implement the Rule of Two evaluator driven by **declared per-tool taint metadata** (`returns_untrusted` / `reads_private_data` / `mutates_external`) combined with the session's accumulated `taint_state`, failing closed when a declaration is absent or unclassifiable, and appending a `taint_transition` event on every change, in `backend-go/internal/security/rule_of_two.go` (FR-033, FR-087)
- [ ] T069b [P] [US3] Implement the **sanitization boundary** — a bounded, audited operation that reduces session taint by isolating untrusted content behind a summarizing sub-agent firewall or an operator-scoped re-baseline, recorded as a `sanitization_boundary` event — so a long session does not become permanently tainted and degrade the control into approval fatigue, in `backend-go/internal/security/sanitize_boundary.go` (FR-087, FR-079)
- [ ] T069a [P] [US3] Implement the inbound-message input guard (injection/jailbreak pattern screening with off/log/warn/block modes, fail-closed on high-severity match) in `backend-go/internal/security/input_guard.go` (FR-069)
- [ ] T070 [P] [US3] Implement egress allowlist + by-class PII/PHI/secret redaction before leaving the trust boundary in `backend-go/internal/security/egress.go` (FR-037)
- [ ] T071 [P] [US3] Implement built-in web search + web fetch tools (egress-allowlisted via T070, crawl4ai as the fetch/crawl backend returning clean chunked markdown, untrusted-content handling under the Rule of Two, high-signal capped/paginated results, oversized bodies offloaded) in `backend-go/internal/tools/builtin/web.go` (FR-058, FR-037, FR-033)
- [ ] T072 [P] [US3] Wire mutating tool execution to the chained audit writer from T026b (receipt per mutating action binding user+tenant+tool+arg/result digests+timestamp into the per-session hash chain) in `backend-go/internal/audit/receipt.go` (FR-040, FR-081)
- [ ] T073 [US3] Implement scoped human approval for high-impact actions: the run **suspends durably at zero token cost** while pending; a TTL expiry denies **the action** and returns a typed synthetic `tool_result` so the agent may replan, terminating the run `approval_expired` (with its best partial artifact) only when it cannot proceed; every outcome appended as a typed approval event; scopes bounded by tool + effect class + expiry with no unbounded `permanent`, in `backend-go/internal/security/approval.go` (FR-036, FR-067, FR-085)
- [ ] T073a [US3] Implement **approval–argument binding**: derive one canonical digest over `tool_id` + fully resolved input at pipeline step 8a, bind it to the `Approval`, derive the FR-071 idempotency key from that same digest, and re-verify it at step 9a — refusing divergence with a typed `approval_mismatch` synthetic result and never silently re-requesting approval in the same turn. Validate at tool registration that every mutating tool's `approval_binding.digest_fields` is a superset of `idempotency_key_spec.fields`, in `backend-go/internal/security/approval_binding.go` (FR-103, FR-071)
- [ ] T073b [US3] Implement the **approval context package** — action summary, `blast_radius_fields`, taint legs, cost + delegation chain, requester — produced by the tenant's versioned redaction policy and stamped with `redaction_policy_version`; honor `approval_context_mode = local | upstream`, refusing `upstream` where residency config forbids it and serving in-boundary rendering under `local`; reject any request that would present identifiers alone, in `backend-go/internal/security/approval_context.go` (FR-104, FR-091, FR-037)
- [ ] T073c [US3] Implement **approver authorization**: require a human principal holding `approve:<effect_class>`; reject agent/service principals; enforce separation of duties (resolver ≠ run initiator) and `required_approvals ≥ 2` on tenant-configured multi-party classes; demand step-up re-authentication for `step_up_classes`; mint and verify the **single-use resolution token** bound to `(approval_id, resolver)` and invalid after first use or TTL; append `approval_resolution_refused` and audit every refused attempt, in `backend-go/internal/security/approval_authz.go` (FR-105, FR-082)
- [ ] T073d [US3] Implement **approval invalidation**: on cancel, any terminal reason, delegation reap, ceiling breach, and steering-into-suspension, invalidate every outstanding `Approval` and `InputRequest` for the session **before** the terminal event is appended, release a paired synthetic `tool_result` per gated `tool_use`, and record any later decision as a refused resolution, in `backend-go/internal/security/approval_invalidate.go` (FR-106, FR-003, FR-100)
- [ ] T073e [US3] Implement **decision vocabulary**: `grant` / `grant_modified` (approver input becomes authoritative, digest recomputed, modification evented, agent not told it ran unmodified) / `deny` with structured rationale returned to the loop in the synthetic `tool_result`, in `backend-go/internal/security/approval_decision.go` (FR-107)
- [ ] T073f [US3] Implement **approver routing and escalation**: resolve `assignee_ref` from tenant config (user / group / rotation — never an implicit broadcast), deliver idempotently to the tenant's channels, emit `approval_notified` / `approval_reminded` / `approval_escalated` on the configured offsets, and fall back to expiry-as-denial when no channel is reachable, in `backend-go/internal/security/approval_routing.go` (FR-108, FR-028, FR-051)
- [ ] T073g [US3] Implement the versioned **`ApprovalPolicy`** (effect class × risk tier × value threshold × autonomy level → `auto` / `once` / `session` / `multi_party`), evaluated deterministically with the resolved `risk_tier` and policy version recorded on the `Approval`; plus **batched approval** and **plan pre-authorization** admitting an invocation only on an enumerated, unexpired, digest-bound member match. Gate policy enablement on `eval_run_id` + `governance_signoff`, and expose no model-facing parameter that selects or relaxes a policy, in `backend-go/internal/security/approval_policy.go` (FR-109, FR-042, FR-043, FR-096)
- [ ] T073h [US3] Implement the **`Oversight.requestInput`** elicitation primitive: schema-declared agent→human question sharing the durable-suspend machinery, response validated against `answer_schema` and passed through the input guard, `on_expiry = assume_default` resolving with a **recorded** assumption or `terminate` ending the run `input_expired` with its partial artifact; typed `input_requested` / `input_answered` / `input_expired` / `input_invalidated` events; structurally incapable of satisfying an FR-036 gate, in `backend-go/internal/security/elicitation.go` (FR-110, FR-004, FR-067, FR-069)
- [ ] T073i [US3] Implement **autonomy semantics and the total permission resolution order**: `read_only` refuses mutating capabilities, `supervised` forces an ask on every mutating invocation, `full` defers to effect class + Rule of Two + `ApprovalPolicy`; pin autonomy per run and make it ratchet-only (tighten via `RunControl.tightenAutonomy`, widenable by no path); implement the published resolution table with its two invariants — a deny at any layer is final, and steps 6–7 (per-invocation safety, Rule of Two) are evaluated unconditionally and can never be short-circuited by a standing scope, batch, pre-authorization, or autonomy level, in `backend-go/internal/security/resolution_order.go` (FR-111, FR-009, FR-033, FR-087)
- [ ] T073j [US3] Emit a chained **authorization receipt** (`AuditReceipt.kind = authorization`) on every grant / modification / denial, binding `approval_id`, approver identity, `resolved_authn_method`, `resolved_channel`, `approved_input_digest`, scope + expiry, and the decision into the same per-session hash chain as action receipts, in `backend-go/internal/audit/authorization_receipt.go` (FR-112, FR-081)
- [ ] T074 [US3] Implement the approval endpoints — `POST /v1/approvals` (carrying `approved_input_digest`, `kind`/`member_digests`, context package + mode, assignee + escalation chain, `required_approvals`/`separation_of_duties`/`step_up_required`, bounded scope `once` | `session` | `standing` with `scope_tool_id`, `scope_effect_class`, `scope_expires_at`; refusing `missing_input_digest`, `unenumerated_batch`, `context_mode_forbidden`, `policy_violation`), `POST /v1/approvals/{id}/resolve` (grant / grant_modified / deny with its typed error set), and `POST /v1/approvals/invalidate` — in `backend-go/cmd/surface-gateway/approvals.go` (FR-036, FR-103–FR-109)
- [ ] T074b [US3] Implement the `POST /v1/input-requests` and `POST /v1/input-requests/{id}/resolve` endpoints (schema-validated answer, `400 default_required`, `422 schema_violation`) in `backend-go/cmd/surface-gateway/input_requests.go` (FR-110)
- [ ] T074a [P] [US3] Integration test: an expired approval denies only the action and lets the run replan to completion, while a run that cannot proceed without it terminates `approval_expired` returning its partial artifact; and a `standing` scope without an expiry is rejected in `backend-go/tests/integration/approval_semantics_test.go` (FR-036, FR-067)
- [ ] T075 [US3] Implement the control-plane authN + RBAC authorization gate that composes the OIDC/provisioning primitives below (rejects unauthenticated/out-of-scope requests before a run is queued) in `backend-go/cmd/control-plane/auth.go` (FR-029, FR-035)
- [ ] T076 [US3] Implement OIDC token validation middleware (OIDC discovery + per-tenant JWKS fetch/cache from `Tenant.identity_config`, signature + `iss`/`aud`/`exp` claim verification; standards-only, no provider-specific SDK) in `backend-go/cmd/control-plane/oidc.go` (FR-029)
- [ ] T077 [US3] Implement JIT user provisioning with a per-tenant claims mapping (resolve `external_subject` and roles/groups from the configured claim names — default `sub`/`roles`, overridable per provider; upsert `User` by `(tenant_id, external_subject)` on first valid sign-in; resolve roles → permission scopes via `Tenant.rbac_map`) in `backend-go/internal/tenancy/provision.go` (FR-035)
- [ ] T078 [US3] Implement the tenant identity-config admin API (register/rotate the OIDC issuer + client credentials and the subject/roles claims mapping in `Tenant.identity_config` during onboarding) in `backend-go/cmd/control-plane/identity_admin.go` (FR-029)
- [ ] T079 [P] [US3] Implement service/machine identity for the CLI & cron surfaces (OIDC client-credentials service tokens carrying a delegated, least-privilege scope) in `backend-go/internal/security/service_identity.go` (FR-035)
- [ ] T080 [P] [US3] Implement the web-surface OAuth2/PKCE login + redirect callback + session handling in `frontend/src/services/auth.ts` and `frontend/src/pages/Login.tsx` (FR-029)
- [ ] T081 [US3] Implement the per-tenant permission-scoped connector catalog (MCP) in `backend-go/internal/tools/connectors.go` (FR-012)
- [ ] T081a [US3] Enforce the MCP isolation boundary (MCP servers as isolated untrusted processes reached only via the catalog; MCP content untrusted under the Rule of Two, never executed as inline shell; no host/cross-tenant/non-allowlisted egress) in `backend-go/internal/tools/mcp_isolation.go` (FR-070, FR-012, FR-033; extends T081)
- [ ] T081b [US3] Implement the catalog-admission injection scanner: scan every tool/connector/MCP descriptor's name, description, parameter docs, and schema for injected instructions before `catalog_scan_status` moves `pending → clean` (enumerable) or `pending → flagged/rejected` (never enumerable, fail closed); re-run on every version bump; record the scan result and `scan_policy_version` against the tool's T146c governance sign-off, in `backend-go/internal/tools/catalog_scan.go` (FR-113, FR-075, FR-096; extends T081)

**Checkpoint**: Enterprise trust surface enforced at the data layer; US1–US2 still work.

---

## Phase 6: User Story 4 - Govern cost and observe behavior (Priority: P2)

**Goal**: Per-turn token/cost metering attributed to task+tenant with hard per-task and
per-tenant ceilings, a content-free trace that joins to the event log, audited content
access, quality-per-dollar
reporting, and an eval gate that blocks any prompt/tool/model/skill change in CI.

**Independent Test**: Run a workload; confirm per-turn metering, per-tenant ceiling
enforcement + alert, a content-free trace that survives a suspended and a killed run, an
audited content-access grant, and a CI gate that blocks a change failing
the eval set (quickstart.md Scenario 4).

### Tests for User Story 4 ⚠️

- [ ] T082 [P] [US4] Integration test: per-tenant ceiling breach stops further runs with cost-exhausted + alert in `backend-go/tests/integration/tenant_budget_test.go` (FR-017)
- [ ] T083 [P] [US4] Integration test: trace view exposes structure/cost/latency/token spans without conversation content in `backend-go/tests/integration/trace_structure_test.go` (FR-040)
- [ ] T083a [P] [US4] Integration test — **content access is a transaction**: a read without a grant, after expiry, by an agent/service principal, or by a requester who is also the sole authorizer on cross-tenant scope is refused and audited; a permitted read emits chained receipts for the grant *and* for each read, naming reader, authorizer, purpose, and sessions read in `backend-go/tests/integration/content_access_test.go` (FR-118, SC-034)
- [ ] T083b [P] [US4] Integration test — **long and killed runs still trace**: a run suspended past its SLO window and a run whose worker is killed mid-turn both produce complete turn-scoped traces, and the latency SLI computed on active time is unaffected by the suspension in `backend-go/tests/integration/trace_longrun_test.go` (FR-120, SC-035)
- [ ] T083c [P] [US4] Integration test — **metric cardinality is bounded**: no metric carries a session, user, task, request, or tool-argument identifier as a label; a run is reachable from a metric only via an exemplar in `backend-go/tests/integration/metric_cardinality_test.go` (FR-122)
- [ ] T083d [P] [US4] Integration test — **cost survives control-plane loss**: with the upstream unavailable, zero metered turns are lost; records queue in the outbox, apply exactly once on recovery, and every reservation reaches a terminal state (reconciled or expired-and-reported) in `backend-go/tests/integration/cost_outbox_test.go` (FR-124, SC-039)
- [ ] T084 [P] [US4] Eval-gate test: a regressing prompt/model change is blocked (≥90% pass AND zero regressions) in `ml-python/tests/test_eval_gate.py` (FR-043)
- [ ] T084a [P] [US4] Integration test: feature-demand routing sends a sub-agent-spawning request to an at-or-above-floor model and a feature-light grounded-QA request to the cheaper tier, with above-floor features disabled on the fast tier in `backend-go/tests/integration/feature_routing_test.go` (FR-076, FR-077)
- [ ] T084b [P] [US4] Integration test: the front-of-model response cache serves a repeat/near-duplicate request without a model call, never serves a cross-tenant hit, and is bypassable per request in `backend-go/tests/integration/response_cache_test.go` (FR-072)
- [ ] T084c [P] [US4] Integration test: the release gate report includes quality-per-dollar (η$) and completions-per-million-token metrics alongside quality (a change cannot pass the gate without them present) in `ml-python/tests/test_release_gate_metrics.py` (FR-018)

### Implementation for User Story 4

- [ ] T085 [P] [US4] Implement per-tenant budget enforcement (rolling sums vs `Budget`) + alert on breach in `backend-go/internal/cost/budget.go` (FR-017)
- [ ] T085a [P] [US4] Implement the optional front-of-model response cache (exact + semantic vector match, similarity-threshold + TTL, tenant-scoped, non-state-dependent only, per-request bypass) in `backend-go/internal/cost/response_cache.go` (FR-072)
- [ ] T086 [P] [US4] Implement deterministic two-axis model routing (data-label + difficulty, auditable, regulated → self-hosted) in `backend-go/internal/provider/routing.go` (FR-037)
- [ ] T086a [P] [US4] Extend routing to capability-floor-aware feature-demand selection (route by the orchestration features a request will exercise — sub-agents/playbooks/heavy MCP → at-or-above-floor model — composed with data-label routing) in `backend-go/internal/provider/routing.go` (FR-076; extends T086)
- [ ] T086b [P] [US4] Implement per-model-tier harness feature profiles (scope the exposed tool catalog and disable above-floor features like delegation below a model's reliability floor; config-driven, no kernel fork) in `backend-go/internal/provider/feature_tiers.go` (FR-077)
- [ ] T087 [P] [US4] Implement quality-per-dollar (η$) and completions-per-million-token reporting in `backend-go/internal/cost/report.go` (FR-018)
- [ ] T088 [US4] Implement the **log-derived span emitter**: turn-scoped traces (one trace per turn / plan step) linked to their predecessor, carrying `session.id`, `root_session_id`, `depth`, tenant, harness digest, the covered `seq` range, and the kernel's classification + terminal reason; spans are produced from durable events rather than held open in process, so a killed worker or a six-hour suspension still yields complete traces, and `telemetry_sink_mode = local` is the same path with a different sink, in `backend-go/internal/observability/trace.go` (FR-119, FR-120; content-free per T024a)
- [ ] T088a [P] [US4] Implement the **active/suspended duration split** and make every latency SLI compute on active time (durable suspension is measured on the FR-095 human-oversight axis instead) in `backend-go/internal/observability/duration.go` (FR-120)
- [ ] T088b [US4] Implement the **content-access grant** lifecycle — request → authorize (authorizer ≠ requester for cross-tenant scope; agent/service principals refused) → bounded-expiry enforcement at the decryption boundary → chained receipt on grant and on **every read** → typed refusal events — in `backend-go/internal/security/content_access.go` and `backend-go/internal/audit/chain.go` (FR-118). This is the *only* path to plaintext; there is no telemetry flag alternative (T024a)
- [ ] T088c [P] [US4] Implement **metric label discipline**: a fixed enumerated label set (tenant, model, surface, terminal_reason, execution_class), per-deployment optional dimensions, and exemplars carrying a trace reference so a metric reaches an individual run without a high-cardinality label, in `backend-go/internal/observability/metrics.go` (FR-122)
- [ ] T088d [US4] Implement the **cost outbox**: append the cost record in the same transaction as the turn, ship to the control plane at-least-once with idempotent application on `(session_id, turn_seq, reservation_id)`, sweep unshipped records, and report a reservation that expires without reconciliation rather than silently releasing it, in `backend-go/internal/cost/outbox.go` (FR-124)
- [ ] T089 [US4] Implement the control-plane rate limits + budget checks + routing front door in `backend-go/cmd/control-plane/gateway.go` (FR-029)
- [ ] T090 [P] [US4] Grow the eval set from production **through the governed export path of T090a** (never by reading traces, which are content-free by construction) and add funnel metrics — task success, cost/task, latency, tool-error rate, human-escalation rate — to `ml-python/src/evals/runner.py` (FR-043, FR-125) *(the runner itself is Foundational: T026e)*
- [ ] T090a [P] [US4] Implement the **production→eval export transaction**: per-tenant recorded consent, extraction inside the tenant boundary under a content-access grant (T088b), redaction by declared class, recorded governance sign-off, provenance stamped on each exported case, and revocation that removes a tenant's derived cases when consent is withdrawn or erasure is exercised, in `backend-go/internal/security/eval_export.go` and `ml-python/src/evals/corpus.py` (FR-125, FR-118, FR-080)
- [ ] T091 [P] [US4] Extend the judge with calibration against human labels and spec-gaming detection (trace review for test edits, credential access, skip markers) in `ml-python/src/judge/rubric.py` (FR-043) *(base scorer is Foundational: T026f)*
- [ ] T092 [US4] Extend the CI gate with the release-report artifact (pass rate, regressions, η$, CPM) in `ml-python/src/evals/gate.py` (FR-042, FR-043, FR-018) *(the blocking gate itself is Foundational: T026g)*
- [ ] T092b [P] [US4] Implement showback/chargeback export — cost aggregated per tenant and, within a tenant, per user, agent, and surface over a billable period, reconciling to the sum of the underlying cost records and naming the price-book version — in `backend-go/internal/cost/chargeback.go` (FR-093)
- [ ] T092c [P] [US4] Integration test: a chargeback export reconciles exactly to the sum of per-turn cost records across all dimensions, and a price-book version change leaves historical figures unchanged in `backend-go/tests/integration/chargeback_test.go` (FR-093, FR-084)
- [ ] T092a [P] [US4] Implement model + connector/MCP version pinning with eval-gated adoption, supply-chain scope vetting, and revert-on-regression (a snapshot/version bump is an eval-gated deploy) in `backend-go/internal/provider/pinning.go` and `ml-python/src/evals/version_gate.py` (FR-078, FR-042, FR-043)
- [ ] T092d [P] [US4] Extend supply-chain vetting to require an audience-/resource-restricted token (RFC 8707 resource indicator, or the provider's narrowest equivalent scope) for every `tenant_service`-auth connector/MCP server, storing it as `Connector.token_audience`; a provider that cannot support audience restriction is rejected at the same gate as an over-broad scope rather than registered with a tenant-wide credential, in `backend-go/internal/provider/pinning.go` (FR-114, FR-078; extends T092a)

**Checkpoint**: Cost governance, content-free observability with audited content access, durable cost accounting, and the CI eval gate are live; US1–US3 still work.

---

## Phase 7: User Story 5 - Grow capability through memory and skills (Priority: P3)

**Goal**: File-first per-tenant memory injected immutably at session start (screened
first, retention-bounded), progressive-disclosure skills, and agent-proposed skills that
are never auto-promoted (propose → human/eval gate → version → promote) — plus the
bounded delegation sub-contract: descent invariant, bounds + fan-out envelope, return
validation, lifecycle, and chain attribution.

**Independent Test**: Seed memory + a skill; confirm session-start injection, on-demand
skill loading, and that an agent-proposed skill is not auto-promoted (quickstart.md Scenario 5).
Separately (quickstart.md Scenario 5b): delegate to a child and confirm it cannot widen
scope, that its bounds and cost envelope hold, that its summary stays untrusted, and that
cancelling the parent reaps it.

### Tests for User Story 5 ⚠️

- [ ] T093 [P] [US5] Integration test: memory injected at session start (not mid-session), tenant-scoped, screened first in `backend-go/tests/integration/memory_injection_test.go` (FR-019)
- [ ] T094 [P] [US5] Integration test: agent-proposed skill requires human+eval gate, never auto-promoted in `backend-go/tests/integration/skill_promotion_test.go` (FR-021)
- [ ] T094a [P] [US5] Integration test: a sub-agent runs read-only in an isolated context and returns only a capped distilled summary, its token spend is metered to the parent, and single-thread remains the default in `backend-go/tests/integration/subagent_firewall_test.go` (FR-079)
- [ ] T094c [P] [US5] Integration test — **descent invariant**: a child cannot obtain any tool, connector, egress entry, data label, region, or approval scope its parent lacks; every escalation attempt (including a crafted delegation payload) fails closed; and the child's `scope_snapshot` is provably a subset of the parent's at spawn time in `backend-go/tests/integration/delegation_descent_test.go` (FR-098, SC-021)
- [ ] T094d [P] [US5] Integration test — **bounds and envelope**: depth, per-parent concurrency, and per-run child totals are never exceeded; a bound breach returns a **non-retryable** synthetic result and the loop routes around it rather than retrying; a fan-out never overspends its pre-reserved envelope and never starves a concurrent sibling session in the same tenant in `backend-go/tests/integration/delegation_bounds_test.go` (FR-099, FR-083, SC-021)
- [ ] T094e [P] [US5] Integration test — **return contract**: a returned summary over its cap is truncated by the platform (not trusted to comply), fails closed on schema violation, is rejected when it misses its declared acceptance criterion, and **never clears the untrusted-content taint leg** even when the child was read-only; a child that engaged a private-data leg folds that leg into the parent in `backend-go/tests/integration/delegation_return_test.go` (FR-100, FR-087, FR-044, SC-022)
- [ ] T094f [P] [US5] Integration test — **lifecycle**: cancelling, terminating, or budget-exhausting a parent reaps every child (zero children still spending afterward), each outstanding delegation receives a synthetic paired result, and a worker crash mid-fan-out resumes from checkpoint rather than restarting the tree in `backend-go/tests/integration/delegation_lifecycle_test.go` (FR-100, FR-003, FR-024, SC-021)
- [ ] T094g [P] [US5] Integration test — **chain attribution**: every cost record, receipt, and span in a nested tree resolves to root + parent + depth; total run cost reconciles to `SUM(...) WHERE root_session_id = :root`; and a receipt's full authorization chain is reconstructable without cross-record correlation in `backend-go/tests/integration/delegation_chain_test.go` (FR-101, FR-093, SC-022)
- [ ] T094b [P] [US5] Integration test: the retrieval tier injects only the top-K reranked chunks and ingestion rejects an unauthenticated/anomalous document while retrieved content stays tagged untrusted in `backend-go/tests/integration/retrieval_precision_test.go` (FR-074, FR-075)

### Implementation for User Story 5

- [ ] T095 [P] [US5] Implement file-first memory load (immutable snapshot at session start) + append (takes effect next session) in `backend-go/internal/memory/store.go` (FR-019)
- [ ] T095a [P] [US5] Implement retrieval reranking + top-K (default ~2–3) injection for the FR-022 retrieval tier (configurable K and rerank strategy) in `backend-go/internal/memory/rerank.go` (FR-074, FR-022)
- [ ] T096 [P] [US5] Implement injection/exfiltration screening + per-tenant retention enforcement (default 90-day, overridable) in `backend-go/internal/memory/screen.go` (FR-019)
- [ ] T096a [P] [US5] Implement access-controlled, provenance-tracked corpus ingestion with a poisoning/backdoor anomaly scan before indexing (no open ingestion path; retrieved content stays untrusted) in `backend-go/internal/memory/ingest.go` (FR-075, FR-022)
- [ ] T097 [P] [US5] Implement progressive-disclosure skills (brief description always visible, body on demand) in `backend-go/internal/skills/registry.go` (FR-020)
- [ ] T098 [US5] Implement the skill promotion pipeline (propose → human/eval gate → version → promote; never auto) in `backend-go/internal/skills/promote.go` (FR-021)
- [ ] T099 [P] [US5] Implement the off-loop structured context condenser/summarizer helper (cheaper model, keep recent + verbatim requirements) in `ml-python/src/condenser/compact.py` (FR-015)
- [ ] T099a [P] [US5] Implement sub-agent delegation as isolated read-only context firewalls (own clean context, bounded ~1–2k-token distilled summary return, parent keeps sole decision authority, per-sub-agent token metering attributed to the parent, capability-floor gated; single-thread by default) in `backend-go/internal/context/subagent.go` (FR-079, FR-076, FR-016)
- [ ] T099b [US5] Implement the **descent resolver**: compute a child's scope as a proven subset of the parent's live scope (tools, connectors, egress allowlist, data label, region), reject any delegation whose requested scope is not a subset, refuse approval-scope inheritance, and snapshot the resolved scope onto the `Delegation` row — fail closed on any unprovable case — in `backend-go/internal/security/delegation_scope.go` (FR-098, FR-035)
- [ ] T099c [US5] Implement **delegation bounds + fan-out cost envelope**: config-driven per-tier/per-tenant depth (default 1), concurrent children per parent (default 3), and children per run (default 16); parent reserves the aggregate worst case against the atomic counter before the first child starts and children draw from that envelope (never reserving independently against the tenant ceiling); admission also fails closed on sandbox cap and region pin; a bound breach emits a typed **non-retryable** result in `backend-go/internal/context/delegation_bounds.go` and `backend-go/internal/cost/fanout_envelope.go` (FR-099, FR-083, FR-047, FR-091)
- [ ] T099d [US5] Implement the **return validator**: platform-side truncation to the summary cap, return-schema validation, acceptance-criterion check with a bounded revision round, typed rejection outcomes, and the taint fold that keeps `returns_untrusted` set on every returned summary in `backend-go/internal/context/delegation_return.go` (FR-100, FR-087, FR-044)
- [ ] T099e [US5] Implement **child lifecycle**: reap on parent terminal/cancel/ceiling breach, synthetic paired `tool_result` for every outstanding delegation, and child progress checkpointed under the parent run so a crash mid-fan-out resumes rather than restarts in `backend-go/internal/context/delegation_lifecycle.go`, wired into `RunControl.cancel` (FR-100, FR-003, FR-024, FR-005)
- [ ] T099f [P] [US5] Propagate the **delegation chain** (`root_session_id`, `parent_session_id`, `depth`, `delegation_path`) through session creation, cost records, audit receipts, and OTel spans; add the tree roll-up query backing ceilings and chargeback in `backend-go/internal/observability/delegation_spans.go` and `backend-go/internal/cost/rollup.go` (FR-101, FR-093, FR-040)
- [ ] T099g [US5] Amend the **sanitization boundary** to the constrained rule — a summarizing/sub-agent firewall may reduce volume and may clear the private-data leg only when the child provably held no private-data capability and no private content entered its context; it MUST NOT clear the untrusted leg, which only an attributable operator re-baseline clears — in `backend-go/internal/security/sanitize_boundary.go` (FR-087, FR-098; **amends T069b**)
- [ ] T100 [US5] Wire context compaction into the kernel (two-zone prompt, byte-stable prefix) at a **configurable trigger well below the hard limit** — a compaction requested at the edge of the window produces a degraded summary — in `backend-go/internal/context/compaction.go` (FR-013, FR-014, FR-015, FR-130d)
- [ ] T100b [US5] Implement the **compaction cache boundary**: a `condensation` event invalidates and re-establishes the cache-stable prefix in a defined order, with a regression test asserting a pre-compaction prefix is never served into a post-compaction turn (a shipped bug class in a comparable product), and cache-read measured across the boundary, in `backend-go/internal/context/compaction.go` and `backend-go/internal/observability/cache_metrics.go` (FR-130c, FR-013, FR-014)
- [ ] T100c [P] [US5] Implement **compaction chain accounting**: count successive compactions per run, expose the depth as a golden signal, bound it, and record the condenser config version on every `condensation` event so a degraded run is attributable to the condenser that produced it, in `backend-go/internal/context/compaction_chain.go` (FR-130b, FR-095)
- [ ] T100d [P] [US5] Add **probe-based compaction-fidelity eval cases** — scored on artifact trail (file paths, identifiers, prior tool outcomes survive), causal-chain preservation, continuity of an in-progress task, and verbatim retention of the user's original requirements — and gate the condenser prompt/template as versioned config under the CI gate; compression ratio is explicitly not accepted as a quality measure, in `ml-python/src/evals/compaction_probes.py` and `ml-python/src/condenser/compact.py` (FR-130a, FR-042, FR-043, SC-038)
- [ ] T100e [P] [US5] Integration test — **compaction chain endurance**: a session driven past N successive compactions still resolves its original requirements and its most recently modified artifacts, and a mid-compaction failure loses nothing because the pre-compaction history remains in the log, in `backend-go/tests/integration/compaction_chain_test.go` (FR-130, FR-006, SC-038)
- [ ] T100a [P] [US5] Implement output-token slot reservation (bounded default `max_tokens`, escalate on truncation/`max_output_tokens` signal via a bounded retry, no silent truncation) in `backend-go/internal/context/token_budget.go` (FR-063)

**Checkpoint**: Memory + skills compound capability; delegation is bounded, attributable, and cannot escalate scope or launder taint; US1–US4 still work.

---

## Phase 8: User Story 6 - Fit any organization by configuration, not forks (Priority: P3)

**Goal**: Onboard a new org via config + connectors only (zero kernel changes) and
deploy the same build as multi-tenant SaaS, single-tenant, self-hosted/BYOC, or hybrid
by configuration, with the data plane movable into a customer VPC.

**Independent Test**: Onboard a new org with config only and deploy the same build in ≥2
topologies (SaaS + self-hosted) by configuration (quickstart.md Scenario 6).

### Tests for User Story 6 ⚠️

- [ ] T101 [P] [US6] Integration test: new org onboarded via config/connectors with zero kernel changes in `backend-go/tests/integration/onboard_config_test.go` (FR-050)
- [ ] T102 [P] [US6] Integration test: same build handshakes in split control/data-plane topology in `backend-go/tests/integration/topology_split_test.go` (FR-030)

### Implementation for User Story 6

- [ ] T103 [P] [US6] Implement org onboarding from config (tenant settings, agent def, seeded skills, enabled surfaces, connectors) in `backend-go/internal/tenancy/onboard.go` (FR-050)
- [ ] T104 [P] [US6] Implement bootstrap-markdown agent definition loader (persona + toolset profile + autonomy) read at runtime in `backend-go/internal/tenancy/bootstrap.go` (FR-050)
- [ ] T105 [P] [US6] Author the signed OCI image set + Helm chart in `deploy/helm/` (control-plane, runtime-worker, surface-gateway) (FR-030)
- [ ] T106 [P] [US6] Author the Terraform module + BYOC KEDA/HPA autoscale-on-queue-depth policy in `deploy/terraform/` (FR-030, FR-046)
- [ ] T107 [US6] Implement deployment-topology configuration (SaaS/single-tenant/BYOC/hybrid) selecting sandbox isolation + data-plane placement in `backend-go/internal/tenancy/topology.go` (FR-050)
- [ ] T107a [P] [US6] Implement **region pinning enforced at admission** — a run whose placement (worker, sandbox, event log, memory, model route) would fall outside the tenant's pinned region is refused with `region_conflict`, never relocated — in `backend-go/cmd/control-plane/placement.go` (FR-091)
- [ ] T107b [P] [US6] Integration test: a region-pinned tenant's run is refused rather than executed out of region, and in a customer-boundary deployment only the enumerated structure-only fields cross the boundary (with `audit_sink_mode=local` emitting nothing) in `backend-go/tests/integration/residency_test.go` (FR-091, SC-004)

**Checkpoint**: Config-not-forks onboarding + multi-topology deploy work; US1–US5 still work.

---

## Phase 9: User Story 7 - Survive failures, deploys, and scale (Priority: P3)

**Goal**: Classify-before-retry (backoff+jitter, circuit-break at 3 identical failures,
no silent retries), durable checkpoint/resume, stuck detection, rainbow deploys, warm
sandbox pool, and admission control / fair scheduling / load-shedding under overload.

**Independent Test**: Crash a worker mid-run and confirm resume-from-checkpoint; deploy
during an active run without cutting it over; drive concurrency past capacity and confirm
graceful degradation (quickstart.md Scenario 7).

### Tests for User Story 7 ⚠️

- [ ] T108 [P] [US7] Integration test: worker crash mid-run resumes from last checkpoint, preserving partial work in `backend-go/tests/integration/resume_test.go` (FR-024)
- [ ] T108a [P] [US7] Integration test: a fatal error path resolves to a degraded success returning the best partial artifact from the last checkpoint with the correct typed reason (never a bare crash) in `backend-go/tests/integration/autosubmit_test.go` (FR-067)
- [ ] T108b [P] [US7] Integration test — **artifacts are not interchangeable**: a resume driven only from a `condensation` fails the test suite (it cannot restore the in-flight claim, held reservation, sandbox handle, or pending approval), while a resume from a `Checkpoint` restores all of them; deleting every `Snapshot` changes hydration time and nothing else in `backend-go/tests/integration/state_artifacts_test.go` (FR-126)
- [ ] T108c [P] [US7] Integration test — **hydration is bounded**: worker pickup on a session with a very long event history replays only `head_seq − snapshot.at_seq`, meeting the stated bound regardless of run length; and rehydrate-to-next-model-call after an approval resolution is measured as an SLI in `backend-go/tests/integration/hydration_bound_test.go` (FR-126, FR-095)
- [ ] T108d [P] [US7] Integration test — **fork**: a completed run forks at a chosen seq with a patched prompt, re-executes with external effects disabled, leaves the source run's cost records, approvals, and audit chain byte-identical, inherits no approvals, and reports a harness-digest divergence rather than presenting a different configuration's result as a reproduction in `backend-go/tests/integration/fork_test.go` (FR-128, FR-129, SC-037)
- [ ] T109 [P] [US7] Integration test: identical failing call circuit-breaks within three attempts with logged reasons (no silent retries) in `backend-go/tests/integration/circuit_breaker_test.go` (FR-023)
- [ ] T109a [P] [US7] Integration test: a stalled model stream is aborted by the idle watchdog and retried once non-streaming without stalling the run in `backend-go/tests/integration/idle_watchdog_test.go` (FR-066)
- [ ] T110 [P] [US7] Integration test: deploy during an active run does not cut it over mid-task in `backend-go/tests/integration/rainbow_deploy_test.go` (FR-026)
- [ ] T111 [P] [US7] Integration test: overload triggers admission control / fair scheduling / load-shedding (429 + `Retry-After`) in `backend-go/tests/integration/overload_test.go` (FR-049)
- [ ] T111a [P] [US7] Integration test: the stuck-detection heuristic clears its negative-case eval set (retrying a fix with small variations, polling a long job, iterating a search query — none of which trip a terminate) with zero regressions; separately, a genuinely oscillating run logs a `stuck_suspected` event on its first heuristic trip without terminating and only hard-terminates on a corroborating second trip in `backend-go/tests/integration/stuck_detection_test.go` (FR-025, FR-115)
- [ ] T112 [P] [US7] Integration test: runaway code (infinite loop / fork bomb / memory blow-up) is killed by the sandbox CPU/memory/PID/wall-clock caps with a typed reclaim reason, and a sandbox egress attempt to a non-allowlisted domain is denied in `backend-go/tests/integration/sandbox_limits_test.go` (FR-059, FR-047, FR-037)

### Implementation for User Story 7

- [ ] T113 [P] [US7] Implement the typed failure classifier + backoff-with-jitter retry policy in `backend-go/internal/reliability/classify.go` (FR-023)
- [ ] T114 [P] [US7] Implement the circuit breaker (break after 3 identical failing calls, logged reasons) in `backend-go/internal/reliability/breaker.go` (FR-023)
- [ ] T115 [P] [US7] Implement durable checkpointing + resume-from-last-checkpoint (Postgres event log + WAL) in `backend-go/internal/reliability/checkpoint.go` (FR-024) — the checkpoint carries the **machine-facing** resume set (covered `seq`, open `in_flight` claim, held reservation, sandbox handle, pending approval digest, in-flight provider request id, open delegations, harness digest), written at effect boundaries rather than on a timer, and is never satisfied by a condensation (FR-126)
- [ ] T115b [P] [US7] Implement the `Snapshot` projection cache (`at_seq`, `projection_version`, encrypted state) and bounded `hydrate` (snapshot + tail replay), with a projection-version bump invalidating every snapshot rather than serving a stale shape, in `backend-go/internal/reliability/snapshot.go` (FR-126, FR-086)
- [ ] T115c [US7] Implement `replay` (pure — no model call, no tool execution, no external effect, no append; the mechanism the FR-086 upcasting path is verified against) and `fork` (new `session_id` from `at_seq` with declared overrides, `forked` event on the source run, external effects disabled or scratch-sandbox confined, no inherited approvals, own budget and audit chain, harness-digest divergence reported) in `backend-go/kernel/replay.go` and `backend-go/kernel/fork.go` (FR-128, FR-129, FR-106)
- [ ] T115d [P] [US7] Implement the **harness digest** — computed over stable system-prompt version, resolved tool catalog, skill versions, safety/permission policy version, and approval-policy version; pinned at run start, held for the run's life, stamped on session/checkpoint/cost record/span, and used as the cache-prefix identity so cache-read is attributed to a known configuration — in `backend-go/internal/context/harness_digest.go` (FR-129, FR-013, FR-088)
- [ ] T115a [P] [US7] Implement autosubmit / degraded-success on fatal & terminal error paths (return the best partial artifact from the last checkpoint with the correct typed reason instead of crashing) in `backend-go/internal/reliability/autosubmit.go` (FR-067, FR-024, FR-004; depends on T115)
- [ ] T116 [P] [US7] Implement stuck detection (repeated actions / oscillation / zero net change over K steps) breaking the loop with a clear reason in `backend-go/internal/reliability/stuck.go` (FR-025)
- [ ] T116a [US7] Harden stuck detection into a two-stage, eval-gated control: gate `K` and the "zero net change" predicate behind the same eval-gate discipline as a prompt/model change (T026g), requiring zero regressions against a negative-case set of near-oscillation sequences; on a first heuristic trip, append a `stuck_suspected` event (counted in the T146 golden signals) and keep running rather than terminating; escalate to a hard terminate only on a corroborating second trip or an explicit hard ceiling, in `backend-go/internal/reliability/stuck.go` (FR-115, FR-042, FR-043, FR-095; extends T116)
- [ ] T117 [P] [US7] Implement provider retry → cooldown → failover across backends in `backend-go/internal/provider/failover.go` (FR-027)
- [ ] T117a [P] [US7] Implement the streaming idle watchdog + non-streaming fallback (abort a no-progress stream after a bounded interval, retry once non-streaming, disabled while speculative/streaming tool execution is active) in `backend-go/internal/provider/watchdog.go` (FR-066)
- [ ] T118 [P] [US7] Implement the warm sandbox pool (hard TTLs, reclamation on terminal/stuck, per-tenant caps, isolation by topology, E2B default backend) in `backend-go/internal/sandbox/pool.go` (FR-047)
- [ ] T119 [P] [US7] Implement per-sandbox resource-limit enforcement (CPU/memory/PID/wall-clock caps → terminate + reclaim with a typed reason) and network default-deny (egress only via the FR-037 domain allowlist) with E2B as the default backend and Docker/microVM (Firecracker/gVisor)/local-OS isolation as swappable fallbacks in `backend-go/internal/sandbox/limits.go` (FR-059, FR-047, FR-037)
- [ ] T120 [P] [US7] Implement per-tenant rate limiting + connection pooling + cached-prefix handling in `backend-go/internal/queue/ratelimit.go` (FR-048)
- [ ] T120a [P] [US7] Integration test: a throttled provider (mocked 429) is transparently retried/rerouted at the provider-abstraction layer without a surface-level error; per-tenant rate-limit counter increments correctly and a ceiling breach stops further provider calls for that tenant (isolated from gateway admission control) in `backend-go/tests/integration/provider_ratelimit_test.go` (FR-048)
- [ ] T121 [US7] Implement admission control + weighted-fair scheduling + priority load-shedding + graceful degradation at the gateway in `backend-go/cmd/control-plane/admission.go` (FR-049)
- [ ] T122 [US7] Implement rainbow (rolling) deploy support keeping in-flight runs alive in `backend-go/internal/queue/deploy.go` and `deploy/helm/` (FR-026)
- [ ] T123 [US7] Implement autoscale-on-queue-depth/age worker signals in `backend-go/cmd/runtime-worker/scale.go` (FR-046)

**Checkpoint**: Operational resilience + horizontal scale in place; all user stories work.

---

## Phase 10: User Story 8 - Connect personal messaging surfaces and systems of record (Priority: P2)

**Goal**: Reach the same kernel from consumer messaging apps (Telegram, Zalo) as thin
webhook adapters, and let each user authorize personal connectors (Gmail, Google Drive,
Google Calendar) via per-user OAuth (auth-code + PKCE) with tokens vaulted per
`(tenant, user, connector)`, auto-refreshed and revocable — the model sees only a handle,
connector content is untrusted, the Rule of Two applies, and high-impact sends are
approval-gated. No kernel fork per surface or per connector.

**Independent Test**: Message the agent from Telegram and Zalo and confirm identical
control flow/terminal reason to the API surface; complete a per-user OAuth consent and
confirm the token is vaulted per `(tenant, user, connector)`, auto-refreshed, and
revocable; confirm the model only sees a handle; confirm a "send email" blocks pending
approval; confirm an unverified external chat identity runs zero actions (quickstart.md
Scenario 8).

### Tests for User Story 8 ⚠️ (write first, ensure they FAIL)

- [ ] T123a [P] [US8] Integration test: a forged (bad signature), a replayed (stale timestamp/nonce), and a flooding webhook delivery are each rejected **before** any adapter translation — zero kernel invocations, zero token spend — while a correctly signed delivery proceeds; likewise an OAuth callback with a reused or unbound `state`, or an unregistered redirect URI, is refused, in `backend-go/tests/integration/webhook_auth_test.go` (FR-082, SC-019)
- [ ] T124 [P] [US8] Integration test: a Telegram webhook message routes to the kernel with identical control flow + terminal reason to the API surface in `backend-go/tests/integration/telegram_surface_test.go` (FR-051)
- [ ] T125 [P] [US8] Integration test: a Zalo webhook message routes to the kernel with identical control flow + terminal reason in `backend-go/tests/integration/zalo_surface_test.go` (FR-051)
- [ ] T126 [P] [US8] Integration test: per-user OAuth auth-code+PKCE consent vaults tokens per `(tenant, user, connector)`, auto-refreshes on expiry, and revoke removes access in `backend-go/tests/integration/connector_oauth_test.go` (FR-052)
- [ ] T126a [P] [US8] Integration test: a vaulted per-user connector token is minted audience-/resource-restricted (`ConnectorAuthorization.resource_audience` set, never a tenant- or IdP-wide credential); a captured token cannot be replayed to call a different resource server, and a connector whose provider cannot support audience restriction is refused at registration in `backend-go/tests/integration/connector_token_audience_test.go` (FR-114)
- [ ] T127 [P] [US8] Unit test: connector token never appears in prompt/transcript/log (handle only) in `backend-go/internal/connectors/token_test.go` (FR-052, FR-054)
- [ ] T128 [P] [US8] Integration test: high-impact connector action (Gmail send) blocks pending scoped approval and is constrained by the Rule of Two in `backend-go/tests/integration/connector_approval_test.go` (FR-054)
- [ ] T129 [P] [US8] Integration test: an inbound Telegram/Zalo identity must be verified-linked to a `User` before any action; unlinked identity is denied in `backend-go/tests/integration/surface_identity_test.go` (FR-055)

### Implementation for User Story 8

- [ ] T129a [US8] Implement the **inbound webhook authenticator** applied to every unsolicited ingress path (Telegram secret-token / Zalo HMAC / inbound email / connector callbacks) *before* adapter translation: provider signature or shared-secret verification, replay rejection outside a bounded timestamp/nonce window, per-external-identity flood limiting, and fail-closed on any check — plus single-use `state` bound to the initiating user session and a pre-registered redirect-URI allowlist for OAuth callbacks — in `backend-go/internal/surfaces/webhook_auth.go` (FR-082)
- [ ] T130 [US8] Author the migration for `ConnectorAuthorization` (per `(tenant, user, connector)` OAuth tokens/scopes/expiry, plus `resource_audience` per FR-114) and `SurfaceIdentity` (external chat id → `User`) tables with `tenant_id` row-level-security policies in `backend-go/migrations/0004_connectors.sql` (FR-052, FR-055, FR-114)
- [ ] T131 [P] [US8] Implement the per-user OAuth connector authorization service (auth-code + PKCE, `state`/nonce, token exchange) in `backend-go/internal/connectors/oauth.go` (FR-052)
- [ ] T131a [P] [US8] Extend the OAuth exchange to request an audience-/resource-restricted token (RFC 8707 resource indicator, or the provider's narrowest equivalent scope) and persist it to `ConnectorAuthorization.resource_audience`; refuse registration of a connector whose provider cannot support audience restriction rather than falling back to a broader grant, in `backend-go/internal/connectors/oauth.go` (FR-114; extends T131)
- [ ] T132 [P] [US8] Implement connector token vault storage + auto-refresh + revoke, keyed per `(tenant, user, connector)`, injected at execution time (model sees a handle) in `backend-go/internal/connectors/vault.go` (FR-052, FR-054)
- [ ] T133 [US8] Implement the connector authorization REST endpoints (`POST /v1/connectors/{name}/authorize`, `GET /v1/connectors/callback`, `GET /v1/connectors`, `DELETE /v1/connectors/{name}`) in `backend-go/cmd/surface-gateway/connectors.go` (FR-052)
- [ ] T134 [P] [US8] Implement the Telegram surface adapter (webhook ingress, update parsing, send, stream/poll progress) in `backend-go/internal/surfaces/telegram.go` (FR-051, FR-031)
- [ ] T135 [P] [US8] Implement the Zalo surface adapter (webhook ingress, OA message parsing, send) in `backend-go/internal/surfaces/zalo.go` (FR-051)
- [ ] T136 [US8] Implement verified surface-identity binding/linking (map + verify external chat id → `User`; deny unlinked, fail-closed) in `backend-go/internal/surfaces/identity.go` (FR-055)
- [ ] T137 [P] [US8] Implement the Gmail reference connector tool (consolidated `gmail_search`/`gmail_read`/`gmail_send`, high-signal outputs, delegated scope) in `backend-go/internal/connectors/gmail.go` (FR-053, FR-054)
- [ ] T138 [P] [US8] Implement the Google Drive reference connector tool (consolidated `drive_search`/`drive_read`/`drive_list`) in `backend-go/internal/connectors/drive.go` (FR-053, FR-054)
- [ ] T139 [P] [US8] Implement the Google Calendar reference connector tool (consolidated `schedule_event` that finds availability and books) in `backend-go/internal/connectors/calendar.go` (FR-053, FR-054)
- [ ] T140 [P] [US8] Implement the Notion reference connector tool (consolidated `notion_search`/`notion_read`/`notion_create`, per-user OAuth, delegated scope, high-impact writes approval-gated) in `backend-go/internal/connectors/notion.go` (FR-053, FR-054)
- [ ] T141 [US8] Register the reference connectors (Gmail/Drive/Calendar/Notion) in the per-tenant catalog with capability metadata, Rule-of-Two enforcement, and approval-gated high-impact sends (extends T081) in `backend-go/internal/tools/connectors.go` (FR-053, FR-054)
- [ ] T142 [P] [US8] Implement the frontend connector-management page (connect/disconnect + OAuth consent redirect + linked-account list) in `frontend/src/pages/Connectors.tsx` and `frontend/src/services/connectors.ts` (FR-052)

**Checkpoint**: Consumer messaging surfaces + per-user personal connectors work as
config-only additions; US1–US7 still work.

---

## Phase 11: User Story 9 - Run a repeatable multi-step process deterministically (Priority: P2)

**Goal**: Encode a recurring business process as a versioned, eval-gated **orchestration
plan** — steps, conditions, bounded loops, approval gates, optional read-only fan-out —
whose control flow the platform evaluates at **zero model-token cost**. The model works
inside a step; it never decides the route between steps. See
[contracts/orchestration-plane.md](contracts/orchestration-plane.md).

**Independent Test**: Author a plan with a conditional branch, a bounded loop, and an
approval gate; run it twice against the deterministic provider and confirm identical
control flow, zero model tokens on transitions, full replay naming the branch taken, and
resume-from-checkpoint after an interrupted step (quickstart.md Scenario 9).

**Independence note**: plans using only `agent` / `condition` / `loop` / `approval_gate`
steps depend on **no** delegation machinery — only a `delegate_fanout` step requires US5
(T099b–T099f). Build and ship this phase without US5 if sequencing demands it; T164 is
the only task that carries that dependency.

### Tests for User Story 9 ⚠️ (write first, ensure they FAIL)

- [ ] T159 [P] [US9] Integration test — **zero-token routing**: a plan with a conditional branch and a bounded loop executes twice on the same inputs, takes the identical path, and makes **zero `Provider.stream` calls to evaluate any transition** (asserted against the deterministic provider, not sampled) in `backend-go/tests/integration/plan_zero_token_routing_test.go` (FR-102, FR-097, SC-023)
- [ ] T160 [P] [US9] Integration test — **validation fails closed**: a plan with an unreachable step, an unbounded loop, a predicate that is not a closed expression, or a step requesting a capability the plan principal lacks is rejected at validation and can never run in `backend-go/tests/integration/plan_validation_test.go` (FR-102, FR-098)
- [ ] T161 [P] [US9] Integration test — **lifecycle gates**: a `draft` plan cannot be enabled without an eval-gate run and a recorded governance sign-off; an `enabled` version is immutable and an edit publishes a new version; an in-flight run finishes on the version and pinned agent/model route it started with in `backend-go/tests/integration/plan_lifecycle_test.go` (FR-102, FR-042, FR-043, FR-096, FR-088, FR-026)
- [ ] T162 [P] [US9] Integration test — **replay + resume**: a completed plan run reconstructs every step entry, the predicate that matched at each transition, each step outcome, and the terminal reason from the event log alone; an interrupted run resumes at the last completed step and its cost envelope reconciles rather than double-reserving in `backend-go/tests/integration/plan_replay_resume_test.go` (FR-102, FR-085, FR-024, FR-083)
- [ ] T163 [P] [US9] Integration test — **approval gate**: reaching an `approval_gate` step suspends the run durably at **zero ongoing token cost**, resumes on the approval event, and an unanswered approval expires as a denial of that step in `backend-go/tests/integration/plan_approval_gate_test.go` (FR-102, FR-036)

### Implementation for User Story 9

- [ ] T164 [P] [US9] Author the plan schema + `ORCHESTRATION_PLAN` migration (`plan_id`/`version` immutable per version, `status`, `steps`, `pinned_routes`, `cost_envelope_usd`, `eval_run_id`, `governance_signoff`), tenant-scoped under the same RLS policy as every other tenant-owned row, in `backend-go/migrations/0004_plans.sql` (FR-102, FR-011, FR-039)
- [ ] T165 [US9] Implement the **plan validator**: schema, reachability (every step reachable and `end` reachable from every step), bounded loops, closed/total predicate expressions (no model call, no I/O, no free-text evaluation), and a per-step scope-subset proof — all failing closed before a plan can run — in `backend-go/internal/orchestration/validate.go` (FR-102, FR-098)
- [ ] T166 [US9] Implement the **zero-token transition evaluator**: the closed predicate expression language over typed step outputs, terminal reasons, and run metadata, plus step dispatch — with an assertion/guard that no `Provider.stream` call can occur inside a transition, so zero-token routing is a property rather than a claim — in `backend-go/internal/orchestration/evaluator.go` (FR-102, SC-023)
- [ ] T167 [US9] Implement the plan **runner**: step checkpointing at every boundary (FR-024), the envelope reservation drawn before step 1 (FR-083, FR-099), `approval_gate` suspension at zero token cost (FR-036), bounded classified retry on `on_error` (FR-023), and the typed `plan_started` / `plan_step_entered` / `plan_transition` / `plan_step_exited` / `plan_completed` event set (FR-085) in `backend-go/internal/orchestration/runner.go` (FR-102)
- [ ] T168 [US9] Implement the plan **lifecycle gates**: `draft → gated → enabled → retired`, agent-version and model-route pinning at enable (FR-088), immutability of an enabled version, and refusal to enable without both `eval_run_id` and `governance_signoff` in `backend-go/internal/orchestration/lifecycle.go`, wired to the eval gate (T026g) and the governance record (T146c) (FR-102, FR-042, FR-043, FR-096)
- [ ] T169 [P] [US9] Wire `delegate_fanout` steps to the `Delegation` seam so a plan step's children obey the descent invariant, bounds, envelope draw, and return contract unchanged, with decision authority remaining in the plan in `backend-go/internal/orchestration/fanout.go` (FR-102, FR-098–FR-101) — **the only task in this phase depending on US5 (T099b–T099f)**
- [ ] T170 [P] [US9] Expose plan submission/status on the run API and the control-plane ↔ data-plane contract (submit a plan run, stream `plan_*` events as structure-only, query step state) in `backend-go/internal/surfaces/api.go` and `specs/001-agent-platform/contracts/run-api.openapi.yaml` (FR-031, FR-030, FR-102)

**Checkpoint**: A recurring process is a versioned, reviewed, eval-gated artifact whose
routing costs zero tokens, replays exactly, and resumes from checkpoint; US1–US8 still work.

---

## Phase 12: Polish & Cross-Cutting Concerns

**Purpose**: Hardening, docs, and the go-live gate spanning all stories

- [ ] T143 [P] Implement the go-live checklist assertion (`make go-live-check`) covering audit, vaulted secrets, sandboxing+approval, trifecta, cost ceilings, reliability, evals-green, cache-read, residency/retention, runbook in `backend-go/cmd/control-plane/golive.go` (FR-045); the check MUST assert that the incident runbook (`docs/runbook.md`), the residency/retention policy doc (`docs/data-policy.md`), and the operating-model doc (`docs/operating-model.md`) exist and are non-empty, and MUST additionally assert: audit-chain verification green with a current anchor (FR-081), isolation verified **through the pooler** (FR-039, SC-013), pre-spend budget reservation active (FR-083), content encrypted at rest with an exercised erasure path (FR-080, FR-089), a restore drill within the last quarter meeting RPO/RTO (FR-090), SBOM + signed artifacts published (FR-092), and error-budget alerting wired to the runbook (FR-095)
- [ ] T144 [P] Add the cache-read steady-state measurement + >90% assertion to observability in `backend-go/internal/observability/cache_metrics.go` (FR-014)
- [ ] T144a [P] Integration test: an oversized tool output is offloaded to object storage and returned as an in-context preview carrying the "do not infer success from the preview" caveat (referenced by path from the event log) in `backend-go/tests/integration/output_offload_test.go` (FR-010)
- [ ] T145 [P] Implement oversized tool-output offload to object storage with in-context preview + "do not infer success" caveat in `backend-go/internal/tools/offload.go` (FR-010)
- [ ] T146 [P] Add SLO measurement + **error-budget policy** + burn-rate alerting (≥99.9% control plane / ≥99.5% run completion; p95 queue-wait, first-token — all latency SLIs computed on **active time**, never wall-clock including durable suspension, FR-120), each alert naming the runbook section it pages to, covering the agent golden signals — queue wait and oldest-message age, completion rate by terminal reason, cost-ceiling breach rate, approval-expiry rate, stuck-detection rate, cache-read rate, sandbox reclamation rate, provider throttle/failover rate — plus the observability and state-integrity signals: content-access grant/read/refusal rate (FR-118), unresolved `in_flight` claims (FR-127), compaction chain depth and cache-read across a compaction boundary (FR-130), session hydration and suspended-run rehydration latency (FR-126), cost-outbox backlog and reservations expiring without reconciliation (FR-124), and telemetry attribute-allowlist drop rate (FR-117) — in `backend-go/internal/observability/sla.go` (FR-095, SC-008, SC-011)
- [ ] T146a [P] Implement backup coverage + point-in-time restore for the event log, audit chain, config, and vault, and automate the **restore drill** measuring RPO/RTO and asserting that the chain verifies and the log replays post-restore, in `deploy/dr/` and `backend-go/tests/integration/restore_drill_test.go` (FR-090, SC-018)
- [ ] T146b [P] Add supply-chain assurance for the platform's own build: SBOM generation per release, artifact signing with published provenance, and dependency + container vulnerability scanning that fails CI above a defined severity, in `.github/workflows/release.yml` and `deploy/` (FR-092)
- [ ] T146c [P] Author the operating-model and governance record: named ownership for the platform team, AgentOps (SLOs, on-call, evals-in-CI, cost dashboards, behavioral incident response), and the governance/risk function, plus the AI risk register and the sign-off gate a new tool/connector/autonomy increase must clear before a tenant enables it, in `docs/operating-model.md` and `backend-go/internal/tenancy/governance.go` (FR-096)
- [ ] T147 [P] Author quickstart validation `Makefile` targets referenced by quickstart.md (`migrate`, `seed-tenant`, `run`, `evals`, `verify-isolation`, `verify-approval-timeout`, `verify-skill-promotion`, `chaos-crash`, `deploy-during-run`, `load-test`, `capacity-check`, `trace`, `seed-memory`, `onboard-org`, `deploy`, `link-surface`, `connect-connector`, `verify-audit-chain`, `verify-erasure`, `restore-drill`, `delegate-escalation-probe`, `delegate-fanout`, `delegate-cancel-parent`, `plan-validate`, `plan-enable`, `run-plan`, `plan-replay`, `verify-telemetry-content-free`, `verify-trace-join`, `content-access`, `chaos-crash-mid-effect`, `fork`, `go-live-check`) — every target referenced anywhere in quickstart.md must exist
- [ ] T148 [P] Author developer + operator documentation in `docs/` including: architecture overview, deployment topologies, and a rehearsed behavioral-incident runbook at `docs/runbook.md` (covering detection, triage, mitigation, and rollback steps for the five most critical failure modes: kernel loop stall, cost ceiling breach, cross-tenant data leak, approval TTL expiry under load, and provider outage) referenced by the T143 go-live gate (FR-045)
- [ ] T148a [P] Author the data-residency, retention, and no-train policy document at `docs/data-policy.md` (covering per-deployment-tier data residency / region-pinning options, the 90-day default memory retention and tenant-override process, the no-training guarantee, and the DSAR support procedure for GDPR/CCPA-tier tenants) referenced by the T143 go-live gate (FR-045)
- [ ] T149 [P] Add unit-test coverage pass across `backend-go/tests/unit/` for kernel, cost, security, and reliability helpers
- [ ] T150 Run the full quickstart.md scenarios 1–9 (including 5b) end-to-end and confirm all acceptance criteria pass

---

## Phase 13: Scale Validation & Capacity Hardening

**Purpose**: Turn "designed to scale" into "measured to scale". Prove SC-008
(thousands of concurrent long-running sessions, p95 queue-wait < 5s interactive /
< 60s batch, first-token < 2s) under real load, then remove the specific
bottlenecks that surface at thousands of concurrent sessions — the shared event-log
write path, database connection exhaustion, the hot Redis session lock, sandbox-pool
capacity, and external provider rate limits. This phase gates high-concurrency
go-live (extends the T143 go-live gate).

**Independent Test**: Ramp to the target concurrent-session count on a production-like
environment and confirm the SC-008 SLAs hold; sustain that load for a multi-hour soak
with zero resource-leak drift (goroutines / DB connections / memory) and no cascading
collapse.

### Load & soak harness

- [ ] T151 [P] Implement the concurrency load harness that ramps to N concurrent long-running sessions and measures the SC-008 SLAs (p95 queue-wait interactive/batch, first-token, run-completion rate) with a pass/fail assertion in `backend-go/tests/load/concurrency_soak_test.go` and a driver script in `deploy/load/soak.js` (SC-008, FR-046) — wire to the `make load-test` target from T147
- [ ] T152 [P] Implement the endurance soak (sustained target concurrency for hours) asserting zero drift in goroutines, DB/Redis/JetStream connections, and memory, and no p95 degradation over time in `backend-go/tests/load/endurance_test.go` (SC-008)

### Bottleneck hardening

- [ ] T153 Implement the event-log write-scaling strategy — partition the Postgres event log (by `tenant_id`/time), batch/pipeline appends, and split the hot append path from analytical reads — in `backend-go/migrations/0005_eventlog_partitioning.sql` and `backend-go/internal/queue/eventlog.go` (FR-006, SC-008)
- [ ] T154 [P] Define and enforce the database connection budget — per-worker `pgx` pool sizing tied to worker concurrency, plus a PgBouncer (transaction-pooling) tier in front of Postgres — in `backend-go/internal/tenancy/config.go` and `deploy/helm/` (SC-008, FR-048). **Must compose with T020**: transaction pooling requires transaction-local tenant scope (`SET LOCAL`) and a `pgx` protocol/statement-cache mode compatible with it; the T029 isolation test runs through this tier and is the gate on this task (FR-039, SC-013)
- [ ] T155 [P] Harden the Redis session-key lock for the hot path (extends T022) — bounded TTL with fencing-token renewal, defined mid-run Redis-failover behavior (fail-closed, re-queue), and thundering-herd/contention backoff — in `backend-go/internal/queue/sessionlock.go` (FR-041, FR-046)
- [ ] T156 [P] Implement the sandbox-pool sizing model + demand-driven pool autoscaler (warm-headroom target vs. concurrent-run demand, per-tenant caps, reclamation under pressure) so pool capacity — not queue throughput — is a known, tunable ceiling in `backend-go/internal/sandbox/pool.go` and `deploy/helm/` (FR-047, SC-008)
- [ ] T157 [P] Implement per-provider rate-limit accounting (TPM/RPM quota pooling per account/region) with 429/`Retry-After` backpressure that feeds the gateway admission controller (T121) instead of failing runs, in `backend-go/internal/provider/quota.go` (FR-027, FR-049)

### Capacity gate

- [ ] T158 Add the capacity/SLA validation gate (`make capacity-check`) that runs the load + soak harness, captures the measured SC-008 numbers into a capacity report, and blocks high-concurrency go-live on a regression, extending the go-live checklist in `backend-go/cmd/control-plane/golive.go` (SC-008, FR-045)

**Checkpoint**: The SC-008 concurrency SLAs are measured (not just asserted), the
known bottlenecks (event-log writes, DB connections, session lock, sandbox pool,
provider quotas) are hardened, and high-concurrency go-live is gated on real numbers.

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup — BLOCKS all user stories. Note that
  the eval runner/judge/CI gate (T026e–T026g) now live here rather than in US4, so
  no behavior-bearing slice ships unmeasured; the remaining US4 eval tasks only
  extend them.
- **User Stories (Phases 3–11)**: All depend on Foundational
  - US1 (P1) is the MVP and should land first
  - US2–US4 (P2) build on US1; US5–US7 (P3) build on the P2 slices; US8 (P2) builds on US2 + US3; US9 (P2) builds on US1 + US3 + US4
  - Stories are independently testable and can be parallelized across teams after Foundational
- **Polish (Phase 12)**: Depends on all targeted user stories
- **Scale Validation & Capacity Hardening (Phase 13)**: Depends on US7 (reliability/scale primitives) being in place; runs against a production-like build. Gates high-concurrency go-live.

### User Story Dependencies

- **US1 (P1)**: Foundational only — standalone MVP
- **US2 (P2)**: Foundational + US1 loop (surfaces translate to the same run model)
- **US3 (P2)**: Foundational + US1 (wraps the loop with trust/isolation)
- **US4 (P2)**: Foundational + US1 (meters/observes the loop); eval gate is independent
- **US5 (P3)**: Foundational + US1 (memory/skills feed the loop's context)
- **US6 (P3)**: Foundational + US3 (onboarding relies on tenancy/connectors)
- **US7 (P3)**: Foundational + US1 (reliability wraps the run lifecycle)
- **US8 (P2)**: Foundational + US2 (new thin surface adapters) + US3 (connector catalog, vaulted secrets, delegated identity, Rule of Two, approval) — adds surfaces/connectors as config, no kernel fork
- **US9 (P2)**: Foundational + US1 (steps run the loop) + US3 (approval gates) + US4 (cost envelope, eval gate, governance sign-off). **Not dependent on US5**: plans using only `agent` / `condition` / `loop` / `approval_gate` steps need no delegation machinery; only the `delegate_fanout` step (T169) requires US5's delegation seam, so the story ships and tests standalone with fan-out as an additive capability

### Within Each User Story

- Tests are written first and must FAIL before implementation
- Types/models → interfaces → services → endpoints → surface wiring
- Story complete and independently testable before moving to the next priority

---

## Parallel Execution Examples

### Foundational (Phase 2)

```bash
# Interface seams are disjoint files — run together:
Task: "Declare Provider interface in backend-go/internal/provider/provider.go"
Task: "Declare Tool interface in backend-go/internal/tools/tool.go"
Task: "Declare Memory interface in backend-go/internal/memory/memory.go"
Task: "Declare Workspace/Sandbox interface in backend-go/internal/sandbox/workspace.go"
Task: "Declare Surface interface in backend-go/internal/surfaces/surface.go"
```

### User Story 1 (Phase 3)

```bash
# Tests first, in parallel:
Task: "Contract test for run API in backend-go/tests/contract/run_api_test.go"
Task: "Integration test tool pairing in backend-go/tests/integration/loop_pairing_test.go"
Task: "Integration test cost ceiling in backend-go/tests/integration/cost_ceiling_test.go"
Task: "Unit test classifier in backend-go/kernel/classify_test.go"

# Then independent implementation pieces:
Task: "Implement classifier in backend-go/kernel/classify.go"
Task: "Implement terminal resolver in backend-go/kernel/terminal.go"
Task: "Implement Provider adapter in backend-go/internal/provider/anthropic.go"
Task: "Implement cost meter in backend-go/internal/cost/meter.go"
```

### User Story 9 (Phase 11)

```bash
# Tests first, in parallel:
Task: "Zero-token routing test in backend-go/tests/integration/plan_zero_token_routing_test.go"
Task: "Plan validation fail-closed test in backend-go/tests/integration/plan_validation_test.go"
Task: "Plan lifecycle gate test in backend-go/tests/integration/plan_lifecycle_test.go"
Task: "Plan replay + resume test in backend-go/tests/integration/plan_replay_resume_test.go"
Task: "Plan approval-gate test in backend-go/tests/integration/plan_approval_gate_test.go"

# Then: schema (T164) → validator (T165) → evaluator (T166) → runner (T167) → lifecycle (T168),
# which are sequential; these two are parallel with the tail of that chain:
Task: "Wire delegate_fanout to the Delegation seam in backend-go/internal/orchestration/fanout.go"
Task: "Expose plan submission/status on the run API in backend-go/internal/surfaces/api.go"
```

### User Story 8 (Phase 10)

```bash
# Tests first, in parallel:
Task: "Telegram surface test in backend-go/tests/integration/telegram_surface_test.go"
Task: "Zalo surface test in backend-go/tests/integration/zalo_surface_test.go"
Task: "Connector OAuth vault/refresh/revoke test in backend-go/tests/integration/connector_oauth_test.go"

# Then independent implementation pieces (disjoint files):
Task: "Implement OAuth authorization service in backend-go/internal/connectors/oauth.go"
Task: "Implement Telegram adapter in backend-go/internal/surfaces/telegram.go"
Task: "Implement Zalo adapter in backend-go/internal/surfaces/zalo.go"
Task: "Implement Gmail connector in backend-go/internal/connectors/gmail.go"
Task: "Implement Drive connector in backend-go/internal/connectors/drive.go"
Task: "Implement Calendar connector in backend-go/internal/connectors/calendar.go"
Task: "Implement Notion connector in backend-go/internal/connectors/notion.go"
```

---

## Implementation Strategy

### MVP First (User Story 1)

Aligned with the **MVP cut line** in [plan.md](plan.md) — seams and schema early,
infrastructure late.

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL — blocks all stories). This includes
   the eval gate, the deterministic provider, transaction-local RLS proven through
   the pooler, the audit chain, encryption/erasure, and the pre-spend budget gate
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: run quickstart.md Scenario 1 independently
5. Deploy/demo the reliable cost-bounded loop

Increment 1 deliberately runs the loop in-process behind the queue *port*, on a
single-tenant container sandbox, with one surface (REST) — the durable queue,
warm sandbox pool, physical plane split, consumer surfaces, and multi-topology
packaging are deferred per the cut line, and each is additive against the
Foundational schema and contracts.

### Incremental Delivery (aligned to the plan's six phases)

1. Setup + Foundational → foundation ready
2. US1 (kernel) → MVP
3. US2 (surfaces) + US3 (trust) + US4 (cost/observability) → the P2 platform
4. US9 (deterministic orchestration plans) → processes, not just conversations
5. US5 (memory/skills) + US6 (config/deploy) + US7 (reliability/scale) → full platform
6. US8 (consumer surfaces + personal connectors) → the day-to-day-assistant experience
7. Polish + go-live gate → production launch
8. Scale validation + capacity hardening (Phase 13) → measured SC-008 SLAs before high-concurrency go-live

### Parallel Team Strategy

After Foundational completes, staff US1 first, then fan out US2/US3/US4 in parallel;
US9 follows once US3 + US4 land (it needs approval gates and the cost/eval governance,
not delegation); US5/US6/US7 follow once their P2 prerequisites land, and US8 follows
once US2 + US3 land. Each story integrates without breaking earlier stories.

---

## Notes

- [P] tasks touch different files with no incomplete dependencies
- [Story] labels map tasks to spec user stories for traceability
- Every user story is an independently testable increment
- Verify tests FAIL before implementing (TDD where tests are listed)
- Commit after each task or logical group
- The kernel is never forked per customer — all per-org behavior is data/config (FR-050)
