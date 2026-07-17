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

**Organization**: Tasks are grouped by user story (P1–P3) so each story is an
independently testable increment aligned with the six delivery phases in the plan
(kernel → surfaces → trust → cost/observability → memory/skills → config → scale).

## Format: `[ID] [P?] [Story] Description`

- **[P]**: Can run in parallel (different files, no dependencies on incomplete tasks)
- **[Story]**: US1–US7 (maps to spec user stories); Setup/Foundational/Polish carry no story label
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

### Data model & tenant isolation (data-model.md)

- [ ] T010 Author the Postgres migration framework and base schema for immutable config tables (`Tenant`, `User`, `Agent`, `Tool`, `Model`, `Skill`, `Connector`) in `backend-go/migrations/0001_config.sql`
- [ ] T011 Author the append-only runtime-state migration (`Session`, `Event`, `Checkpoint`, `Cost Record`, `Budget`, `Memory`, `Approval`, `Audit Receipt`, `Sandbox`) in `backend-go/migrations/0002_runtime.sql`
- [ ] T012 Add Postgres row-level-security policies keyed on `tenant_id` for every tenant-scoped table (zero rows without tenant context) in `backend-go/migrations/0003_rls.sql` (FR-038, FR-039)
- [ ] T013 [P] Define immutable config domain types (`Tenant`, `User`, `Agent`, `Tool`, `Model`, `Skill`, `Connector`) in `backend-go/internal/tenancy/model.go` and `backend-go/internal/tools/model.go`
- [ ] T014 [P] Define append-only runtime types (`Session`, `Event`, `Checkpoint`, `CostRecord`, `Budget`, `Memory`, `Approval`, `AuditReceipt`, `Sandbox`) with the typed `Event.type` and `TerminalReason` enums in `backend-go/kernel/types.go`

### Kernel/harness interface seams (contracts/kernel-abi.md)

- [ ] T015 [P] Declare the `Provider` interface + normalized `Chunk` stream contract in `backend-go/internal/provider/provider.go` (FR-027)
- [ ] T016 [P] Declare the `Tool` interface (self-describing, per-invocation checks) in `backend-go/internal/tools/tool.go` (FR-007, FR-008, FR-009, FR-011)
- [ ] T017 [P] Declare the `Memory` interface in `backend-go/internal/memory/memory.go` (FR-019)
- [ ] T018 [P] Declare the `Workspace`/`Sandbox` interface in `backend-go/internal/sandbox/workspace.go` (FR-047)
- [ ] T019 [P] Declare the `Surface` adapter interface in `backend-go/internal/surfaces/surface.go` (FR-001, FR-028, FR-031)

### Cross-cutting infrastructure

- [ ] T020 [P] Implement tenant context propagation + RLS session scoping (set `tenant_id` GUC per connection) in `backend-go/internal/tenancy/context.go` (FR-038)
- [ ] T021 [P] Implement the Postgres event-log store (append event, read by session `seq`) in `backend-go/internal/queue/eventlog.go` (FR-006)
- [ ] T022 [P] Implement the durable job queue + session-key router (per-session serial lock via Redis, cross-session concurrent) in `backend-go/internal/queue/queue.go` (FR-041, FR-046)
- [ ] T023 [P] Implement structured error handling + typed failure taxonomy skeleton in `backend-go/internal/reliability/errors.go`
- [ ] T024 [P] Implement OpenTelemetry span bootstrap (structure-only, no content) + logging config in `backend-go/internal/observability/otel.go` (FR-040)
- [ ] T025 [P] Implement runtime configuration loader (tenant/agent/config read at runtime, never forked) in `backend-go/internal/tenancy/config.go` (FR-050)
- [ ] T026 Implement the versioned control-plane ↔ data-plane handshake per contracts/control-data-plane.md in `backend-go/internal/queue/controlplane.go` (FR-030)

### Foundational contract tests

- [ ] T027 [P] Contract test asserting the kernel ABI interfaces compile with ≥1 stub impl each in `backend-go/tests/contract/kernel_abi_test.go`
- [ ] T028 [P] Contract test for the control/data-plane versioned handshake in `backend-go/tests/contract/control_data_plane_test.go`
- [ ] T029 [P] Integration test asserting RLS returns zero cross-tenant rows (testcontainers Postgres) in `backend-go/tests/integration/rls_isolation_test.go` (FR-039)

**Checkpoint**: Foundation ready — data layer + RLS + interface seams + queue exist. User stories can now begin.

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
- [ ] T032 [P] [US1] Integration test: per-task cost ceiling breach terminates with `cost_exhausted` in `backend-go/tests/integration/cost_ceiling_test.go` (FR-017)
- [ ] T033 [P] [US1] Unit test: response classifier returns `TOOL_CALLS`/`CONTENT`/`EMPTY` (no string matching) in `backend-go/kernel/classify_test.go` (FR-002)

### Implementation for User Story 1

- [ ] T034 [P] [US1] Implement the response classifier (typed union over parsed model output) in `backend-go/kernel/classify.go` (FR-002)
- [ ] T035 [P] [US1] Implement the terminal-reason resolver (exhaustive `TerminalReason` enum) in `backend-go/kernel/terminal.go` (FR-004)
- [ ] T036 [US1] Implement the async-generator kernel step loop (observe → think → act, dispatch on classification) in `backend-go/kernel/loop.go` (FR-001, FR-002; depends on T034, T035)
- [ ] T037 [US1] Enforce the `tool_use`→`tool_result` invariant with synthetic results on cancel/error before the next model call in `backend-go/kernel/invariant.go` (FR-003)
- [ ] T038 [P] [US1] Implement a first concrete `Provider` adapter (Anthropic-native or CLI-subprocess fallback) with normalized chunk streaming in `backend-go/internal/provider/anthropic.go` (FR-027)
- [ ] T039 [P] [US1] Implement the tool execution pipeline (validate → permission → execute → result-budget → telemetry) + self-registering registry + `buildTool` factory in `backend-go/internal/tools/registry.go` (FR-007, FR-011)
- [ ] T040 [P] [US1] Implement per-turn token/cost metering attributed to task+tenant and the per-task ceiling check → `cost_exhausted` in `backend-go/internal/cost/meter.go` (FR-016, FR-017)
- [ ] T041 [US1] Implement acceptance-criteria verification (no self-declared success; end-state checks) in `backend-go/kernel/verify.go` (FR-044)
- [ ] T042 [US1] Wire the runtime-worker entrypoint (pull session → run kernel loop → append events) in `backend-go/cmd/runtime-worker/main.go`
- [ ] T043 [US1] Implement the run-submission REST surface (`POST /v1/runs`, `GET /v1/runs/{id}`, `GET /v1/runs/{id}/events` SSE) in `backend-go/cmd/surface-gateway/runs.go` (run-api.openapi.yaml)
- [ ] T044 [US1] Implement mid-run steering input (`POST /v1/runs/{id}/input`) in `backend-go/cmd/surface-gateway/input.go` (FR-005)

**Checkpoint**: User Story 1 is a standalone MVP — a reliable, cost-bounded, typed-terminal agent loop reachable via the REST surface.

---

## Phase 4: User Story 2 - Reach the same agent from many surfaces (Priority: P2)

**Goal**: The same kernel reachable from CLI, chat, web, REST/gRPC, email, and cron —
each a thin adapter translating only I/O, with identical control flow and guarantees,
streaming/polling long runs instead of blocking a connection.

**Independent Test**: Run the same task via ≥3 surfaces (CLI, API, chat) and confirm
identical control flow, safety/cost guarantees, and no per-surface fork (quickstart.md Scenario 2).

### Tests for User Story 2 ⚠️

- [ ] T045 [P] [US2] Integration test: identical control flow + terminal reason across CLI, API, and chat adapters in `backend-go/tests/integration/multi_surface_test.go` (FR-028)
- [ ] T046 [P] [US2] Integration test: long run streams/polls without a blocked connection in `backend-go/tests/integration/streaming_test.go` (FR-031)

### Implementation for User Story 2

- [ ] T047 [US2] Implement the shared surface-gateway dispatch that maps every adapter to one run model in `backend-go/cmd/surface-gateway/main.go` (FR-001, FR-028)
- [ ] T048 [P] [US2] Implement the CLI surface adapter in `backend-go/internal/surfaces/cli.go`
- [ ] T049 [P] [US2] Implement the chat (Slack/Teams) surface adapter in `backend-go/internal/surfaces/chat.go`
- [ ] T050 [P] [US2] Implement the email surface adapter in `backend-go/internal/surfaces/email.go`
- [ ] T051 [P] [US2] Implement the cron/scheduled-trigger surface adapter in `backend-go/internal/surfaces/cron.go`
- [ ] T052 [P] [US2] Implement the gRPC run surface in `backend-go/internal/surfaces/grpc.go` (FR-028)
- [ ] T053 [P] [US2] Implement the React web surface: run submission + SSE/WS event stream + polling in `frontend/src/services/runs.ts` and `frontend/src/pages/Run.tsx` (FR-031)
- [ ] T054 [US2] Implement stream/poll progress emission shared by all surfaces (no blocked connection) in `backend-go/internal/surfaces/emit.go` (FR-031)

**Checkpoint**: The same agent is reachable identically from multiple surfaces; US1 still works.

---

## Phase 5: User Story 3 - Operate safely with enterprise trust (Priority: P2)

**Goal**: Per-tenant isolation at the data layer, attributable immutable audit,
vault-injected secrets (model sees only a handle), delegated identity, the Rule of Two,
and scoped human approval that fails closed on timeout (`approval_expired`).

**Independent Test**: Run two tenants concurrently and prove zero cross-tenant access;
every mutating action has an audit receipt; a high-impact action blocks pending approval
and expires as denial (quickstart.md Scenario 3).

### Tests for User Story 3 ⚠️

- [ ] T055 [P] [US3] Integration test: two-tenant concurrent run, cross-tenant read returns zero rows in `backend-go/tests/integration/tenant_isolation_test.go` (FR-039)
- [ ] T056 [P] [US3] Integration test: every mutating action emits a tamper-evident audit receipt binding user+tenant+tool+args+result+timestamp in `backend-go/tests/integration/audit_receipt_test.go` (FR-040)
- [ ] T057 [P] [US3] Integration test: unanswered approval expires as denial → run ends `approval_expired`, gated action does not proceed in `backend-go/tests/integration/approval_timeout_test.go` (FR-036)
- [ ] T058 [P] [US3] Integration test: Rule of Two blocks the third leg of the lethal trifecta unattended in `backend-go/tests/integration/rule_of_two_test.go` (FR-033)
- [ ] T059 [P] [US3] Unit test: secret handle never appears in prompt/transcript in `backend-go/internal/security/secrets_test.go` (FR-034)

### Implementation for User Story 3

- [ ] T060 [P] [US3] Implement the secrets vault client injecting credentials at tool-execution time (model sees a handle), per-tenant isolated in `backend-go/internal/security/secrets.go` (FR-034)
- [ ] T061 [P] [US3] Implement delegated-identity (act-as calling user) enforcement at the tool boundary in `backend-go/internal/security/identity.go` (FR-035)
- [ ] T062 [P] [US3] Implement layered fail-closed defense (channel allowlist, autonomy mode, workspace restriction, shell allow/blocklist) in `backend-go/internal/security/defense.go` (FR-032)
- [ ] T063 [P] [US3] Implement the Rule of Two evaluator over {untrusted input, private data, external state change} per session in `backend-go/internal/security/rule_of_two.go` (FR-033)
- [ ] T064 [P] [US3] Implement egress allowlist + by-class PII/PHI/secret redaction before leaving the trust boundary in `backend-go/internal/security/egress.go` (FR-037)
- [ ] T065 [P] [US3] Implement the immutable audit log + HMAC tamper-evident tool receipts in `backend-go/internal/audit/receipt.go` (FR-040)
- [ ] T066 [US3] Implement scoped human approval for high-impact actions with a TTL that expires as denial (`approval_expired`) in `backend-go/internal/security/approval.go` (FR-036)
- [ ] T067 [US3] Implement the `POST /v1/approvals/{id}` resolve endpoint (grant/deny + scope) in `backend-go/cmd/surface-gateway/approvals.go` (FR-036)
- [ ] T068 [US3] Implement the control-plane authN (SSO/OIDC) + RBAC authorization gate in `backend-go/cmd/control-plane/auth.go` (FR-029, FR-035)
- [ ] T069 [US3] Implement the per-tenant permission-scoped connector catalog (MCP) in `backend-go/internal/tools/connectors.go` (FR-012)

**Checkpoint**: Enterprise trust surface enforced at the data layer; US1–US2 still work.

---

## Phase 6: User Story 4 - Govern cost and observe behavior (Priority: P2)

**Goal**: Per-turn token/cost metering attributed to task+tenant with hard per-task and
per-tenant ceilings, a structure-only trace (no conversation content), quality-per-dollar
reporting, and an eval gate that blocks any prompt/tool/model/skill change in CI.

**Independent Test**: Run a workload; confirm per-turn metering, per-tenant ceiling
enforcement + alert, a structure-only trace, and a CI gate that blocks a change failing
the eval set (quickstart.md Scenario 4).

### Tests for User Story 4 ⚠️

- [ ] T070 [P] [US4] Integration test: per-tenant ceiling breach stops further runs with cost-exhausted + alert in `backend-go/tests/integration/tenant_budget_test.go` (FR-017)
- [ ] T071 [P] [US4] Integration test: trace view exposes structure/cost/latency/token spans without conversation content in `backend-go/tests/integration/trace_structure_test.go` (FR-040)
- [ ] T072 [P] [US4] Eval-gate test: a regressing prompt/model change is blocked (≥90% pass AND zero regressions) in `ml-python/tests/test_eval_gate.py` (FR-043)

### Implementation for User Story 4

- [ ] T073 [P] [US4] Implement per-tenant budget enforcement (rolling sums vs `Budget`) + alert on breach in `backend-go/internal/cost/budget.go` (FR-017)
- [ ] T074 [P] [US4] Implement deterministic two-axis model routing (data-label + difficulty, auditable, regulated → self-hosted) in `backend-go/internal/provider/routing.go` (FR-037)
- [ ] T075 [P] [US4] Implement quality-per-dollar (η$) and completions-per-million-token reporting in `backend-go/internal/cost/report.go` (FR-018)
- [ ] T076 [P] [US4] Implement structure-only trace spans (decision structure + per-turn cost/latency/token) with content gated behind an authorized debug scope in `backend-go/internal/observability/trace.go` (FR-040)
- [ ] T077 [US4] Implement the control-plane rate limits + budget checks + routing front door in `backend-go/cmd/control-plane/gateway.go` (FR-029)
- [ ] T078 [P] [US4] Implement the eval runner (~20 real cases, end-state checks) in `ml-python/src/evals/runner.py` (FR-043)
- [ ] T079 [P] [US4] Implement the LLM-as-judge rubric scorer + held-out grader protection in `ml-python/src/judge/rubric.py` (FR-043)
- [ ] T080 [US4] Wire the eval gate into CI (≥90% pass AND zero regressions vs baseline) in `.github/workflows/ci.yml` and `ml-python/src/evals/gate.py` (FR-042, FR-043)

**Checkpoint**: Cost governance, structure-only observability, and the CI eval gate are live; US1–US3 still work.

---

## Phase 7: User Story 5 - Grow capability through memory and skills (Priority: P3)

**Goal**: File-first per-tenant memory injected immutably at session start (screened
first, retention-bounded), progressive-disclosure skills, and agent-proposed skills that
are never auto-promoted (propose → human/eval gate → version → promote).

**Independent Test**: Seed memory + a skill; confirm session-start injection, on-demand
skill loading, and that an agent-proposed skill is not auto-promoted (quickstart.md Scenario 5).

### Tests for User Story 5 ⚠️

- [ ] T081 [P] [US5] Integration test: memory injected at session start (not mid-session), tenant-scoped, screened first in `backend-go/tests/integration/memory_injection_test.go` (FR-019)
- [ ] T082 [P] [US5] Integration test: agent-proposed skill requires human+eval gate, never auto-promoted in `backend-go/tests/integration/skill_promotion_test.go` (FR-021)

### Implementation for User Story 5

- [ ] T083 [P] [US5] Implement file-first memory load (immutable snapshot at session start) + append (takes effect next session) in `backend-go/internal/memory/store.go` (FR-019)
- [ ] T084 [P] [US5] Implement injection/exfiltration screening + per-tenant retention enforcement (default 90-day, overridable) in `backend-go/internal/memory/screen.go` (FR-019)
- [ ] T085 [P] [US5] Implement progressive-disclosure skills (brief description always visible, body on demand) in `backend-go/internal/skills/registry.go` (FR-020)
- [ ] T086 [US5] Implement the skill promotion pipeline (propose → human/eval gate → version → promote; never auto) in `backend-go/internal/skills/promote.go` (FR-021)
- [ ] T087 [P] [US5] Implement the off-loop structured context condenser/summarizer helper (cheaper model, keep recent + verbatim requirements) in `ml-python/src/condenser/compact.py` (FR-015)
- [ ] T088 [US5] Wire context compaction at ~80% budget into the kernel (two-zone prompt, byte-stable prefix) in `backend-go/internal/context/compaction.go` (FR-013, FR-014, FR-015)

**Checkpoint**: Memory + skills compound capability; US1–US4 still work.

---

## Phase 8: User Story 6 - Fit any organization by configuration, not forks (Priority: P3)

**Goal**: Onboard a new org via config + connectors only (zero kernel changes) and
deploy the same build as multi-tenant SaaS, single-tenant, self-hosted/BYOC, or hybrid
by configuration, with the data plane movable into a customer VPC.

**Independent Test**: Onboard a new org with config only and deploy the same build in ≥2
topologies (SaaS + self-hosted) by configuration (quickstart.md Scenario 6).

### Tests for User Story 6 ⚠️

- [ ] T089 [P] [US6] Integration test: new org onboarded via config/connectors with zero kernel changes in `backend-go/tests/integration/onboard_config_test.go` (FR-050)
- [ ] T090 [P] [US6] Integration test: same build handshakes in split control/data-plane topology in `backend-go/tests/integration/topology_split_test.go` (FR-030)

### Implementation for User Story 6

- [ ] T091 [P] [US6] Implement org onboarding from config (tenant settings, agent def, seeded skills, enabled surfaces, connectors) in `backend-go/internal/tenancy/onboard.go` (FR-050)
- [ ] T092 [P] [US6] Implement bootstrap-markdown agent definition loader (persona + toolset profile + autonomy) read at runtime in `backend-go/internal/tenancy/bootstrap.go` (FR-050)
- [ ] T093 [P] [US6] Author the signed OCI image set + Helm chart in `deploy/helm/` (control-plane, runtime-worker, surface-gateway) (FR-030)
- [ ] T094 [P] [US6] Author the Terraform module + BYOC KEDA/HPA autoscale-on-queue-depth policy in `deploy/terraform/` (FR-030, FR-046)
- [ ] T095 [US6] Implement deployment-topology configuration (SaaS/single-tenant/BYOC/hybrid) selecting sandbox isolation + data-plane placement in `backend-go/internal/tenancy/topology.go` (FR-050)

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

- [ ] T096 [P] [US7] Integration test: worker crash mid-run resumes from last checkpoint, preserving partial work in `backend-go/tests/integration/resume_test.go` (FR-024)
- [ ] T097 [P] [US7] Integration test: identical failing call circuit-breaks within three attempts with logged reasons (no silent retries) in `backend-go/tests/integration/circuit_breaker_test.go` (FR-023)
- [ ] T098 [P] [US7] Integration test: deploy during an active run does not cut it over mid-task in `backend-go/tests/integration/rainbow_deploy_test.go` (FR-026)
- [ ] T099 [P] [US7] Integration test: overload triggers admission control / fair scheduling / load-shedding (429 + `Retry-After`) in `backend-go/tests/integration/overload_test.go` (FR-049)

### Implementation for User Story 7

- [ ] T100 [P] [US7] Implement the typed failure classifier + backoff-with-jitter retry policy in `backend-go/internal/reliability/classify.go` (FR-023)
- [ ] T101 [P] [US7] Implement the circuit breaker (break after 3 identical failing calls, logged reasons) in `backend-go/internal/reliability/breaker.go` (FR-023)
- [ ] T102 [P] [US7] Implement durable checkpointing + resume-from-last-checkpoint (Postgres event log + WAL) in `backend-go/internal/reliability/checkpoint.go` (FR-024)
- [ ] T103 [P] [US7] Implement stuck detection (repeated actions / oscillation / zero net change over K steps) breaking the loop with a clear reason in `backend-go/internal/reliability/stuck.go` (FR-025)
- [ ] T104 [P] [US7] Implement provider retry → cooldown → failover across backends in `backend-go/internal/provider/failover.go` (FR-027)
- [ ] T105 [P] [US7] Implement the warm sandbox pool (hard TTLs, reclamation on terminal/stuck, per-tenant caps, isolation by topology) in `backend-go/internal/sandbox/pool.go` (FR-047)
- [ ] T106 [P] [US7] Implement per-tenant rate limiting + connection pooling + cached-prefix handling in `backend-go/internal/queue/ratelimit.go` (FR-048)
- [ ] T107 [US7] Implement admission control + weighted-fair scheduling + priority load-shedding + graceful degradation at the gateway in `backend-go/cmd/control-plane/admission.go` (FR-049)
- [ ] T108 [US7] Implement rainbow (rolling) deploy support keeping in-flight runs alive in `backend-go/internal/queue/deploy.go` and `deploy/helm/` (FR-026)
- [ ] T109 [US7] Implement autoscale-on-queue-depth/age worker signals in `backend-go/cmd/runtime-worker/scale.go` (FR-046)

**Checkpoint**: Operational resilience + horizontal scale in place; all user stories work.

---

## Phase 10: Polish & Cross-Cutting Concerns

**Purpose**: Hardening, docs, and the go-live gate spanning all stories

- [ ] T110 [P] Implement the go-live checklist assertion (`make go-live-check`) covering audit, vaulted secrets, sandboxing+approval, trifecta, cost ceilings, reliability, evals-green, cache-read, residency/retention, runbook in `backend-go/cmd/control-plane/golive.go` (FR-045)
- [ ] T111 [P] Add the cache-read steady-state measurement + >90% assertion to observability in `backend-go/internal/observability/cache_metrics.go` (FR-014)
- [ ] T112 [P] Implement oversized tool-output offload to object storage with in-context preview + "do not infer success" caveat in `backend-go/internal/tools/offload.go` (FR-010)
- [ ] T113 [P] Add SLA measurement + alerting (≥99.9% control plane / ≥99.5% run completion; p95 queue-wait, first-token) in `backend-go/internal/observability/sla.go` (SC-008, SC-011)
- [ ] T114 [P] Author quickstart validation `Makefile` targets referenced by quickstart.md (`verify-isolation`, `verify-approval-timeout`, `verify-skill-promotion`, `chaos-crash`, `load-test`, `trace`, `seed-memory`, `onboard-org`, `deploy`)
- [ ] T115 [P] Add developer + operator documentation in `docs/` (architecture, deployment topologies, incident runbook)
- [ ] T116 [P] Add unit-test coverage pass across `backend-go/tests/unit/` for kernel, cost, security, and reliability helpers
- [ ] T117 Run the full quickstart.md scenarios 1–7 end-to-end and confirm all acceptance criteria pass

---

## Dependencies & Execution Order

### Phase Dependencies

- **Setup (Phase 1)**: No dependencies — start immediately
- **Foundational (Phase 2)**: Depends on Setup — BLOCKS all user stories
- **User Stories (Phases 3–9)**: All depend on Foundational
  - US1 (P1) is the MVP and should land first
  - US2–US4 (P2) build on US1; US5–US7 (P3) build on the P2 slices
  - Stories are independently testable and can be parallelized across teams after Foundational
- **Polish (Phase 10)**: Depends on all targeted user stories

### User Story Dependencies

- **US1 (P1)**: Foundational only — standalone MVP
- **US2 (P2)**: Foundational + US1 loop (surfaces translate to the same run model)
- **US3 (P2)**: Foundational + US1 (wraps the loop with trust/isolation)
- **US4 (P2)**: Foundational + US1 (meters/observes the loop); eval gate is independent
- **US5 (P3)**: Foundational + US1 (memory/skills feed the loop's context)
- **US6 (P3)**: Foundational + US3 (onboarding relies on tenancy/connectors)
- **US7 (P3)**: Foundational + US1 (reliability wraps the run lifecycle)

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

---

## Implementation Strategy

### MVP First (User Story 1)

1. Complete Phase 1: Setup
2. Complete Phase 2: Foundational (CRITICAL — blocks all stories)
3. Complete Phase 3: User Story 1
4. **STOP and VALIDATE**: run quickstart.md Scenario 1 independently
5. Deploy/demo the reliable cost-bounded loop

### Incremental Delivery (aligned to the plan's six phases)

1. Setup + Foundational → foundation ready
2. US1 (kernel) → MVP
3. US2 (surfaces) + US3 (trust) + US4 (cost/observability) → the P2 platform
4. US5 (memory/skills) + US6 (config/deploy) + US7 (reliability/scale) → full platform
5. Polish + go-live gate → production launch

### Parallel Team Strategy

After Foundational completes, staff US1 first, then fan out US2/US3/US4 in parallel;
US5/US6/US7 follow once their P2 prerequisites land. Each story integrates without
breaking earlier stories.

---

## Notes

- [P] tasks touch different files with no incomplete dependencies
- [Story] labels map tasks to spec user stories for traceability
- Every user story is an independently testable increment
- Verify tests FAIL before implementing (TDD where tests are listed)
- Commit after each task or logical group
- The kernel is never forked per customer — all per-org behavior is data/config (FR-050)
