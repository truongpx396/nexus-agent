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
`go-redis`; NATS JetStream (`nats.go`) as the default durable-queue + event-plane
adapter behind an abstract queue port (SQS/Redis Streams/Temporal-class swappable);
a single internal provider-abstraction interface with adapters
(Anthropic native, OpenAI-compatible, Bedrock/Vertex, CLI-subprocess fallback);
OpenTelemetry SDK; MCP client for external connectors; E2B sandbox runtime
(default code-execution backend, swappable for Docker/microVM/local-OS isolation);
crawl4ai for LLM-friendly web fetch/crawl (clean chunked markdown); Python: eval
runner + LLM-as-judge; React 19 + Vite + Tailwind + React Query

**Storage**: PostgreSQL (append-only, `schema_version`-stamped event log + cost
records + hash-chained audit receipts + tenant/agent/skill config; tenant
isolation via row-level security with **transaction-local** scope — `SET LOCAL`,
never session-level, because PgBouncer runs in transaction-pooling mode);
Redis (session-key serial locks, **atomic budget-reservation counters**,
rate-limit token buckets, sandbox-pool metadata, hot session cache); NATS
JetStream (durable job queue + persisted-consumer redelivery + structure-only
run-event pub/sub, default adapter behind the swappable queue port); object
storage (S3-compatible) for offloaded oversized tool outputs and large artifacts,
referenced by path from the event log; external secrets vault + KMS/HSM
(tool-execution-time secret injection, per-tenant content-encryption keys with
BYOK, and a **sign-only** audit-chain signing key)

**Testing**: Go `go test` (unit + integration, incl. testcontainers for
Postgres/Redis) run against a **deterministic recorded/fake provider** so
correctness tests neither flake nor bill a live model; property-based tests over
generated event sequences for the transcript-hygiene invariants; the cross-tenant
isolation test executed **through the PgBouncer tier** used in production; Python
`pytest` for the eval harness; a versioned eval set (~20 real cases) with an
LLM-as-judge rubric + end-state checks running in CI as the release gate,
delivered in the Foundational phase; contract tests against the control-plane ↔
data-plane API and the kernel ABI interfaces.

The eval suite is the one place determinism is unavailable — it calls live models
— so it is governed by measurement discipline instead: **k trials per case** with
per-case exact intervals and a three-valued verdict where `inconclusive` never
resolves to `pass`; suite classes (regression / capability / safety / negative)
with distinct thresholds; a pinned **`eval_environment_digest`** and cold
per-trial sandboxes, because the warm pool is shared state and resource
configuration is a measured confounder; a **pinned, calibrated, cross-family
judge** that cannot block a change without a current passing calibration; and
efficiency (tokens, turns, tool calls, active time) gated alongside quality.
Quality in production is measured by an **in-boundary online scorer** emitting
structure-free scores through the telemetry allowlist — the platform's answer to
continuous evaluation without reopening the content-egress path

**Target Platform**: Linux server (containerized, OCI images + Helm chart /
Terraform module); code/shell execution defaults to E2B sandboxes with hard
per-sandbox resource limits (CPU/memory/PID/wall-clock) and network default-deny,
falling back to per-tenant Docker/microVM (Firecracker/gVisor) for SaaS and
lighter containers / local-OS isolation for single-tenant/BYOC; web surface
targets evergreen browsers

**Project Type**: Web application + service platform — Go backend services
(control plane, kernel/runtime workers, surface adapters), Python eval/ML helper
service, React frontend web surface

**Performance Goals**: >90% cache-read on steady-state turns; p95 queue-wait
< 5s interactive / < 60s batch; first-token < 2s interactive; sustain thousands
of concurrent long-running sessions; directional −40% cost / −40% latency per
completed task versus an unoptimized baseline at equal quality

**Constraints**: Availability ≥99.9% control plane/API and ≥99.5% agent-run
completion, each with a defined error budget and burn-rate alerting; RPO ≤5 min /
RTO ≤4 h with a quarterly rehearsed restore; hard per-task and per-tenant cost
ceilings enforced **pre-spend** by reservation (never post-hoc aggregation),
terminating with an explicit `cost_exhausted` reason; Rule of Two enforced per
session from declared per-tool taint metadata; fail-closed defaults
throughout; secrets never in the prompt; all code/shell execution runs in a
resource-limited (CPU/mem/PID/wall-clock) sandbox with network default-deny
(egress only via the domain allowlist); sensitive/regulated payloads routable to
a self-hosted in-VPC model so they never leave the trust boundary; default 90-day
memory retention (tenant-overridable); high-impact actions gated by an approval
**transaction** — bound to the digest of the exact resolved call, rendered to a
named approver as a decision-ready package, resolvable only by an authorized human
(separated from the requester and step-up re-authenticated on irreversible
classes), invalidated with the run it gates, and unanswered-after-escalation
expiring as a denial (`approval_expired`)

**Scale/Scope**: 159 functional requirements across 9 user stories (see the MVP cut
line below for what ships in Increment 1); single reusable
kernel serving 9 surface classes (CLI, chat, web, REST/gRPC, email, cron, Telegram,
Zalo, and agent-to-agent ingress — the last being its own `principal_kind`
admission class, FR-158)
plus per-user personal connectors (Gmail/Drive/Calendar); startup (5 people) →
enterprise (50,000 people) via
configuration; four deployment topologies from one build; ~5,000+ concurrent
sessions per production single-org deployment

## Constitution Check

*GATE: Must pass before Phase 0 research. Re-check after Phase 1 design.*

Evaluated against Nexus Agent Constitution **v1.2.0** (all nine Core Principles plus
the Security, Delivery/Scale, and Workflow constraint sections). v1.2.0 added no
principle; it materially expanded two Development Workflow rules, and both are
mapped explicitly below rather than assumed to be covered:

- *"Observability captures structure, not content"* — now names the mechanism
  (deny-by-default attribute allowlist, no content-admitting flag) and makes
  debugging access an audited, scoped, expiring grant emitting a receipt per read
  → FR-117, FR-118 (Principle VI row).
- *"State artifacts are not interchangeable"* — compaction / checkpoint / snapshot
  separated, write-ahead effect claims resolved by probe or human rather than
  re-execution, replay / resume / fork named as three operations, per-run
  configuration digest required for reproducibility → FR-126–FR-130 (Principle
  VIII row and the Development Workflow mapping below).

| # | Principle | How this plan complies | Status |
|---|-----------|------------------------|--------|
| I | One Loop, Many Surfaces | Single kernel async generator with typed terminal states; surfaces are thin adapters that only translate I/O (FR-001, FR-028). No per-surface control-flow fork. "Thin" bounds what an adapter may *do*, not what it must be *able* to do: each surface publishes a conformance-tested capability descriptor that approval and input-request routing filter on, declares a `principal_kind` (agent ingress being its own admission class), resolves the **turn-submitting** principal rather than inheriting authority from a conversation, binds its native thread identity into `session_key` by declaration, and delivers outbound through a durable outbox (FR-155–FR-159). | PASS |
| II | Immutable Models, Append-Only State | Agent/Tool/Model/config immutable; only mutable state is the append-only event log; every `tool_use` paired with a `tool_result` (synthetic on cancel/error) (FR-003, FR-006). | PASS |
| III | Cache-Stable Context Is Architecture | Byte-stable prefix + volatile tail; per-turn content banned from the prefix; >90% cache-read target; structured off-loop compaction (FR-013, FR-014, FR-015). | PASS |
| IV | Stop on Cost, Not Vibes | Per-turn token metering attributed to task+tenant; hard per-task/per-tenant ceilings → `cost_exhausted`; iteration/wall-clock are backstops; η$ and CPM in the release gate (FR-016, FR-017, FR-018). | PASS |
| V | Safety Is Per-Invocation and Fails Closed | Per-invocation safety on parsed input via a hybrid deterministic-rule + fail-closed-timeout model classifier; fail-closed tool defaults; layered defense; Rule of Two; untrusted tool/retrieved content (FR-008, FR-009, FR-032, FR-033, FR-116). Human oversight is specified as a transaction rather than a flag — approval binds the digest of the exact resolved call, carries a decision-ready context package, is resolvable only by an authorized human, is invalidated with the run it gates, and sits in one published total resolution order in which a deny is final and neither the per-invocation safety check nor the Rule of Two can be short-circuited by any scope, batch, or autonomy level (FR-103–FR-112). The tool/connector/MCP catalog itself is a trust boundary: every descriptor is scanned for injected instructions at admission and on version bump, and every connector/MCP token is minted audience-restricted to its own server (FR-113, FR-114). The catalog itself is identity-bearing and bypass-free: tools are `{namespace}/{name}@{version}` with one owning source per namespace and collision refused at admission, descriptors are re-verified against the admitted digest at use, skills are signed content-addressed bundles that may only *narrow* capability, and sandbox-originated calls re-enter the same pipeline through a broker or have no route at all (FR-147, FR-149, FR-150, FR-151, FR-153). | PASS |
| VI | Tenant First; Audit & Observability Day-One | Tenant is the first dimension of session key/row/workspace/cost/secret; DB row-level security with **transaction-local** scope that survives the transaction-pooling tier, proven by an isolation test run through that pooler; **hash-chained, externally anchored** audit log with sign-only key custody. Observability is specified as a mechanism rather than an intention: telemetry is a **content-free signal class** enforced by a deny-by-default attribute allowlist so it stays inside the crypto-shredding erasure boundary, spans are derived from the log and turn-scoped so long and killed runs still trace, events and spans carry a bidirectional join key, metric labels are fixed with per-run detail via exemplars, and reading decrypted content is an audited, expiring, receipt-emitting grant rather than an operator capability (FR-038, FR-039, FR-040, FR-081, FR-117–FR-125). | PASS |
| VII | Model- and Provider-Agnostic by Abstraction | One provider abstraction + normalized stream contract; native tool-calling only; deterministic auditable routing by data label + difficulty; regulated payloads → self-hosted (FR-027, FR-037). Third-party frameworks (model gateways, tracing backends, eval platforms, workflow engines) attach as **optional** adapters behind existing ports and are bound by one authority boundary — transport, capacity, storage, or presentation only, never routing, ceilings, truth, the gate, the audit record, or content — each admitted by a conformance suite recording what it supports, degrades, and cannot do (FR-131–FR-136). | PASS |
| VIII | Reliability: Classify, Resume, Never Silently Retry | Typed failure classification before retry; logged backoff+jitter; circuit-break at 3 identical failures; durable checkpoint/resume; stuck detection — itself eval-gated against a negative-case set and escalating from a logged `stuck_suspected` signal to a hard terminate only on a corroborating second trip, so an imprecise heuristic never silently discards partial work; rainbow deploy (FR-023, FR-024, FR-025, FR-026, FR-115). v1.2.0's "state artifacts are not interchangeable" rule is met structurally rather than by convention: `Condensation` / `Checkpoint` / `Snapshot` are three artifacts with different consumers and correctness properties, the exactly-once claim is committed **write-ahead** and resolved on resume by probe or human escalation — never by re-execution and never by silent discard — and `replay` / `resume` / `fork` are three named operations with distinct guarantees, each run persisting the `harness_digest` that determined its behavior (FR-126–FR-129). | PASS |
| IX | Verify Against Acceptance Criteria; Govern Every Change | No self-declared success; verified against explicit criteria; prompts/tools/skills are versioned, reviewed, eval-gated (≥90% pass + zero regressions); skills promoted only via human/eval gate (FR-021, FR-042, FR-043, FR-044). Eval set + CI gate delivered in **Foundational**, before the first behavior-bearing slice; correctness suite runs on a deterministic provider harness (FR-097). The constitution's "track pass rates over N runs" is now operational rather than implied: k trials with per-case intervals, a three-valued verdict that never resolves `inconclusive` to `pass`, a pinned eval-environment digest, suite classes, a calibrated cross-family judge, gated efficiency, and a measured held-out gap (FR-137–FR-146). | PASS |

**Additional constraint sections**: Security & Trust Surface (secrets/identity/
chained receipts/at-rest encryption/erasure/egress/HITL/webhook authenticity/
compliance) → FR-034–FR-037, FR-045, FR-080–FR-082, FR-089–FR-092; Delivery,
Scale & Technology (control/data-plane split, config-not-forks, stateless
externalized state, files-first memory, pre-spend ceilings, versioned event
envelope, expand/contract migration, rehearsed restore) → FR-019, FR-030,
FR-046–FR-050, FR-083, FR-086, FR-090, FR-094; Development Workflow (evals as
release gate, reviewed config, content-free observability with audited content
access, **state artifacts not interchangeable** — the v1.2.0 rule: separated
durable-state artifacts, write-ahead effect claims, replay/resume/fork, and the
per-run harness digest — SLO/error budget, named ownership, go-live gate) →
FR-042–FR-045, FR-095–FR-097, FR-117–FR-125, FR-126–FR-130.

**Result**: PASS on principles I–IX with **one recorded tension** (see Complexity
Tracking): the constitution's "build for the current stage" rule versus a 159-FR,
9-surface-class, 4-topology target architecture starting from zero code. This is
resolved by sequencing, not by scope reduction — the spec remains the target
architecture and the MVP cut line below states what actually ships first. The
multi-service structure (control plane, runtime, eval helper, web) is mandated
directly by Principle I (surface/kernel separation), the control/data-plane
split, and Principle VII (Python for ML helpers), not incidental complexity.

## MVP cut line (what ships first, and what is deliberately deferred)

The specification is the target architecture; this plan is not a commitment to
build all of it before anything is usable. **Increment 1 is the smallest system
that is honestly production-shaped**, and everything else is explicitly deferred
rather than implicitly assumed.

**In Increment 1 (ships and is usable):**

- The kernel loop: typed response classification, paired `tool_use`/`tool_result`
  with hygiene pass, typed terminal reasons, mid-run steering, cancel.
- One provider adapter + the **deterministic recorded/fake provider** (FR-097).
- Built-in tools: workspace-restricted filesystem, sandboxed shell, web fetch —
  each with taint declarations (FR-087) and a **fully-qualified identity**
  `{namespace}/{name}@{version}` with one owning source per namespace (FR-147),
  because a bare name already written into an approval scope or an audit receipt
  cannot be re-qualified afterwards.
- Postgres event log with `schema_version`, the `trace_id`/`span_id` join key
  (FR-119), the `harness_digest` and fork columns (FR-129, FR-128), the separated
  `Checkpoint`/`Snapshot`/`IdempotencyClaim` tables (FR-126, FR-127), RLS with
  **transaction-local** scope, and the isolation test running through PgBouncer
  (FR-039).
- A **content-free** telemetry export path: deny-by-default attribute allowlist,
  bounded value lengths, and no flag that admits content (FR-117) — cheap on day
  one, and the only way the erasure attestation stays true once telemetry exists.
- Cost: token classes split, versioned price book, **reserve-then-reconcile**
  ceilings (FR-016, FR-083, FR-084).
- Audit: hash-chained receipts + verifier (FR-081); per-tenant envelope
  encryption with the erasure path in place (FR-080, FR-089).
- The ~20-case eval set, judge, and CI gate — **before** the loop is tuned — with
  the measurement discipline that makes the gate mean something: trial statistics
  and a three-valued verdict, a pinned eval-environment digest with cold trial
  sandboxes, suite classes, and a judge calibrated against human labels *before*
  it blocks its first change (FR-137, FR-138, FR-139, FR-141).
- The **catalog, skill, and surface identity seams** (FR-147–FR-159): the
  `CatalogManifest`, `Skill.bundle_digest`/`origin`/`executable`, the `Surface`
  capability descriptor with its `principal_kind` and `conversation_binding`, the
  `DeliveryRecord` outbox table, `SurfaceIdentity.channel_operator`, and
  `Session.surface_id`/`audience_ref`. Same rule as every other seam above — a
  session whose turns were never attributed to a *submitting* principal cannot
  have that attribution reconstructed later, and a skill admitted without a
  bundle digest cannot be signature-checked retroactively.
- One surface (REST) and single-tenant SaaS topology.

**Deliberately deferred (built when the stage demands it, not before):**

| Deferred | Until |
|----------|-------|
| NATS JetStream durable queue + stateless worker pool | Concurrency exceeds one worker's comfortable load; Increment 1 runs the loop in-process behind the same queue *port* |
| E2B / microVM sandbox pool + warm-pool autoscaler | Multi-tenant hostile isolation is required (single-tenant containers suffice first) |
| Control-plane / data-plane **physical** split | A customer requires BYOC; the *contract* and package boundaries exist from day one so the split is a deployment change |
| Telegram/Zalo + personal connectors (US8) | After the trust surface (US3) — they are the highest-risk ingress and depend on FR-082 |
| Memory tiers beyond files, skills promotion | The file-first tier is exhausted (~1M tokens durable knowledge) |
| Sub-agent delegation (behavior) | A single continuous context stops being sufficient for a real workload. **The delegation *seams* are not deferred** — the chain columns (`root_session_id`/`parent_session_id`/`depth`, receipt `delegation_path`) and the `Delegation` interface ship in Increment 1, because retrofitting a chain onto historical cost records and audit receipts is exactly the event-log migration this cut line exists to prevent |
| The orchestration plane (US9, P2) | The first customer process that must run the same way twice. It follows US3 + US4 (it needs approval gates and cost/eval governance) but **not** US5 — plans without `delegate_fanout` steps need no delegation machinery. Deferred from Increment 1 as *behavior*, not as a seam: the `plan_*` event types and `Session.plan_id`/`plan_version` are in the Foundational taxonomy so a plan run replays from a log written before plans existed |
| Multi-region residency, BYOK, chargeback export | A tenant contract requires them; the schema seams (`region`, `Encryption Key`, cost dimensions) exist from day one so they are additive |
| Fork-based incident debugging, snapshot-bounded hydration, log-derived span emission (FR-128, FR-126, FR-120) | The first long-running production incident and the first session long enough for full replay to hurt. **The seams are not deferred**: the join key, harness digest, fork columns, claim table, and separated checkpoint/snapshot tables ship in Increment 1, because each is a column on an append-only log |
| Content-access grants as a workflow (UI, approver routing) (FR-118) | Support access is needed operationally. The *enforcement point* — no plaintext read outside a grant, receipt per read — ships with encryption in Increment 1; only the routing and UI wait |
| Third-party adapters — model gateway, external tracing backend, workflow engine, dataset sync (FR-131–FR-136) | Each is wanted. Increment 1 ships **built-in defaults only**, which is the state SC-040 requires to keep working forever. **The seams are not deferred**: the ports already exist, the `IntegrationAdapter` registry and the authority-boundary assertions ship with the schema, and the conformance suite ships with the first adapter — an adapter admitted without a recorded capability matrix is the failure FR-133 exists to prevent |
| Deferred tool disclosure + the gated selector (FR-062, FR-148) | The catalog grows past the declared engagement threshold. Increment 1 ships a small resident catalog, so there is nothing to defer and no selector to measure. **The seam is not deferred**: the `CatalogManifest` pins the resolvable universe into the harness digest from day one, so turning disclosure on later never moves a run's digest |
| The in-sandbox broker for programmatic tool calling (FR-149) | The token savings of in-sandbox orchestration are actually needed. **This is a deferral of a capability, not of a control**: until the broker exists, connector and MCP endpoints are in the sandbox egress deny set, so the "no route at all" branch of FR-149 is the shipped behaviour — a bypass is never the interim state |
| Skill marketplace import, executable bundles, trust tiers (FR-152) | A tenant asks to import third-party skills. Increment 1 admits `first_party` and `agent_proposed` origins only, through the one gate FR-152 requires of every origin. **The seam is not deferred**: `origin`, `bundle_digest`, `signature`, and `trust_tier` are columns, and a skill admitted without them cannot be verified retroactively |
| Agent-to-agent ingress (FR-158), multi-principal channels (FR-156), escalation/reminder routing (FR-108) | US2 for the channel work, after US3 for A2A — it is the most dangerous principal type and depends on the approval transaction. A2A ships **disabled by default**. The per-turn principal resolution and the delivery outbox are Increment 1 schema for the reason above |
| Four-topology packaging, Helm/Terraform, rainbow deploy | Increment 3 — before the first customer-operated deployment |

The rule this encodes: **seams and schema decisions are made early because they
are expensive to retrofit; infrastructure is added late because it is cheap to
add and expensive to carry.** Every deferred item above is additive against the
Increment 1 schema and contracts — none requires a migration of the event log,
the audit chain, or the encryption model.

## Project Structure

### Documentation (this feature)

```text
specs/001-agent-platform/
├── plan.md              # This file (/speckit.plan command output)
├── research.md          # Phase 0 output (/speckit.plan command)
├── data-model.md        # Phase 1 output (/speckit.plan command)
├── quickstart.md        # Phase 1 output (/speckit.plan command)
├── contracts/           # Phase 1 output (/speckit.plan command)
│   ├── kernel-abi.md            # Provider/Tool/Memory/Workspace/Channel/Skills/
│   │                            #   RunControl/Oversight/Persistence/Telemetry interfaces
│   ├── control-data-plane.md    # Versioned control-plane ↔ data-plane contract
│   ├── run-api.openapi.yaml     # External run-submission REST surface contract
│   ├── tool-contract.md         # Self-describing tool + execution-pipeline contract
│   │                            #   (the published total permission resolution order)
│   ├── orchestration-plane.md   # Declarative plan schema + zero-token transition contract
│   └── integration-ports.md     # Optional third-party adapters + the authority boundary
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
│   ├── tools/                # registry (self-registering), buildTool factory, exec pipeline;
│   │                         #   qualified identity + namespace ownership, catalog manifest,
│   │                         #   deferred disclosure + gated selector, descriptor re-verification;
│   │                         #   builtin/ = filesystem, sandboxed shell, web search/fetch tools
│   ├── connectors/           # per-user OAuth (auth-code+PKCE), token vault/refresh/revoke,
│   │                         #   reference connectors (gmail, gdrive, gcalendar)
│   ├── context/              # two-zone prompt, cache discipline, structured compaction
│   ├── memory/               # file-first memory, per-tenant, injection screening, retention
│   ├── skills/               # signed content-addressed bundles, three-tier disclosure,
│   │                         #   one admission gate per origin, capability narrowing,
│   │                         #   propose→gate→version→promote
│   ├── cost/                 # per-turn token/cost meter, per-task/per-tenant ceilings
│   ├── reliability/          # failure classifier, circuit breaker, stuck detection,
│   │                         #   checkpoint/snapshot/hydrate, write-ahead effect claims, resume
│   ├── tenancy/              # tenant context, RLS scoping, per-tenant budgets/limits
│   ├── security/             # layered defense, Rule of Two, receipts, egress, secrets vault
│   ├── audit/                # immutable audit log + tamper-evident tool receipts
│   ├── queue/                # durable job queue (NATS JetStream default adapter,
│   │                         #   swappable port), session-key routing, admission control
│   ├── sandbox/              # warm pool, TTL/reclamation, per-tenant caps, resource limits
│   │                         #   (CPU/mem/PID/wall-clock) + network default-deny; E2B default backend;
│   │                         #   broker = the only route from sandbox code into the tool pipeline
│   ├── surfaces/             # per-surface adapter translators (cli, api, chat, email, cron,
│   │                         #   telegram, zalo, agent-ingress); capability descriptors,
│   │                         #   per-turn principal resolution, conversation binding,
│   │                         #   outbound delivery outbox
│   └── observability/        # log-derived turn-scoped spans, content-free attribute allowlist,
│                             #   versioned attribute model, fixed metric labels + exemplars,
│                             #   trace-context propagation, cost/latency/token spans
├── migrations/               # Postgres schema incl. row-level security policies
└── tests/
    ├── contract/             # kernel ABI + control/data-plane + run-API contract tests
    ├── integration/          # multi-tenant isolation, resume, cost-ceiling, HITL
    ├── load/                 # concurrency + endurance-soak harness, SC-008 SLA assertions
    └── unit/

ml-python/                    # Python 3.12 helper service (off the paying loop)
├── src/
│   ├── evals/                # corpus + suite classes, trial statistics, environment digest,
│   │                         #   fork-based cases, efficiency budgets, integrity, CI gate
│   ├── condenser/            # structured compaction / summarizer on a cheaper helper model
│   └── judge/                # rubric scoring, held-out grader protection, human-label calibration
└── tests/

frontend/                     # React 19 web surface (a thin surface adapter)
├── src/
│   ├── components/
│   ├── pages/
│   └── services/             # run submission, event-stream (SSE/WS) subscription, polling
└── tests/

deploy/                       # OCI image set + Helm chart / Terraform module;
                              #   KEDA/HPA autoscale-on-queue-depth policy for BYOC;
                              #   load/ = concurrency-soak driver for the SC-008 capacity gate
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

No principle is violated, but three tensions are real enough to record rather than
declare away. Each is resolved by an explicit mechanism, not by assertion.

| Tension | Why the complexity is needed | Simpler alternative rejected because | Resolution |
|---------|------------------------------|--------------------------------------|------------|
| **Target scope (159 FRs, 9 surface classes, 4 topologies) vs. "build for the current stage"** | The spec is a target architecture for a platform whose whole thesis is that the enterprise tax cannot be retrofitted; the schema, contract, and trust seams must be right before the first migration. | Writing a smaller spec would hide the retrofit cost rather than remove it — the expensive decisions (event envelope, encryption/erasure model, audit chain, tenant scoping) are *schema* decisions that cannot be deferred cheaply. | The **MVP cut line** above: seams and schema early, infrastructure late. Every deferred item is additive against Increment 1's schema. |
| **Three languages (Go / Python / TypeScript)** | Go for the concurrency-bound kernel and small BYOC-shippable binaries; Python only where the ML/eval ecosystem lives, and strictly **off the paying loop**; TypeScript only for the web surface. | A single-language stack would either lose the eval/judge ecosystem (Go-only) or the deployable-binary and concurrency properties the data plane needs (Python-only). | Python is confined to `ml-python/` (evals, judge, condenser) and reaches the runtime only through the queue/contract — it is never in the request path. |
| **Control/data-plane split before any customer needs BYOC** | Principle-mandated, and "move the data plane into the customer VPC" is only a flag if the planes never bled together in the first place. | Building one plane and splitting later is the rewrite the constitution's Delivery section exists to prevent. | The **contract and package boundary** ship in Increment 1; the *physical* split is deferred until a BYOC customer exists (see cut line). |

**Sub-agent policy divergence** (recorded, not a violation): FR-079 is
deliberately stricter than the comparable systems — read-only context firewalls
only, no parallel decision-making children spawned at the model's discretion.
This trades away the model-driven breadth-first research win for a single locus
of decision authority, clean cost attribution, and a tractable taint model
(FR-087). The parallelism is relocated rather than abandoned: **FR-102** recovers
it through declarative orchestration plans, where fan-out is reviewed, versioned,
eval-gated configuration evaluated at zero model-token cost — which is the same
trade the constitution already makes for routing (deterministic and auditable,
never model discretion). Widening FR-079 itself remains a governed change under
FR-096.

The one-line rule FR-079 used to be is now a sub-contract, because a sub-agent is
a second locus of execution holding real credentials and spending real money:
**FR-098** (capability descends monotonically, taint ascends monotonically),
**FR-099** (depth / concurrency / per-run bounds and a pre-reserved fan-out cost
envelope, so one delegation cannot starve its own tenant), **FR-100** (return
validation, acceptance criteria, and child reaping), and **FR-101** (full
delegation-chain attribution, not just an immediate parent). FR-087's sanitization
boundary is correspondingly narrowed: summarization reduces volume and may clear
the private-data leg, but **never** the untrusted-content leg — a model
summarizing injected text can carry the injection into its summary, so only an
attributable operator re-baseline clears that leg.
