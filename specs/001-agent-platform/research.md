# Phase 0 Research: Production-Grade AI Agent Platform

**Feature**: `001-agent-platform` | **Date**: 2026-07-17 | **Plan**: [plan.md](plan.md)

All spec-level ambiguities were resolved in the spec's Clarifications section
(availability SLA, queue-wait/latency SLA, approval-timeout behavior, eval-gate
threshold, memory retention). This document records the remaining technical
decisions — one entry per unknown, dependency, and integration point in the
Technical Context — so no `NEEDS CLARIFICATION` remains before Phase 1.

---

## 1. Kernel loop shape

- **Decision**: Implement the loop as a single Go async-generator-style step
  function that classifies each model response into a typed union
  (`TOOL_CALLS` / `CONTENT` / `EMPTY`) and returns a discriminated terminal reason
  (`completed`, `max_turns`, `cost_exhausted`, `error`, `aborted`,
  `prompt_too_long`, `hook_stopped`, `approval_expired`, `input_expired`).
- **Rationale**: Branching on a tagged union (not string matching) eliminates the
  most common agent-loop bug ("the model responded but the code didn't know what
  to do"); exhaustive terminal reasons let every surface handle stops uniformly
  (Constitution I/II; FR-002, FR-004).
- **Alternatives considered**: Callback/event-emitter loop (rejected —
  backpressure, cancellation, and typed stops are lost); string-matched dispatch
  (rejected — fragile, the classic production failure).

## 2. Provider abstraction

- **Decision**: One internal `Provider` interface with a single normalized stream
  chunk contract; adapters for Anthropic native, OpenAI-compatible, Bedrock/Vertex,
  and a CLI-subprocess fallback. Native tool-calling only. Layer
  retry → cooldown → failover across backends.
- **Rationale**: The model is roughly fixed for the platform's life; the harness is
  the durable asset. Standardizing the harness (not the model) enables
  multi-provider failover and capacity spreading without code forks (Constitution
  VII; FR-027).
- **Alternatives considered**: Direct per-SDK calls scattered through the loop
  (rejected — prohibited by Principle VII, defeats failover); parsing tool calls
  from free-form text (rejected — brittle, banned).

## 3. State store & tenant isolation

- **Decision**: PostgreSQL as the append-only event log plus config/cost/audit
  tables, with **row-level security (RLS)** policies keyed on `tenant_id` for
  isolation. Redis for session locks, rate-limit token buckets, and hot state.
  Object storage for offloaded oversized artifacts, referenced by path.
- **Rationale**: One replayable, audit-friendly store; RLS enforces isolation at
  the data layer so an application bug cannot leak across tenants (Constitution
  VI; FR-038, FR-039). Redis handles the per-session serial lock; object storage
  keeps bulky payloads out of the event log and the context window.
- **Alternatives considered**: App-level ACLs only (rejected — Principle VI
  forbids; single bug = cross-tenant leak); NoSQL event store (rejected — loses
  RLS + relational audit queries).

## 4. Concurrency & scale model

- **Decision**: Agent runs are asynchronous jobs on a durable queue, pulled by
  stateless disposable workers with all state externalized. Route by `session_key`
  (tenant-first) → per-session serial, cross-session concurrent. Autoscale on queue
  depth/age. Admission control + weighted-fair scheduling + priority load-shedding +
  graceful degradation at the gateway.
- **Queue tech**: The queue is an abstract port (`internal/queue/`) so it never
  pins the platform to one broker. **NATS JetStream is the documented default
  adapter**: a single embeddable Go binary that travels into a customer VPC with no
  extra managed service (FR-030/BYOC), native ack/redelivery + persisted-consumer
  state for survive-worker-death re-queue-from-checkpoint (FR-024), core pub/sub
  that doubles as the structure-only `StreamEvents` fan-out plane (no Postgres
  polling), and built-in flow control for backpressure/admission (FR-049).
  SQS/Redis Streams/Temporal-class remain drop-in alternates behind the same port.
- **Session serialization stays broker-agnostic**: JetStream ordering is
  per-stream/subject, not "exactly one in-flight per `session_key`", so the
  per-session serial lock is enforced by a Redis lock keyed on `session_key`
  (independent of which broker carries the work) — NATS provides transport,
  durability, and the event plane, not the session-serialization primitive.
- **Rationale**: A run holds a "connection" for minutes across many round-trips and
  must survive deploys — it is a job, not a request. Externalized state makes a
  killed worker lose nothing (re-queue from checkpoint); session-key routing gives
  linear horizontal scale with no history races (FR-041, FR-046, FR-049).
- **Alternatives considered**: Synchronous request-thread-per-run (rejected —
  blocks threads, dies on deploy); CPU-based autoscaling (rejected — workers are
  I/O-bound on the model, CPU is a misleading signal); Redis Streams as the primary
  broker (viable alternate, but weaker native pub/sub fan-out for the event plane
  and an extra hop versus one embeddable JetStream binary in BYOC).

## 5. Sandbox isolation

- **Decision**: Warm pool of pre-provisioned per-tenant sandboxes with hard TTLs,
  reclamation on terminal/stuck state, and per-tenant caps. The default backend is
  a **session-scoped OCI container under gVisor (`runsc`)** — `network=none`, all
  capabilities dropped, read-only root, explicit CPU/memory/PID/wall-clock caps.
  The same image and the same hardening run in both deployment phases: Docker with
  `--runtime=runsc` on a single host, then Kubernetes with
  `runtimeClassName: gvisor`. **Kata Containers** is the hardened tier for tenants
  whose threat model demands a separate kernel, selected per tenant as a second
  RuntimeClass wherever nodes expose hardware virtualization. Git worktrees per
  session for workspace isolation. **Placement is modelled as a second, independent
  axis** — `executor` ∈ {`local`, `ssh`, `service`} — because where a sandbox runs
  and what boundary it runs behind are different questions with different failure
  modes. `local` is the default; `ssh` targets a dedicated execution host and ships
  in Increment 1; `service` is the seat for an external sandbox platform and is
  deferred with every other third-party adapter.
- **Why two axes rather than one enum**: an `ssh` value inside `isolation` would
  assert a boundary it does not describe. A remote host running plain `runc` is a
  `container` sandbox that happens to sit elsewhere, and a single enum would let a
  deployment satisfy "isolation is configured" while running the weakest boundary
  available. Keeping them separate also keeps `eval_environment_digest` honest —
  placement and boundary each move a wall-clock-bounded result, and each is
  recorded. The rule that makes the axis safe rather than merely descriptive: **the
  executor never relaxes the isolation contract.** A non-`local` executor must
  still deliver the FR-059 limits, the network default-deny, and the workspace-only
  filesystem view on the far side, and one that cannot attest to them is refused
  rather than admitted at a weaker boundary.
- **Why `ssh` earns its place in Increment 1**: it is the single-host topology's
  substitute for the privilege separation Kubernetes gets from RBAC. The
  orchestrator holds root-equivalent authority over whatever runtime it drives, so
  putting the execution host at the far end of an SSH connection is how a droplet
  deployment stops one compromise from being total — the same split the Kubernetes
  phase achieves with a scoped ServiceAccount. It is a built-in, not an adapter:
  credentials come from the vault (FR-034), the target is configuration rather than
  model output, command construction runs through the same allowlist as local
  create arguments, and the target host is subject to the tenant's residency pin
  (FR-091).
- **Rationale**: Cold-start per run dominates tail latency; a warm pool trades
  small idle cost for a large p95 win. The sandbox is the trust boundary; TTL +
  reclamation prevent cost and security leaks (FR-047; Constitution V). gVisor is
  the one option that spans a bare single host and a managed cluster **unchanged**:
  it interposes a userspace kernel without hardware virtualization, so no node
  class, nested-virt setting, or bare-metal instance gates the deployment, and
  isolation strength collapses to one `runtimeClassName` field rather than an
  architectural fork. That is what keeps the plan's deferral honest — the hostile
  multi-tenant upgrade is a config change on a shipped interface, not a port.
- **Two constraints this decision carries**, both load-bearing rather than
  operational detail. (a) The sandbox orchestrator MUST NOT hold a container-runtime
  socket from inside a container: socket access is root-equivalent on the host, so
  containerizing an orchestrator that also parses untrusted channel input and
  untrusted model output buys nothing. The single-host phase orchestrates from the
  host; the Kubernetes phase removes the question entirely by creating Pods through
  an RBAC-scoped ServiceAccount, which is *less* privilege than the arrangement it
  replaces. (b) Every container-create argument — mounts, network mode,
  capabilities, security options — MUST be validated against an allowlist and MUST
  NOT be interpolated from configuration or from any model-derived value. An agent
  platform that builds create arguments from config has a published escape
  (OpenClaw OC-13, unvalidated bind-mount config injection → container escape →
  host takeover); the same shape here carries the same defect.
- **Alternatives considered**: Cold container per run (rejected — seconds of tail
  latency); shared sandbox (rejected — breaks the trust boundary); runc alone
  (rejected as the *default* — a shared kernel suffices for single-tenant BYOC but
  sits below the bar FR-059 sets for hostile multi-tenancy, and defaulting to it
  would make gVisor a migration instead of a flag); **E2B** (rejected as the
  default — the self-host path is genuinely Apache-2.0, but it is Nomad + Consul +
  Terraform rather than Kubernetes, so it stands up a second orchestrator beside
  the cluster, and its Firecracker sandboxes require bare metal or nested
  virtualization. Two readmission paths stay open, and they are not the same door.
  **Self-hosted** E2B is a `microvm` value behind the existing sandbox port,
  admissible wherever that infrastructure cost is already paid; nothing above the
  port changes, because the warm pool, TTLs, limits, and broker rule are all stated
  against the port rather than the backend. **Managed** E2B is a third-party
  adapter that moves session workspace content — tool arguments, files, converted
  documents — outside the deployment, so it is admissible only under the FR-133
  conformance gate and only where the tenant's residency configuration permits it
  (FR-091). That excludes BYOC and every region-pinned tenant by construction, and
  it is the same shape as `approval_context_mode = upstream` in FR-104: a
  topology-restricted choice, never a backend swap); **dify-sandbox** (rejected — it is a seccomp + chroot
  filter around Python/JS snippets inside *one shared container*, so concurrent
  executions share a namespace, a cgroup hierarchy, and a kernel. That is precisely
  the shared-sandbox alternative rejected above, and it offers no shell, no
  session-scoped workspace, and no pool lifecycle. Its escape history —
  CVE-2024-10252, `preload` injection executing before seccomp initialized — is
  structural rather than patchable: a boundary one filter deep fails whole, across
  every concurrent execution sharing it).
- **OpenSandbox is a different question from the two above, and is admitted rather
  than rejected.** E2B and dify-sandbox were candidates to *be* the boundary;
  OpenSandbox (Alibaba Cloud, Apache-2.0) sits **above** it — a lifecycle control
  plane with its own OpenAPI contracts, SDKs, an `execd` agent inside each sandbox,
  and Docker/Kubernetes runtimes that themselves run gVisor or Kata. So it is not
  an alternative to the decision above; it is an alternative to *this platform's
  own* pool, TTL, and reclamation code, and it attaches as a `service` executor
  under the FR-133 gate rather than as an `isolation` value. Three boundaries make
  that adoption conditional rather than free, and each is an instance of
  **adopt the tool, keep the authority** (FR-131). (a) Its **egress-policy API MUST
  NOT become the egress decision**: the FR-037 allowlist and the FR-059 network
  default-deny are platform-owned controls, and a vendor-side policy is at most a
  redundant second enforcement point, never the first. (b) Its lifecycle records
  are a **projection, not the source of truth** — pool state, TTLs, and per-tenant
  caps remain platform-enforced, because a ceiling enforced only in a component the
  platform does not own is not a ceiling. (c) Its in-sandbox `execd` gRPC endpoint
  is a **second route into the sandbox** and MUST NOT become a path from sandbox
  code into the tool pipeline that bypasses the FR-149 broker; where it is reachable
  at all, it is reachable only by the orchestrator. Adopted within those bounds it
  supplies real lifecycle and distributed-scheduling capacity; adopted outside them
  it relocates four controls this platform holds constitutionally.

## 6. Context / cache discipline

- **Decision**: Two-zone prompt — a byte-stable prefix (tool-schema catalog +
  stable system prompt + append-only transcript) followed by a volatile tail
  rebuilt each turn. Per-turn content is structurally banned from the prefix.
  Structured compaction at ~80% budget on a cheaper helper model, off the paying
  loop, keeping recent messages + original requirements verbatim.
- **Rationale**: Input tokens are ~90% of the bill; cache-read is the single
  highest-leverage cost and throughput lever (>90% target). Cache stability is
  architecture, not a late optimization (Constitution III; FR-013–FR-015).
- **Alternatives considered**: In-loop summarization (rejected — pays full price,
  stalls the run); mutating the system prompt mid-session (rejected — busts the
  cache, banned).

## 7. Cost metering & routing

- **Decision**: Meter input/output tokens per turn in the same layer that spends
  them; attribute to task chain + tenant; enforce hard per-task and per-tenant
  ceilings → `cost_exhausted`. Deterministic two-axis routing by data label
  (sensitivity → self-hosted in-VPC for regulated) and difficulty/feature demand
  (capability floor), never model discretion; decision is auditable.
- **Rationale**: Step counts vary ~5× across models; token usage explains most
  performance variance — cost is the real stop signal (Constitution IV/VII;
  FR-016, FR-017, FR-037). Routing by data label keeps regulated payloads inside
  the boundary.
- **Alternatives considered**: Iteration-count-only stops (rejected — false
  signal); model-chosen routing (rejected — non-deterministic, unauditable).

## 8. Memory & skills

- **Decision**: File-first memory (`MEMORY.md`/`USER.md`/`history.jsonl`), injected
  immutably at session start (updates take effect next session), scoped per tenant,
  default 90-day retention (tenant-overridable), scanned for injection/exfiltration
  before injection. Skills as `SKILL.md` with progressive disclosure; agent-proposed
  skills follow propose → human/eval gate → version → promote, never auto-promoted.
  Vector DB / knowledge graph introduced only past ~1M tokens of durable knowledge
  or genuinely graph-shaped data.
- **Rationale**: Frozen-snapshot injection protects the cache; per-tenant retention
  + screening make memory a governed data surface; progressive disclosure keeps
  skill cost low while compounding capability (Constitution IX; FR-019–FR-022).
- **Alternatives considered**: Mid-session memory mutation (rejected — cache bust +
  Principle III); auto-promoting agent skills (rejected — ungoverned behavior
  change, banned); vector DB from day one (rejected — premature, no value on flat
  corpora).

## 9. Reliability & durable execution

- **Decision**: Classify every failure into a typed class before any retry; log
  each retry with reason; exponential backoff + jitter; circuit-break after 3
  identical failing calls. Checkpoint to durable storage (Postgres event log + WAL
  journaling) and resume from last checkpoint; autosubmit partial work on failure.
  Stuck detection (repeated actions / oscillation / zero net change over K steps)
  breaks the loop. Rainbow deploys keep in-flight runs alive.
- **Rationale**: In agentic systems minor issues derail agents and errors compound;
  classification + durable resume + circuit-breaking convert fragile long runs into
  recoverable ones with bounded spend (Constitution VIII; FR-023–FR-026).
- **Alternatives considered**: Silent retry (rejected — banned); restart-from-scratch
  (rejected — expensive, loses partial work); blue/green cutover (rejected — cuts
  running agents mid-task).

## 10. Security & trust surface

- **Decision**: Layered fail-closed defense (channel allowlist, autonomy mode,
  workspace restriction, shell allow/blocklist, per-tenant sandbox, tamper-evident
  audit receipts). Rule of Two per session (≤2 of {untrusted input, private data,
  external state change} unattended; else human approval). Secrets injected at
  tool-execution time from a vault (model sees a handle), per-tenant isolated.
  Act-as delegated identity enforced at the tool boundary. Egress allowlist +
  by-class redaction. High-impact actions gated by scoped approval; unanswered
  approval expires as denial (`approval_expired`) after a configurable TTL.
- **Rationale**: Prompt injection is unsolved — design out the lethal trifecta
  rather than filter; a forgotten flag must yield slow behavior, never a breach
  (Constitution V + Security section; FR-032–FR-037, FR-045).
- **Alternatives considered**: Filter-only injection defense (rejected — "95%
  blocked" is a failing grade); god-mode service account (rejected — Principle V/
  Security); secrets in the prompt (rejected — banned).

## 11. Observability & evals-in-CI

- **Decision**: Telemetry is a **content-free signal class** derived from the
  append-only event log. Spans are emitted from durable events rather than held in
  process memory; traces are **turn-scoped and linked** rather than one long root
  span per run; events and spans carry a **bidirectional join key**
  (`trace_id`/`span_id` on the event, `session.id`/`seq` range/`root_session_id`
  on the span); attributes are **allowlisted by key** with bounded lengths and no
  flag that admits content; the internal attribute model is versioned and mapped
  to a pinned `gen_ai.*` convention at the exporter; metric label sets are fixed
  with per-run detail reached through exemplars; W3C trace context propagates into
  sandbox, connector/MCP, provider, and child sessions; duration is reported as
  active vs suspended, and every latency SLI uses active time. Reading decrypted
  content is a **scoped, expiring, receipt-emitting grant** (FR-118), not an
  operator capability. Eval set (~20 real cases) with an LLM-as-judge rubric +
  end-state checks runs in CI and gates any prompt/tool/model/skill change; ship
  only at ≥90% pass AND zero regressions; held-out grader tests the agent cannot
  edit; production cases enter the corpus only through a consented, redacted,
  governance-signed export (FR-125). **What those two numbers mean statistically
  — trials, metric, interval, verdict — and how quality is measured in production
  rather than only in CI are settled separately in §28**; this section settles
  the telemetry contract, not the measurement one.
- **Rationale**: Four forces decide this shape. (1) **Erasure**: content is
  envelope-encrypted and erased by key destruction (§14), but telemetry leaves
  through an export path the key does not reach — a single prompt fragment on a
  span is an unencrypted copy outside the shredding boundary that falsifies an
  erasure attestation, so the only safe design is a signal class that structurally
  cannot carry content. (2) **Long, suspendable, killable runs**: OpenTelemetry
  exports a span only when it ends, and a run that suspends hours on an approval
  or dies mid-turn is exactly the run an operator needs — turn-scoped traces
  emitted from the log are the only variant that survives both, and they make
  `telemetry_sink_mode = local` the same code path with a different sink. (3)
  **Convention instability**: the GenAI semantic conventions moved to their own
  repository in June 2026, remain pre-stable, and have already renamed
  `gen_ai.system` and the token attributes — alerting defined directly in that
  vocabulary inherits its churn, so the internal model is the source of truth and
  the convention is a translation at the boundary, versioned like the event
  envelope (§18). (4) **Cardinality**: at SC-008 concurrency, session/user/request
  identifiers as metric labels take the monitoring system down before the
  platform; they belong on spans, reachable from a metric via exemplars.
  Separately, reading a customer's conversation was the platform's only privileged
  operation with no receipt, in a system where every mutating action has one — an
  asymmetry that a compliance reviewer finds immediately.
- **Comparison set**: Claude Code ships the most concrete model of this — three
  signals, ~15 named events, content **off by default** behind explicit flags with
  a content-length cap, cardinality toggles, `prompt.id`/`message.uuid`/
  `tool_use_id` correlation into the transcript, and W3C context propagation into
  subprocesses. This platform adopts its correlation and cardinality discipline
  and diverges deliberately on two points: content is not *default-off* but
  *absent*, because a multi-tenant platform with a crypto-shredding erasure
  guarantee cannot offer a flag whose use silently voids it; and spans are derived
  from the log rather than emitted in-process, which a single-user CLI does not
  need. OpenHands, OpenClaw, GoClaw, and Hermes bind a session id into logs or
  JSONL transcripts without a span/attribute contract; none reconciles telemetry
  with an erasure boundary.
- **Alternatives considered**: Content-reading observability (rejected — privacy/
  compliance, and it breaks erasure); content behind a debug flag as Claude Code
  does (rejected for this platform — a flag that voids an erasure attestation is
  not a safe default even when off, and the audited log read of FR-118 serves the
  same need with a receipt); one long root span per run (rejected — exports late
  or never, exactly for the runs that matter); emitting `gen_ai.*` directly as the
  internal model (rejected — pre-stable vocabulary as a dashboard contract);
  session/user as metric labels (rejected — unbounded cardinality); ship-on-
  green-only without regression check (rejected — allows silent regressions,
  spec-gaming).

## 12. Deployment topologies & packaging

- **Decision**: Hard control-plane / data-plane split behind a versioned contract
  from day one; the same build serves multi-tenant SaaS, single-tenant, self-hosted/
  BYOC, and hybrid by configuration. Ship a signed OCI image set + Helm chart /
  Terraform module; BYOC autoscaling policy (KEDA/HPA on queue depth) ships with the
  chart. Per-org behavior is data/config (tenant row + bootstrap markdown + skills +
  surfaces + per-tenant MCP connectors) read at runtime — the kernel is never forked.
- **Rationale**: "Move the data plane into the customer VPC" becomes a deployment
  flag, not a rewrite, only if the two planes never bleed together; config-not-forks
  is what makes the platform sellable across topologies (Constitution Delivery
  section; FR-030, FR-050, FR-012).
- **Alternatives considered**: Per-customer kernel fork (rejected — banned,
  unmaintainable); monolithic single-plane deploy (rejected — cannot satisfy
  data-residency/no-egress mandates).

---

# Phase 0b: Design-review decisions (2026-07-27)

The following unknowns were surfaced by a production-readiness review of the
Phase 0/1 artifacts. Each was a place where two documents were individually
correct but contradicted when combined, or where a stated success criterion was
not measurable from the designed data.

## 13. Tenant isolation under connection pooling

- **Decision**: Tenant scope for row-level security is established
  **transaction-locally** — `SET LOCAL app.tenant_id` (or `SET ROLE LOCAL` onto a
  per-tenant role) inside the transaction that runs the query. Session-level
  `SET` is prohibited. The cross-tenant isolation test runs **through the
  production PgBouncer tier**, not against a direct connection.
- **Rationale**: The scale plan puts PgBouncer in transaction-pooling mode in
  front of Postgres so thousands of workers do not each hold a connection. In
  that mode a physical connection is handed to a different tenant's transaction
  between statements, so session-level scope leaks — silently defeating the only
  control that survives an application bug (Constitution VI; FR-039). An
  isolation test that passes against a direct connection proves nothing about the
  deployed topology.
- **Alternatives considered**: Session pooling instead of transaction pooling
  (rejected — reintroduces the connection-exhaustion problem the pooler exists to
  solve); application-level `WHERE tenant_id = ?` only (rejected — Principle VI
  forbids); a connection per tenant (rejected — does not scale to thousands of
  concurrent sessions).

## 14. Erasure against an append-only log

- **Decision**: **Crypto-shredding.** Event payloads and other customer content
  are envelope-encrypted under a per-tenant data key (per-erasure-subject at
  tiers requiring subject-level DSAR), and erasure destroys the key. No event row
  is ever deleted or rewritten; the sequence, digests, audit chain, and
  structure-only telemetry survive intact. Erasure is itself a typed event with
  an audit receipt.
- **Rationale**: An append-only, replayable, hash-chained log and a right to
  erasure are otherwise flatly incompatible — and the incompatibility is a schema
  decision, so it must be made before the first migration, not after (FR-080,
  FR-089). Retaining the *structural* record that an action occurred while
  destroying its content is the standard reconciliation between erasure rights
  and audit/record-keeping duties.
- **Alternatives considered**: Physically deleting or rewriting events (rejected —
  breaks replay and chain verification, and destroys the audit obligation);
  tombstone-without-encryption (rejected — content remains recoverable from
  backups and WAL); refusing DSAR (rejected — the platform claims GDPR/CCPA
  tiers).

## 15. Audit-log integrity

- **Decision**: Receipts are **hash-chained** per session (`prev_digest` +
  monotonic `chain_seq`), computed over **digests rather than plaintext**, signed
  by a **sign-only** KMS/HSM key the data plane cannot read, with the chain head
  periodically **anchored** to an append-only external store. A scheduled
  verifier proves continuity, sequence completeness, and agreement with the
  latest anchor, and alerts on any break.
- **Rationale**: A per-record HMAC written by the same component that holds the
  key is tamper-*resistant* at best — that component can rewrite or drop records
  undetectably, which is exactly the insider/compromise case an audit log exists
  to answer (FR-081). Chaining over digests additionally keeps verification valid
  after a lawful redaction (§14).
- **Alternatives considered**: Per-record HMAC alone (rejected — the reviewed
  design; not evidence); write-only database grants (rejected — an admin or a
  compromised writer bypasses them); full blockchain (rejected — cost and
  operational burden far exceed the requirement; periodic anchoring gets the same
  external commitment).

## 16. Cost-ceiling enforcement

- **Decision**: **Reserve-then-reconcile.** Before every model call the platform
  reserves worst-case cost (measured input + reserved output at price-book rates)
  against an **atomic** per-tenant and per-task counter; a refusal terminates the
  run `cost_exhausted` before tokens are spent; actuals reconcile the hold
  afterwards and release the remainder. Reservations are TTL-bounded so a crashed
  worker cannot strand budget. The worker also enforces a local hard per-run
  budget synchronously.
- **Rationale**: Aggregating usage after a turn completes and then signalling a
  stop is a lagging indicator, not a ceiling: one expensive turn, or a burst of
  concurrent sessions inside one tenant, exceeds the limit before the signal is
  observed — falsifying SC-002's "zero surprise overruns" (FR-083).
- **Alternatives considered**: Post-hoc aggregation with a stop signal (rejected —
  the reviewed design; races); per-turn hard token caps only (rejected — token
  caps are not dollars across a routing fleet); optimistic accounting with
  after-the-fact billing correction (rejected — a hard ceiling is a safety
  control, not an invoice).

## 17. Cost measurement granularity and price stability

- **Decision**: Meter tokens **split by class** — uncached input, cache-read
  input, cache-write input, output — and compute cost from a **versioned,
  effective-dated price book** held as configuration, with every cost record
  naming the price-book version used.
- **Rationale**: The >90% cache-read gate (FR-014, SC-003) is a release
  criterion; an undifferentiated input-token total makes it unmeasurable, so the
  platform could not evaluate its own headline metric. A price table embedded in
  code silently invalidates every ceiling, η$ figure, and chargeback report the
  day a provider changes prices (FR-016, FR-084).
- **Alternatives considered**: Single `input_tokens` field with an estimated
  cache ratio (rejected — estimates cannot gate a release); hardcoded prices
  (rejected — silent drift, non-reproducible history).

## 18. Event taxonomy, versioning, and projections

- **Decision**: Extend the event taxonomy to cover human steering input, the full
  approval lifecycle, taint transitions, errors, erasure, and a terminal event;
  carry a **`schema_version` envelope** on every event with a documented
  upcasting path; and document every mutable status column as a **projection**
  rebuildable by replay.
- **Rationale**: The reviewed taxonomy could not represent a steering message, an
  approval decision, or a run's own termination — so a run that used any of them
  was not replayable from the "single source of truth" (FR-006, FR-085). An
  event-sourced system whose payload shape evolves without versioning loses its
  multi-year replay guarantee, which is the entire reason for the design
  (FR-086).
- **Alternatives considered**: Keeping status only in mutable columns (rejected —
  two sources of truth); versioning by table migration (rejected — historical
  events must remain readable as written).

## 19. Rule-of-Two evaluation inputs

- **Decision**: Every tool, connector, and retrieval source **declares** its three
  taint legs (returns untrusted content / reads private data / mutates external
  state) as required capability metadata defaulting to `true`; the session
  carries typed accumulated taint state; and taint is reduced only through an
  explicit, audited **sanitization boundary**.
- **Rationale**: The evaluator had no inputs — capability metadata carried only
  read-only-vs-mutating, so the platform's headline safety control could not be
  computed (FR-087). Without a sanitization boundary, any long session becomes
  permanently tainted and gates on everything, and approval fatigue degrades the
  control into a rubber stamp — a failure mode worse than not having it.
- **Alternatives considered**: Runtime inference from tool behavior (rejected —
  unreliable, and fails open on the first unclassified tool); per-run rather than
  per-session taint (rejected — under-approximates a continuing conversation).

## 20. Run-lifecycle completeness

- **Decision**: Promote steer, **cancel**, and resume to first-class operations on
  both the external API and the control/data-plane contract, and persist the
  run's determining inputs — pinned `agent_version`, `data_label`, routing
  decision and reason, execution class and priority, region.
- **Rationale**: `aborted` was a mandated terminal reason with no operation that
  could produce it, and `POST /runs/{id}/input` crossed a plane boundary the
  versioned contract did not describe. Priority load-shedding (FR-049) had no
  persisted field to read, and an unpinned agent version lets a mid-run deploy
  change behavior and bust the prompt prefix (FR-088).
- **Alternatives considered**: Cancel via queue-message deletion (rejected — skips
  the paired-result invariant and the partial-artifact guarantee); re-deriving
  routing at read time (rejected — not auditable, not replayable).

## 21. Inbound channel authenticity

- **Decision**: Every unsolicited inbound path (Telegram/Zalo webhooks, OAuth
  callbacks, inbound email, third-party hooks) verifies the provider's
  signature/shared secret, rejects replays outside a bounded window, and applies a
  per-external-identity flood limit **before** adapter translation. Identity
  binding remains a separate, later control.
- **Rationale**: The reviewed design authenticated *who the user is* (FR-055) but
  never proved *the request came from the provider* — leaving an open endpoint
  that feeds attacker-controlled text straight into the kernel and burns tokens
  without a payer (FR-082). This is the one place where an injection defense can
  be complete, because the check is cryptographic rather than heuristic.
- **Alternatives considered**: Relying on URL secrecy (rejected — not a control);
  filtering at the input guard (rejected — heuristic, and the run has already
  been admitted and metered by then).

## 22. Encryption at rest, residency, and recovery

- **Decision**: Per-tenant envelope encryption of conversation content with
  customer-managed keys available at regulated tiers; region pinning enforced at
  admission (fail closed rather than relocate); an enumerated, structure-only
  egress list for customer-boundary deployments with a selectable **fully local**
  audit/telemetry sink; and a documented RPO ≤5 min / RTO ≤4 h with a quarterly
  rehearsed restore that re-verifies the chain and replays the log.
- **Rationale**: Prompts and tool arguments are customer data sitting in Postgres
  for 90+ days; encryption at rest with per-tenant keys is both the standard
  procurement bar and the mechanism that makes crypto-shredding possible
  (FR-089/FR-080). "Sensitive payloads never leave" was true of content but the
  contract still shipped cost and audit metadata upstream from a customer
  boundary, which needed to be enumerated and made optional (FR-091). An
  availability SLA without a rehearsed restore is a claim, not a commitment
  (FR-090).
- **Alternatives considered**: Full-disk encryption only (rejected — no per-tenant
  boundary, no shredding path); policy-only residency (rejected — unenforceable);
  backups without a drill (rejected — untested restores routinely fail).

## 23. Test determinism for a non-deterministic dependency

- **Decision**: A **recorded/fake provider** implementing the same normalized
  stream contract (including truncation, stall, malformed-stream, and failover
  paths) backs the correctness suite; fixtures are versioned with the contract;
  and the transcript-hygiene invariants are covered by **property-based tests**
  over generated event sequences. Live-model calls are confined to the eval
  suite.
- **Rationale**: Roughly 120 planned test tasks depend on model behavior. Without
  a deterministic double they are either flaky or they bill a live provider on
  every CI run — and the paired `tool_use`/`tool_result` rule is a total
  invariant over all histories, which example-based tests sample rather than
  prove (FR-097).
- **Alternatives considered**: Live model in CI (rejected — cost and flake);
  mocking at the HTTP layer per provider (rejected — re-couples tests to vendor
  wire formats the abstraction exists to hide).

## 24. Delivery sequencing: evals and the MVP cut line

- **Decision**: Move the ~20-case eval set, judge, and CI gate into the
  **Foundational** phase, before the first behavior-bearing slice; and record an
  explicit MVP cut line in the plan with everything else marked deliberately
  deferred.
- **Rationale**: The reviewed task order placed the eval gate after three phases
  of kernel, surface, and trust work, leaving the highest-effect-size window
  unmeasured and contradicting both the constitution and the "no evals early"
  anti-pattern (FR-043). Separately, a 97-FR / 8-surface / 4-topology plan
  starting from zero code is precisely the "speculative future-proof rig"
  anti-pattern unless the deferral is explicit — the spec stays the north star,
  the plan states what ships first.
- **Alternatives considered**: Keeping evals in the cost/observability phase
  (rejected — measurement must precede the changes it grades); cutting the spec
  scope (rejected — the spec is the target architecture; sequencing, not scope,
  is the lever).

---

## 25. Comparative gap-closure: catalog trust, token audience, stuck-detection, hybrid classification

- **Decision**: A 2026 comparative pass against Claude Code, OpenHands, SWE-agent,
  OpenAI Agents SDK, OpenClaw/GoClaw, Hermes, and the OWASP Agentic Security
  Initiative Top 10 closed four specific gaps: (1) tool/connector/MCP descriptors
  are now scanned for injected instructions at catalog admission and on every
  version bump, not just vetted for provenance and treated as untrusted at runtime
  (FR-113); (2) every connector/MCP token is minted audience-/resource-restricted
  to its own server, never a tenant-wide credential, closing a confused-deputy/
  token-passthrough path the vault-handle model alone didn't close (FR-114); (3)
  the FR-025 stuck-detection heuristic is now eval-gated against a negative-case
  set and escalates from a logged, non-terminal `stuck_suspected` signal to a hard
  terminate only on a corroborating second trip (FR-115); (4) the per-invocation
  safety classifier (Gate 3) is specified as a hybrid deterministic-rule-then-model
  classifier with a fail-closed timeout, rather than leaving the mechanism
  unstated (FR-116).
- **Rationale**: (1) and (2) are named 2026 attack classes ("tool poisoning" and
  "confused deputy via token passthrough") that this platform's existing controls
  don't structurally prevent — FR-078 vets what a server claims to be and FR-070
  treats what it returns as untrusted, but neither scans the descriptor text
  itself or restricts the token's audience, which is exactly where the named
  attacks land. (3) is a documented lesson from a comparable project: SWE-agent
  tried and abandoned a code-based loop detector for an unacceptable
  false-positive rate against legitimate retry-with-variation sequences; shipping
  FR-025 without the same evaluation discipline applied to prompts/models risks
  repeating that mistake at the cost of discarded partial work. (4) mirrors Claude
  Code's own documented hook design (fast deterministic checks first, slow AI
  classification only for the ambiguous remainder) and forecloses two failure
  modes symmetrically: a rules-only Gate 3 under-blocks novel/obfuscated attacks,
  while a model-only Gate 3 puts a metered, non-deterministic call on every tool
  invocation's hot path.
- **Alternatives considered**: Scanning only at runtime via the Rule of Two
  (rejected for (1) — a poisoned description is read by the model before any
  runtime taint check ever fires); relying on the vault handle alone without
  audience restriction (rejected for (2) — the handle still unlocks more than the
  one call it was injected for if the token itself is unscoped); a single hard
  terminate on first stuck-heuristic trip (rejected for (3) — indistinguishable
  from the false-positive failure mode SWE-agent already reversed); a model-only
  or rules-only Gate 3 (rejected for (4) — each fails in the direction the other
  covers).

---

## 26. Durable state: three artifacts, write-ahead effects, and replay vs fork

- **Decision**: Separate the three durable artifacts the design had collapsed into
  one — a **Condensation** (model-facing context compaction, FR-015), a
  **Checkpoint** (machine-facing resume record carrying the in-flight claim, held
  reservation, sandbox handle, pending approval digest, provider request id, open
  delegations, and harness digest, FR-024/FR-126), and a **Snapshot** (disposable
  projection cache that bounds hydration, FR-126). Commit the exactly-once
  **idempotency claim write-ahead**, before a state-changing effect leaves the
  process, and resolve an `in_flight` claim on resume by provider probe or human
  decision, never by re-execution (FR-127). Define three named operations —
  `replay` (pure), `resume` (same run), `fork` (new run from seq N with overrides
  and effects disabled) — instead of one undifferentiated "replay" (FR-128). Pin a
  single **harness digest** covering system prompt, tool catalog, skills, and
  safety/approval policy versions, doubling as the cache-prefix identity (FR-129).
  Treat compaction as eval-gated, chain-bounded, cache-ordered, and
  latency-declared (FR-130).
- **Rationale**: The durable-execution literature names the failure this closes as
  "the dangerous middle": a checkpoint records *where a run was*, never *whether
  the payment was taken* — so a dedup record written after a successful effect
  protects against a retried call but not a crash during one, and neither a
  compaction nor a checkpoint can answer the question. Version drift is the other
  named replay failure: a step that depends on model version, prompt text, tool
  schema, retrieval index, memory snapshot, sandbox image, and policy rules
  diverges on replay if any one moves, which is why the harness digest pins the
  four determinants FR-088 didn't. Compaction is lossy by construction and
  repeated: published probe-based comparisons score every method 2.19–2.45/5.0 on
  artifact tracking while all achieve 98–99% compression — so compression ratio is
  not a quality measure — and a long run compacts 10–100 times, compounding
  degradation that no single-compaction benchmark captures. Serving a
  pre-compaction cached prefix into a post-compaction turn is a real, shipped bug
  class (Claude Code v2.1.62), which is why the cache boundary at a `condensation`
  is ordered and regression-tested rather than assumed.
- **Comparison set**: SWE-agent's `.traj` is versioned and records
  fully-qualified component names precisely so a replay reconstructs the same
  harness — the same instinct as the harness digest, at file granularity.
  OpenHands splits conversation metadata from the event store and paginates event
  pages, the same hydration concern the Snapshot answers. OpenClaw explicitly
  declines to replay on sequence gaps ("clients refresh state and continue"),
  which is a defensible choice for a single-operator gateway and not available to
  a platform whose audit and cost attribution derive from the log. None of the
  comparison set separates a compaction artifact from a resume record, and none
  commits an idempotency claim write-ahead — this platform's event-sourced base
  makes both cheap, so the divergence is a strict improvement rather than a
  trade-off.
- **Alternatives considered**: One `Checkpoint` carrying `condensed_state` for
  both purposes (rejected — the two have different consumers, retention, and
  failure modes, and the conflation hides that a compaction cannot answer whether
  an effect completed); dedup on success only (rejected — leaves an unresolvable
  window across a crash mid-effect); re-executing an `in_flight` claim on resume
  (rejected — duplicates payments and sends); discarding it (rejected — loses the
  effect silently); a single "replay" verb (rejected — the incident runbook needs
  a *fork* with effects disabled, and calling it replay invites a re-execution
  against production); compaction quality measured by compression ratio (rejected
  — indistinguishable ratios hide materially different information loss).

## 27. Ecosystem integration: ports, optionality, and the authority boundary

- **Decision**: Third-party frameworks attach through the ports that already
  exist for the platform's own implementations, are selectable by configuration,
  and are **optional** — the full suite passes with all of them disabled. Each is
  bound by one authority boundary: an integration may supply transport, capacity,
  storage, or presentation, and may never become the routing authority, the
  cost-ceiling authority, the source of truth, the release gate, the audit
  record, or a path to content (FR-131). A **model gateway** (LiteLLM,
  OpenRouter, cloud gateways, self-hosted vLLM/Ollama) attaches as a `Provider`
  adapter with gateway-side aliasing, substitution, and fallback disabled and one
  pinned snapshot per request (FR-132). An **observability backend** (Langfuse,
  Arize/Phoenix, Braintrust, Grafana/Tempo, Datadog) attaches **only** through the
  platform's OTLP export so the FR-117 allowlist applies; vendor SDKs and
  auto-instrumentation are prohibited (FR-134). **Eval/dataset platforms** may
  host corpora and scores but never the gate (FR-135). **Durable-execution
  engines** may implement the queue or plan-runner port with the log remaining
  truth, approvals remaining digest-bound, and the write-ahead claim remaining the
  exactly-once mechanism; **prompt-management tools** may author but never
  hot-swap at runtime (FR-136). Every adapter is admitted by a **conformance
  suite** producing a recorded per-adapter capability matrix (FR-133).
- **Rationale**: The overlap is the hazard. A model gateway is adopted precisely
  because it *also* does routing, fallbacks, budgets, and caching — the four
  things this platform holds constitutionally (FR-037/FR-076, FR-048, FR-083,
  FR-013). Handing any of them over does not merely duplicate a feature; it
  relocates an audited, deterministic control into a component the platform does
  not own and cannot attest to. The same reasoning applies asymmetrically to
  observability: an LLM-tracing vendor's most valuable features (playground,
  judge-over-traces, dataset-from-trace) all require content, and its access
  control is its own — a read in its interface produces no FR-118 receipt — so
  the integration is *deliberately* limited to the half that works without
  content, and the content-bearing route is the governed FR-125 export instead.
  Optionality matters for the same reason config-not-forks does: an integration
  that becomes a prerequisite is a fork of the deployment story.
- **Observed failure modes that shaped FR-132/FR-133** (these are not
  hypotheticals — they are current, documented behaviors in a widely used
  gateway): provider-native cache-read counts not normalized into the gateway's
  usage report, which would silently make the >90% cache-read gate unmeasurable
  while every dashboard still rendered; router cache affinity held for a fixed
  short window while the provider's cache lives far longer, so a later turn with
  the same prefix is re-routed to a cold backend and pays full input price; and a
  gateway parameter mode that drops a provider's `thinking` parameter, changing
  model behavior for that turn without an error. Each is invisible from the
  platform's side unless conformance is *tested and recorded per adapter* — which
  is why FR-133 records a capability matrix rather than a boolean, and why FR-132
  withdraws the cache-read claim on a path that cannot measure it rather than
  estimating around the gap.
- **Alternatives considered**: Letting the gateway route (rejected — breaks
  data-label routing, snapshot pinning, and price-book attribution at once, and
  does so silently); relying on gateway-side budgets (rejected — a ceiling in a
  component the platform does not own is not a ceiling, FR-083); vendor SDK
  instrumentation for a richer backend experience (rejected — bypasses the
  FR-117 allowlist, which is the single choke point that keeps telemetry inside
  the erasure boundary); hosting the eval gate on an external platform (rejected
  — reopens spec-gaming and makes a vendor outage a release decision); adopting a
  durable-execution engine's history as the audit record (rejected — it journals
  scheduling, not agent behavior, and cannot answer whether an external effect
  occurred); a boolean "supported/unsupported" per adapter (rejected — real
  adapters are *partially* conformant, and it is the undeclared partial support
  that produces metrics nobody is measuring).

## 28. Evaluation as measurement: trials, environment, and the production gap

- **Decision**: The gate becomes a **statistical decision over repeated trials**
  — k trials per case, `pass^k` for regression and safety classes and `pass@k`
  only where one success is genuinely the requirement, a confidence interval per
  case, a regression defined as downward interval separation rather than a
  flipped trial, a three-valued verdict in which `inconclusive` never resolves as
  `pass`, and a published minimum detectable effect (FR-137). The environment is
  pinned as a second digest alongside the harness digest, comparison across
  differing digests is refused, trials run on **cold** sandboxes from a declared
  memory/skill baseline, and infra errors are excluded from the denominator
  rather than scored as agent failures (FR-138). The corpus splits into
  **regression / capability / safety / negative** classes with distinct
  thresholds and blocking semantics, plus a graduation and retirement path
  (FR-139). Quality is measured in production through an **in-boundary online
  scorer** emitting structure-free scores through the FR-117 allowlist, feeding
  drift alerting and a rollout guardrail (FR-140). The judge is a pinned,
  calibrated, cross-family instrument (FR-141). Trajectory regressions are
  reached through **fork-based cases** on FR-128 (FR-142). Every behavior-bearing
  artifact carries its own suite, and the corpus re-runs on a schedule
  independent of any change (FR-143). Graders follow an explicit selection rule
  and cases meet an authoring bar (FR-144); efficiency joins the gate (FR-145);
  and held-out protection is mechanized *and measured* (FR-146). `EvalSuite`,
  `EvalCase`, `EvalRun`, `EvalEnvironmentDigest`, `Judge`, and `JudgeCalibration`
  become first-class entities, because every `eval_run_id` in the data model
  previously pointed at nothing.
- **Rationale**: Four forces decide this shape. (1) **The noise floor exceeded
  the gate's resolution.** Twenty binary cases scored once means one flip moves
  the rate five points, while a controlled study of agentic coding evals measured
  *infrastructure configuration alone* — container CPU/memory bands, concurrency,
  egress — swinging scores about six points, with error rates moving 5.8% → 2.1%
  purely from the ceiling chosen and pass rates drifting with provider load by
  time of day. A gate that cannot resolve the regression it promises to catch
  will block clean changes and pass real ones, and teams learn to route around
  it. The constitution already said "track pass rates over N runs"; the
  requirement had silently dropped N. (2) **One threshold cannot serve two
  jobs.** Published practice separates capability evals (low pass rates by
  design, measuring new ability) from regression evals (held near 100%,
  protecting existing behavior), with saturated capability cases graduating into
  the regression suite. A single ≥90% bar makes adding a genuinely hard case a
  deploy-blocking act, which selects for easy cases — a gaming path opened by the
  gate's own shape rather than by the agent. (3) **The platform had no quality
  signal in production.** FR-095's golden signals are all system health; nothing
  moved when answers got worse. The field's answer — a vendor judge over
  production traces — is exactly what FR-117 and FR-134 close, and the design
  said so without saying what replaces it. A judge *inside* the boundary emitting
  only a score resolves the tension completely: a float is not content, so it
  passes the allowlist untouched and the erasure attestation survives. (4)
  **Harness and environment are independent confounders, each larger than the
  change under test.** A controlled harness benchmark attributes ~29.4 points of
  Pass@1 to model choice and ~27.4 points to harness choice under a fixed model,
  with the same backbone spanning 19.1% → 73.4% between a minimal and a full
  adapter. FR-129 already pinned configuration; nothing pinned substrate.
- **Comparison set**: The *governance* of evaluation here was already ahead of
  the set — the gate is Foundational (§24) where the reference implementations
  added evals after shipping, held-out graders and spec-gaming detection are
  designed in, behavior-bearing heuristics are gated like prompts (§25, §26), and
  the production→eval path is consent-governed (FR-125). What the set does better
  is *measurement*, and each borrowing is specific. **Anthropic's agent-eval
  guidance** supplies the capability/regression split, `pass@k` vs `pass^k`, the
  20–50-case starting size, per-trial environment isolation, balanced
  positive/negative sets, the "0% means a broken task" reading, and the
  Swiss-cheese layering (offline evals + production monitoring + A/B + manual
  review) that exposed the missing production layer; its infrastructure-noise
  study supplies the guaranteed-versus-ceiling resource split and empirical band
  calibration. **Hermes** supplies two patterns adopted directly: per-skill YAML
  suites gating each skill's own upgrade rather than one monolithic corpus, and a
  scheduled re-run — and its probe-based compression eval is already the model
  for FR-130. **SWE-agent** supplies replay-from-recorded-trajectory as the
  precedent for FR-142's fork-based cases; the platform's version is cheaper,
  because `fork` (FR-128) already branches from a durable log with effects
  disabled. **OpenHands** supplies the multi-benchmark harness shape and the
  observation that pass-to-pass regression results are executed but not surfaced
  — the reason FR-139 gives regression its own class rather than folding it into
  a headline rate. **The OpenClaw/GoClaw harness benchmarks** (Claw-SWE-Bench,
  WildClawBench, ClawProBench) supply the fixed-adapter protocol and the
  harness-versus-model attribution that motivates FR-138, plus deterministic
  grading with repeated-trial reliability. **Claude Code's** telemetry discipline
  was already adopted in §11; its eval history supplies the negative lesson that
  a reasoning-effort change can pass internal evals and still degrade in the
  field, which is FR-140's justification rather than a footnote.
- **Alternatives considered**: Keeping the single-run gate and raising the
  threshold (rejected — raises the false-block rate without improving resolution;
  the problem is variance, not the bar); running k trials but reporting the mean
  (rejected — hides the consistency question that `pass^k` exists to ask, and a
  safety case that passes 8 of 10 has failed); defaulting `inconclusive` to
  `pass` to keep CI moving (rejected — it converts an unmeasured change into a
  shipped one, which is the exact failure the gate exists to prevent, and it is
  the path of least resistance so it must be closed by construction); serving
  eval trials from the warm pool for speed (rejected — pooled state produces
  correlated failures and inflated scores, a documented failure mode, and the
  latency it saves is spent on measurement nobody can trust); a vendor
  observability platform's judge and dataset features for the production layer
  (rejected — it is content leaving the boundary into a system whose reads leave
  no FR-118 receipt, the same reasoning as §27, and the in-boundary scorer gets
  the capability without the exception); hosting the corpus *and* the gate
  externally (rejected in §27 and unchanged — the corpus may be hosted, the
  decision may not); one global corpus for skills, tools, plans, and policies
  (rejected — it measures the platform, not the artifact, and does not scale with
  a catalog); grading trajectories against a required step sequence (rejected —
  penalises valid alternative solutions, the brittleness that makes process
  grading unpopular for good reason; an acceptable-action set keeps the signal
  without the brittleness); judging with the same model family as the agent
  (rejected — systematic self-flattery); and treating efficiency as a report-only
  metric (rejected — on a platform whose stop signal is cost, an ungated
  efficiency regression is an incident that ships with a green check).

## 29. Channels, tools, and skills

- **Decision**: Tools get fully-qualified identity (`{namespace}/{name}@{version}`)
  with one owning source per namespace, collision refused at admission, and a
  governance-signed alias map; deferred disclosure is reconciled with the harness
  digest by pinning a **catalog manifest** (the resolvable universe) rather than
  the materialized descriptor set, with each load a `tool_loaded` event and the
  selector eval-gated and measured; sandbox-originated tool calls are permitted
  **only** through an in-sandbox broker that re-enters the pipeline at step 1, and
  a direct network path from sandbox code to a connector is a prohibited egress
  route; MCP listing caches are advisory with the descriptor digest re-verified at
  use, and server-initiated user input is an FR-110 input request. Skills become
  signed, content-addressed **bundles** whose every file passes the FR-113 scan,
  whose scripts register as tools or the bundle is refused, admitted through one
  gate for all four origins with provenance/signature/pinned version for imports,
  capability-**narrowing** only, and disclosed in three bounded tiers. Surfaces
  publish a conformance-tested **capability descriptor** that approval routing
  filters on; authority is the turn-submitting principal rather than the
  conversation's opener; outbound delivery ships through a durable outbox; and
  agent-principal ingress is its own admission class (FR-147–FR-159).
- **Rationale**: The review found the strong gates pointed at the safest sources.
  FR-021 gated *agent-proposed* skills — the platform's own agent — while a
  marketplace import entered as configuration, against a supply chain where a
  42,447-skill study found 26.1% carrying at least one vulnerability (13.3% data
  exfiltration, 11.8% privilege escalation) and 157 confirmed malicious, a
  separate study found injection in 36% of skills and 1,467 malicious payloads,
  and a February 2026 campaign distributed 30+ malicious skills to users of three
  major agent products; publishing requires only a `SKILL.md` and a week-old
  account. FR-113 scanned tool descriptors but not skill bodies, which the model
  reads identically and which are typically an order of magnitude larger. Skills
  bundling executable scripts measure at 2.12× the vulnerability rate of
  instruction-only skills, and the ecosystem format that ~40 products adopted
  after the standard opened (2025-12-18) is a directory that may carry them.
  Separately, FR-062 and FR-129 forbade each other — one requires the tool set to
  change mid-run, the other pins "the resolved tool-catalog contents" and FR-088
  makes a mid-run digest change a defect. And the sandbox was the one place a
  capability could be reached with no permission chain at all, precisely as the
  code-execution pattern (GA February 2026, ~98% token reduction on the reference
  workload) invites.
- **Comparison set**: **Tool execution came out ahead of the entire set** — the
  single pipeline, the published total permission order, and the
  digest/claim/receipt trilogy have no equivalent in Claude Code, OpenHands,
  SWE-agent, OpenClaw/GoClaw, or Hermes — and **skill governance likewise**, since
  the ecosystem's gate is a marketplace listing. The borrowings are specific.
  **Claude Code** supplies deferred tool loading at a declared context-budget
  threshold (the trigger FR-062 lacked) and the plugin model in which skills,
  hooks, subagents, and MCP servers are one versioned installable unit — the
  packaging insight behind treating a skill as a bundle rather than a row.
  **Anthropic's advanced tool use** supplies Tool Search and Programmatic Tool
  Calling as the two patterns FR-148 and FR-149 must answer. **MCP `2026-07-28`**
  supplies cacheable listings (a TOCTOU on the admission scan), multi-round-trip
  server-initiated input (an inbound path to the human), and structured results.
  The **Agent Skills standard and the security literature that followed it**
  supply the three-tier disclosure model, the executable-bundle threat, and the
  graduated trust-tier posture that replaces binary admit/reject. **SWE-agent and
  OpenHands** supply the agent-computer-interface lesson that a small, well-shaped
  tool set beats a large one — the argument for measuring selection accuracy
  rather than only catalog coverage. **OpenClaw/GoClaw** supply per-thread channel
  isolation and the channel allowlist as a first-class control. **Hermes** supplies
  the per-skill suite already adopted in FR-143. The **2026 protocol stack**
  (MCP / A2A / AG-UI) supplies the third surface class the spec never named.
- **Alternatives considered**: Bare tool names with last-writer-wins resolution
  (rejected — shadowing is a documented attack and connection order is not a
  policy); pinning the materialized descriptor set into the harness digest
  (rejected — it makes FR-062 unimplementable and every load a reproducibility
  defect); allowing sandbox code to call connectors directly for the token savings
  (rejected — it is the absence of the control surface, not an optimization, and
  every guarantee in the trust section is void on that path); trusting server
  cache hints (rejected — a listing that can change after admission makes the scan
  a formality); treating a skill as reviewed text (rejected — the ecosystem
  artifact carries executable code and the measured malicious population is not
  hypothetical); admitting unsigned imports at a reduced trust tier (rejected — a
  reduced tier for an unauthenticated artifact is a decision made about a publisher
  nobody identified); honouring a skill's declared tools as a grant (rejected — it
  reintroduces the widening lever FR-111 closes, reachable from injected content);
  binding a shared conversation to whoever opened it, the prevailing practice
  (rejected — it runs one participant's instructions under another's authority and
  makes separation of duties unevaluable); sending outbound messages inline
  (rejected — a duplicated reply is user-visible and a lost escalation is
  indistinguishable from a human who declined to answer); and admitting agent
  callers on the ordinary service-token path (rejected — it produces an
  un-taint-tracked instruction channel that can also answer the platform's own
  questions).

## Resolved unknowns summary

| Technical Context item | Resolution |
|------------------------|------------|
| Kernel loop shape | Typed-union async-generator loop (§1) |
| Provider dependency | One abstraction + normalized stream + failover (§2) |
| Storage / isolation | Postgres + RLS, Redis, object storage (§3) |
| Scale/concurrency | Durable queue (NATS JetStream default) + stateless workers, Redis session-key lock (§4) |
| Sandbox | Warm per-tenant pool, TTL/reclamation, gVisor default across Docker and K8s (§5) |
| Context/cache | Two-zone prompt, off-loop structured compaction (§6) |
| Cost/routing | Per-turn meter + ceilings + deterministic two-axis routing (§7) |
| Memory/skills | File-first, per-tenant, gated promotion (§8) |
| Reliability | Classify/resume/circuit-break/stuck/rainbow (§9) |
| Security | Layered fail-closed, Rule of Two, vault, receipts (§10) |
| Observability/evals | Content-free signal class derived from the log; turn-scoped linked traces; bidirectional trace↔event join; versioned attribute model mapped to pinned `gen_ai.*`; fixed metric labels + exemplars; audited content-access grants; eval gate in CI (§11) |
| Deployment | Control/data-plane split, config-not-forks (§12) |
| Isolation under pooling | Transaction-local scope; test through PgBouncer (§13) |
| Erasure vs append-only | Crypto-shredding; never delete or rewrite events (§14) |
| Audit integrity | Hash chain + external anchor + sign-only key + verifier (§15) |
| Ceiling enforcement | Reserve-then-reconcile on an atomic counter (§16) |
| Cost measurement | Token classes split; versioned price book (§17) |
| Event model | Full taxonomy + `schema_version` + projections (§18) |
| Rule of Two inputs | Declared per-tool taint + sanitization boundary (§19) |
| Run lifecycle | Steer / cancel / resume first-class; determinants persisted (§20) |
| Webhook ingress | Provider signature + replay window + flood limit (§21) |
| At-rest / residency / DR | Per-tenant keys + BYOK; pinned placement; RPO 5m / RTO 4h (§22) |
| Test determinism | Recorded/fake provider + property tests (§23) |
| Sequencing | Evals foundational; explicit MVP cut line (§24) |
| Catalog/token/reliability gap-closure | Descriptor injection scan; audience-bound tokens; eval-gated stuck detection; hybrid Gate-3 classifier (§25) |
| Durable state artifacts | Condensation / Checkpoint / Snapshot separated; write-ahead idempotency claim; replay vs resume vs fork; harness digest; eval-gated compaction (§26) |
| Ecosystem integration | Optional adapters behind existing ports; one authority boundary; gateway as transport not router; OTLP-only observability; conformance-recorded capability matrix (§27) |
| Channels / tools / skills | Qualified tool identity with one namespace owner; catalog manifest pinned instead of the materialized set; broker-only sandbox tool calls; signed skill bundles under one admission gate, narrowing-only; surface capability descriptors driving approval routing; per-turn principal authority; delivery outbox; agent-principal ingress class (§29) |
| Evaluation measurement | k-trial statistical gate with `pass^k`/intervals and a three-valued verdict; environment digest + cold sandboxes; suite classes with graduation; in-boundary online scorer + rollout guardrail; pinned calibrated cross-family judge; fork-based trajectory cases; per-artifact suites + scheduled re-run; efficiency in the gate; measured held-out gap; eval entities made first-class (§28) |

**No `NEEDS CLARIFICATION` remain.** Proceed to Phase 1.
