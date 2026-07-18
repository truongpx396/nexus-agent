# Implementation Plan: Production-Grade AI Agent Platform

**Branch**: `001-agent-platform` | **Date**: 2026-07-17 | **Spec**: [spec.md](spec.md)

**Input**: Feature specification from `/specs/001-agent-platform/spec.md`

## Summary

Build **one** model-agnostic AI agent platform: a single reliable kernel loop
(observe → think → act, an async generator with typed terminal states over an
append-only event log) wrapped in an engineered harness (tools, cache-stable
context, cost metering, memory, skills, reliability), exposed through thin surface
adapters (CLI, chat, web, REST/gRPC, email, cron, Telegram/Zalo), fronted by a control plane
(auth, RBAC, budgets, routing), and grounded in a trust surface (per-tenant
isolation, vaulted secrets, audit receipts, evals-in-CI). The same build serves
multi-tenant SaaS, single-tenant, self-hosted/BYOC, and hybrid topologies by
configuration — the kernel is never forked per customer.

**Technical approach**: A hard control-plane / data-plane split behind a versioned
contract. Go 1.23 owns the control plane, gateway, and kernel loop for concurrency
and small deployable binaries; Python 3.12 hosts ML/eval/condenser helpers.
PostgreSQL is the append-only event log with row-level security (tenant isolation);
Redis provides session locks, rate-limit counters, and ephemeral state. Agent runs
execute as asynchronous jobs on a durable queue processed by stateless, disposable
workers with all state externalized, routed by session key (per-session serial,
cross-session concurrent), backed by a warm per-tenant sandbox pool with hard TTLs.
Delivered in six shippable phases (kernel → harness → reliability/context →
surfaces/skills → trust surface → scale/compliance), each an independently testable
increment.

## Technical Context

**Language/Version**: Go 1.23 (control plane, gateway, kernel loop, workers);
Python 3.12 (eval harness, LLM-as-judge, context condenser / summarizer helpers);
TypeScript 5.x on React 19 (web surface)

**Primary Dependencies**: Go stdlib + `net/http`/gRPC, `pgx` (Postgres),
`go-redis`; a single internal provider-abstraction interface with adapters
(Anthropic native, OpenAI-compatible, Bedrock/Vertex, CLI-subprocess fallback);
OpenTelemetry SDK; MCP client for external connectors; Python: eval runner +
LLM-as-judge; React 19 + Vite + Tailwind + React Query

**Storage**: PostgreSQL (append-only event log + cost records + audit receipts +
tenant/agent/skill config, tenant isolation via row-level security); Redis
(session locks, rate-limit token buckets, sandbox-pool metadata, hot session
cache); object storage (S3-compatible) for offloaded oversized tool outputs and
large artifacts, referenced by path from the event log; external secrets vault
(secrets injected at tool-execution time, never in the prompt)

**Testing**: Go `go test` (unit + integration, incl. testcontainers for
Postgres/Redis); Python `pytest` for the eval harness; a versioned eval set
(~20 real cases) with an LLM-as-judge rubric + end-state checks running in CI as
the release gate; contract tests against the control-plane ↔ data-plane API and
the kernel ABI interfaces

**Target Platform**: Linux server (containerized, OCI images + Helm chart /
Terraform module); per-tenant Docker/microVM (Firecracker/gVisor) sandboxes for
SaaS, lighter containers for single-tenant/BYOC; web surface targets evergreen
browsers

**Project Type**: Web application + service platform — Go backend services
(control plane, kernel/runtime workers, surface adapters), Python eval/ML helper
service, React frontend web surface

**Performance Goals**: >90% cache-read on steady-state turns; p95 queue-wait
< 5s interactive / < 60s batch; first-token < 2s interactive; sustain thousands
of concurrent long-running sessions; directional −40% cost / −40% latency per
completed task versus an unoptimized baseline at equal quality

**Constraints**: Availability ≥99.9% control plane/API and ≥99.5% agent-run
completion; hard per-task and per-tenant cost ceilings terminate with an explicit
`cost_exhausted` reason; Rule of Two enforced per session; fail-closed defaults
throughout; secrets never in the prompt; sensitive/regulated payloads routable to
a self-hosted in-VPC model so they never leave the trust boundary; default 90-day
memory retention (tenant-overridable); a required approval unanswered within its
TTL expires as a denial (`approval_expired`)

**Scale/Scope**: 55 functional requirements across 8 user stories; single reusable
kernel serving 8+ surfaces (CLI, chat, web, REST/gRPC, email, cron, Telegram, Zalo)
plus per-user personal connectors (Gmail/Drive/Calendar); startup (5 people) →
enterprise (50,000 people) via
configuration; four deployment topologies from one build; ~5,000+ concurrent
sessions per production single-org deployment

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluated against Nexus Agent Constitution v1.0.0 (all nine Core Principles plus
the Security, Delivery/Scale, and Workflow constraint sections).

| # | Principle | How this plan complies | Status |
|---|-----------|------------------------|--------|
| I | One Loop, Many Surfaces | Single kernel async generator with typed terminal states; surfaces are thin adapters that only translate I/O (FR-001, FR-028). No per-surface control-flow fork. | PASS |
| II | Immutable Models, Append-Only State | Agent/Tool/Model/config immutable; only mutable state is the append-only event log; every `tool_use` paired with a `tool_result` (synthetic on cancel/error) (FR-003, FR-006). | PASS |
| III | Cache-Stable Context Is Architecture | Byte-stable prefix + volatile tail; per-turn content banned from the prefix; >90% cache-read target; structured off-loop compaction (FR-013, FR-014, FR-015). | PASS |
| IV | Stop on Cost, Not Vibes | Per-turn token metering attributed to task+tenant; hard per-task/per-tenant ceilings → `cost_exhausted`; iteration/wall-clock are backstops; η$ and CPM in the release gate (FR-016, FR-017, FR-018). | PASS |
| V | Safety Is Per-Invocation and Fails Closed | Per-invocation safety on parsed input; fail-closed tool defaults; layered defense; Rule of Two; untrusted tool/retrieved content (FR-008, FR-009, FR-032, FR-033). | PASS |
| VI | Tenant First; Audit & Observability Day-One | Tenant is the first dimension of session key/row/workspace/cost/secret; DB row-level security; immutable audit log; per-turn structure-only observability (FR-038, FR-039, FR-040). | PASS |
| VII | Model- and Provider-Agnostic by Abstraction | One provider abstraction + normalized stream contract; native tool-calling only; deterministic auditable routing by data label + difficulty; regulated payloads → self-hosted (FR-027, FR-037). | PASS |
| VIII | Reliability: Classify, Resume, Never Silently Retry | Typed failure classification before retry; logged backoff+jitter; circuit-break at 3 identical failures; durable checkpoint/resume; stuck detection; rainbow deploy (FR-023, FR-024, FR-025, FR-026). | PASS |
| IX | Verify Against Acceptance Criteria; Govern Every Change | No self-declared success; verified against explicit criteria; prompts/tools/skills are versioned, reviewed, eval-gated (≥90% pass + zero regressions); skills promoted only via human/eval gate (FR-021, FR-042, FR-043, FR-044). | PASS |

**Additional constraint sections**: Security & Trust Surface (secrets/identity/
receipts/egress/HITL/compliance) → FR-034–FR-037, FR-045; Delivery, Scale &
Technology (control/data-plane split, config-not-forks, stateless externalized
state, files-first memory) → FR-019, FR-030, FR-046–FR-050; Development Workflow
(evals as release gate, reviewed config, structure-only observability, go-live
gate) → FR-042–FR-045. All satisfied by design.

**Result**: PASS — no violations. Complexity Tracking table is intentionally empty.
The multi-service structure (control plane, runtime, eval helper, web) is mandated
directly by Principle I (surface/kernel separation), the control/data-plane split,
and Principle VII (Python for ML helpers), not incidental complexity.

## Project Structure

### Documentation (this feature)

```text
specs/001-agent-platform/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
│   ├── kernel-abi.md            # Provider/Tool/Memory/Workspace/Channel interfaces
│   ├── control-data-plane.md    # Versioned control-plane ↔ data-plane contract
│   ├── run-api.openapi.yaml     # External run-submission REST surface contract
│   └── tool-contract.md         # Self-describing tool + execution-pipeline contract
├── checklists/
│   └── requirements.md
└── tasks.md             # Phase 2 output (/speckit.tasks command - NOT created here)
```

### Source Code (repository root)

Monorepo aligned with the draft plan's conventions (Go control plane + kernel,
Python ML/eval helper, React web surface). The control plane and data plane are
separate deployables behind a versioned contract so the data plane can move into a
customer VPC by configuration.

```text
backend-go/
├── cmd/
│   ├── control-plane/        # gateway: authN (SSO/OIDC), RBAC, rate limit, budget, routing
│   ├── runtime-worker/       # stateless worker: pulls a session, runs the kernel loop
│   └── surface-gateway/      # thin surface adapters entrypoint (CLI/API/chat/email/cron/telegram/zalo)
├── kernel/                   # the agent loop: async-generator step, typed terminal states,
│                             #   response classification, tool_use/tool_result invariant
├── internal/
│   ├── provider/             # provider abstraction + normalized stream contract + adapters
│   ├── tools/                # registry (self-registering), buildTool factory, exec pipeline
│   ├── connectors/           # per-user OAuth (auth-code+PKCE), token vault/refresh/revoke,
│   │                         #   reference connectors (gmail, gdrive, gcalendar)
│   ├── context/              # two-zone prompt, cache discipline, structured compaction
│   ├── memory/               # file-first memory, per-tenant, injection screening, retention
│   ├── skills/               # progressive disclosure + propose→gate→version→promote
│   ├── cost/                 # per-turn token/cost meter, per-task/per-tenant ceilings
│   ├── reliability/          # failure classifier, circuit breaker, stuck detection, resume
│   ├── tenancy/              # tenant context, RLS scoping, per-tenant budgets/limits
│   ├── security/             # layered defense, Rule of Two, receipts, egress, secrets vault
│   ├── audit/                # immutable audit log + tamper-evident tool receipts
│   ├── queue/                # durable job queue, session-key routing, admission control
│   ├── sandbox/              # warm pool, TTL/reclamation, per-tenant caps, isolation
│   ├── surfaces/             # per-surface adapter translators (cli, api, chat, email, cron, telegram, zalo)
│   └── observability/        # OTel spans, structure-only tracing, cost/latency/token spans
├── migrations/               # Postgres schema incl. row-level security policies
└── tests/
    ├── contract/             # kernel ABI + control/data-plane + run-API contract tests
    ├── integration/          # multi-tenant isolation, resume, cost-ceiling, HITL
    └── unit/

ml-python/                    # Python 3.12 helper service (off the paying loop)
├── src/
│   ├── evals/                # ~20-case eval set, LLM-as-judge rubric, end-state checks, CI gate
│   ├── condenser/            # structured compaction / summarizer on a cheaper helper model
│   └── judge/                # rubric scoring + held-out grader protection
└── tests/

frontend/                     # React 19 web surface (a thin surface adapter)
├── src/
│   ├── components/
│   ├── pages/
│   └── services/             # run submission, event-stream (SSE/WS) subscription, polling
└── tests/

deploy/                       # OCI image set + Helm chart / Terraform module;
                              #   KEDA/HPA autoscale-on-queue-depth policy for BYOC
```

**Structure Decision**: Web-application + multi-service platform layout. The Go
`backend-go/` tree holds three separately deployable binaries (`control-plane`,
`runtime-worker`, `surface-gateway`) sharing the immutable `kernel/` and
`internal/` harness — this realizes the mandatory control-plane / data-plane split
(the data plane = `runtime-worker` + `kernel` + `internal/{sandbox,memory,provider}`
can deploy into a customer VPC unchanged). `ml-python/` isolates ML/eval work that
must run off the paying loop. `frontend/` is one surface adapter among many. All
per-organization behavior lives in Postgres config rows + markdown bootstrap files
read at runtime — the kernel is never forked.

## Complexity Tracking

> No Constitution Check violations. Table intentionally empty.

| Violation | Why Needed | Simpler Alternative Rejected Because |
|-----------|------------|-------------------------------------|
| — | — | — |
