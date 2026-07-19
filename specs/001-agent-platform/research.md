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
  `prompt_too_long`, `hook_stopped`, `approval_expired`).
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
  reclamation on terminal/stuck state, and per-tenant caps. Firecracker/gVisor for
  hostile multi-tenant SaaS; lighter containers for single-tenant/BYOC where the
  tenant boundary is the whole stack. Git worktrees per session for workspace
  isolation.
- **Rationale**: Cold-start per run dominates tail latency; a warm pool trades
  small idle cost for a large p95 win. The sandbox is the trust boundary; TTL +
  reclamation prevent cost and security leaks (FR-047; Constitution V).
- **Alternatives considered**: Cold container per run (rejected — seconds of tail
  latency); shared sandbox (rejected — breaks the trust boundary).

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

- **Decision**: OpenTelemetry spans over the append-only event log; per-turn
  token/cost/latency spans; monitor decision structure without reading conversation
  content, keeping prompts inspectable for authorized debugging. Eval set (~20 real
  cases) with an LLM-as-judge rubric + end-state checks runs in CI and gates any
  prompt/tool/model/skill change; ship only at ≥90% pass AND zero regressions;
  held-out grader tests the agent cannot edit.
- **Rationale**: You can't operate what you can't see; agents are non-deterministic
  between runs. Governed, eval-gated config separates a demo from a safely-changeable
  system (Constitution IX + Workflow section; FR-040, FR-042–FR-044).
- **Alternatives considered**: Content-reading observability (rejected — privacy/
  compliance); ship-on-green-only without regression check (rejected — allows
  silent regressions, spec-gaming).

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

## Resolved unknowns summary

| Technical Context item | Resolution |
|------------------------|------------|
| Kernel loop shape | Typed-union async-generator loop (§1) |
| Provider dependency | One abstraction + normalized stream + failover (§2) |
| Storage / isolation | Postgres + RLS, Redis, object storage (§3) |
| Scale/concurrency | Durable queue (NATS JetStream default) + stateless workers, Redis session-key lock (§4) |
| Sandbox | Warm per-tenant pool, TTL/reclamation (§5) |
| Context/cache | Two-zone prompt, off-loop structured compaction (§6) |
| Cost/routing | Per-turn meter + ceilings + deterministic two-axis routing (§7) |
| Memory/skills | File-first, per-tenant, gated promotion (§8) |
| Reliability | Classify/resume/circuit-break/stuck/rainbow (§9) |
| Security | Layered fail-closed, Rule of Two, vault, receipts (§10) |
| Observability/evals | OTel structure-only + eval gate in CI (§11) |
| Deployment | Control/data-plane split, config-not-forks (§12) |

**No `NEEDS CLARIFICATION` remain.** Proceed to Phase 1.
