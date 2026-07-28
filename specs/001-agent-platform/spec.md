# Feature Specification: Production-Grade AI Agent Platform

**Feature Branch**: `001-agent-platform`

**Created**: 2026-07-17

**Status**: Draft

**Input**: User description: "build agent in draft-plan.md"

## Overview

Build **one** model-agnostic AI agent platform — a single reliable agent kernel wrapped in an engineered harness, exposed through thin surface adapters, fronted by a control plane, and grounded in a trust surface — that serves customers from a 5-person startup to a 50,000-person enterprise via configuration and connectors, never per-customer code forks. The platform aims to be more reliable, more cost-efficient, more secure, and more scalable than the current generation of agent products by unifying their best ideas on one immutable, event-sourced kernel and adding the control plane, trust surface, and cost governance that separate a demo from a system a security-conscious enterprise will sign.

## Clarifications

### Session 2026-07-17

- Q: What availability/uptime SLA should the platform target? → A: 99.9% control plane / API, 99.5% agent-run completion (enterprise-standard baseline)
- Q: What queue-wait / latency SLA should submitted runs meet? → A: p95 queue-wait < 5s interactive / < 60s batch; first token < 2s interactive (tiered)
- Q: What happens when a required human approval is never answered? → A: Fail-closed — the approval expires as a denial after a configurable TTL and the run terminates with a typed `approval_expired` reason (audited)
- Q: What eval-gate pass threshold must a change clear in CI to ship? → A: ≥90% pass AND zero regressions versus the current baseline (no previously-passing case may regress)
- Q: What default retention window applies to per-tenant memory? → A: 90-day default, tenant-overridable (regulated tiers may tighten or extend by config)

### Session 2026-07-27 (design review — production-readiness gaps)

- Q: How is a right-to-erasure request satisfied against an append-only, immutable event log? → A: Crypto-shredding — event payloads are envelope-encrypted per tenant (per erasure subject at regulated tiers) and erasure destroys the key; the event sequence, its digests, and the audit chain remain intact and verifiable (FR-080, FR-089)
- Q: What makes the audit log tamper-evident, given the writer holds the MAC key? → A: Per-session hash-chaining plus periodic external anchoring of the chain head, sign-only key custody (KMS/HSM), and a scheduled verifier that alerts on a break or sequence gap (FR-081)
- Q: How is a hard cost ceiling enforced when usage is only known after a turn completes? → A: Reserve-then-reconcile — each turn reserves its worst-case cost against an atomic per-tenant counter before the model call, the worker enforces a local hard per-run budget synchronously, and actuals reconcile the reservation afterwards (FR-083)
- Q: What happens when a required approval expires — does the whole run die? → A: The expiry denies **the action** and is returned to the loop as a typed denial so the agent may replan; the run terminates with `approval_expired` only when it cannot proceed without that action (FR-036)
- Q: Does tenant isolation survive a connection-pooling tier? → A: Tenant scoping MUST be transaction-local (`SET LOCAL` / `SET ROLE LOCAL`); session-level scoping is prohibited, and the isolation test MUST run through the production pooling tier (FR-039)
- Q: What is the recovery objective for the event log and audit chain? → A: RPO ≤ 5 minutes and RTO ≤ 4 hours for the control plane and event log, proven by a rehearsed restore drill at least quarterly (FR-090)

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Complete a real task through a reliable agent (Priority: P1)

An end user submits a request (e.g., "triage this bug and propose a fix") to the agent. The agent works through an observe → think → act loop, calling tools as needed, and returns a result whose completion is verified against explicit acceptance criteria rather than self-declared. The run stops when it reaches a cost ceiling, completes, or hits a bounded backstop — never runs away.

**Why this priority**: This is the irreducible core — a reliable single-agent loop that completes tasks under a cost bound. Without it, nothing else has value. It is a viable MVP on its own: a user can get real work done safely.

**Independent Test**: Give the agent a multi-turn task requiring at least one tool call; confirm it holds the conversation, pairs every tool invocation with a result, stops on the configured cost cap, and reports a typed terminal reason — all 8 values must be exhaustively handled: `completed` / `max_turns` / `cost_exhausted` / `error` / `aborted` / `prompt_too_long` / `hook_stopped` / `approval_expired` (per FR-004 and kernel-abi.md).

**Acceptance Scenarios**:

1. **Given** a user request that needs a tool, **When** the agent runs, **Then** it invokes the tool, incorporates the result, and returns a completed answer with a stated terminal reason.
2. **Given** a run that reaches its per-task cost ceiling, **When** the ceiling is crossed, **Then** the run halts with an explicit `cost_exhausted` reason rather than continuing or failing silently.
3. **Given** a tool invocation that errors or is cancelled, **When** the loop continues, **Then** a synthetic result is recorded for that invocation before the next model call so the transcript stays valid.
4. **Given** a completed run, **When** success is claimed, **Then** it is verified against explicit acceptance criteria (e.g., tests pass, build green, schema validates), not self-declared.
5. **Given** a task that needs local files or a command, **When** the agent runs, **Then** it uses built-in workspace-restricted filesystem tools and a sandboxed shell that cannot escape the session's workspace or reach another tenant, with each command judged by a per-invocation safety check on parsed input.

---

### User Story 2 - Reach the same agent from many surfaces (Priority: P2)

A user interacts with the same underlying agent from whichever surface they already use — command line, chat (Slack/Teams), a web app, a REST/gRPC API, email, or a scheduled/cron trigger. Behavior and guarantees are identical across surfaces; each surface only translates input and output.

**Why this priority**: The platform's promise is "one loop, many surfaces." Consistent multi-surface access multiplies reach without multiplying behavior or bugs, but it depends on the core loop (P1) existing first.

**Independent Test**: Run the same task through at least three surfaces (e.g., CLI, API, chat) and confirm identical control flow, identical safety/cost guarantees, and no surface-specific forks of agent logic.

**Acceptance Scenarios**:

1. **Given** the same task submitted via CLI and via chat, **When** both run, **Then** both follow the same loop and produce equivalent results and terminal reasons.
2. **Given** a new surface is added, **When** it is configured, **Then** it works as a thin adapter with no change to the agent kernel.
3. **Given** a long-running task submitted via a surface, **When** the user awaits results, **Then** the surface streams or polls progress without holding a blocked connection.

---

### User Story 3 - Operate safely with enterprise trust (Priority: P2)

A security/compliance owner needs every agent action to be attributable, isolated per tenant, scoped to the calling user's permissions, and protected against prompt-injection abuse. Secrets never appear in prompts; one tenant can never reach another's data, secrets, budgets, or workspaces; high-impact actions require human approval.

**Why this priority**: The enterprise tax — multi-tenancy, audit, isolation, and safety — is what gets a deal signed and is a day-one requirement (retrofitting it is a rewrite). It is high value but builds on a working, multi-surface loop.

**Independent Test**: Run tasks for two tenants and confirm complete data/secret/budget isolation at the data layer; confirm every action is attributable to a user + tenant in an immutable audit log; confirm a high-impact action is blocked pending human approval.

**Acceptance Scenarios**:

1. **Given** two tenants running concurrently, **When** either agent queries data, **Then** it can only ever reach its own tenant's rows, secrets, and workspace — enforced at the data layer, not just the application.
2. **Given** any mutating action, **When** it executes, **Then** an immutable, tamper-evident audit record ties it to a specific user, tenant, tool, inputs, result, and timestamp.
3. **Given** a tool that needs a credential, **When** it runs, **Then** the credential is injected at execution time from a vault and the model only ever sees a handle, never the secret.
4. **Given** a payment, deletion, external send, or production change, **When** the agent attempts it, **Then** it is gated by scoped human approval before proceeding.
5. **Given** a flow that would combine processing untrusted input, accessing private data, and changing state/communicating externally, **When** all three would occur in one session, **Then** the platform requires human approval (no more than two of the three proceed unattended) — decided from each tool's declared taint metadata, failing closed when metadata is absent.
6. **Given** an audit log and an attacker who can write to it, **When** a receipt is modified, deleted, or reordered, **Then** hash-chain verification fails and alerts, because the chain head is anchored outside the writing system and the signing key is sign-only.
7. **Given** a right-to-erasure request for a subject, **When** it is executed, **Then** the subject's content becomes unrecoverable through key destruction while the event sequence replays and the audit chain still verifies — no event is deleted or rewritten.
8. **Given** a forged or replayed inbound webhook delivery, **When** it arrives, **Then** it is rejected before any adapter translation or kernel invocation, with zero token spend.

---

### User Story 4 - Govern cost and observe behavior (Priority: P2)

A platform/ops owner needs every run's token usage and cost metered per turn and attributed to the requesting task and tenant, with hard per-task and per-tenant ceilings. They can inspect decision structure, latency, and cost without reading private conversation content, and any change to prompts, tools, or models must pass an evaluation gate before release.

**Why this priority**: "Stop on cost, not vibes" and "you can't operate what you can't see" are core operating requirements. This makes the platform affordable and safe to change, but depends on the loop and trust surface.

**Independent Test**: Run a workload and confirm per-turn token/cost metering attributed to task and tenant, enforcement of a per-tenant ceiling, a structure-only trace view, and a CI gate that blocks a prompt/model change failing the eval set.

**Acceptance Scenarios**:

1. **Given** a running task chain, **When** each turn completes, **Then** input and output tokens, latency, and cost are recorded and attributed to that task and tenant.
2. **Given** a tenant that reaches its cost ceiling, **When** the ceiling is crossed, **Then** further runs stop with an explicit cost-exhausted reason and an alert, not a surprise bill.
3. **Given** an operator investigating a run, **When** they open its trace, **Then** they can see decision patterns and per-turn cost/latency/token spans without reading conversation content, and can still inspect the actual prompt/response when debugging is authorized.
4. **Given** a proposed change to a prompt, tool, model, or skill, **When** it is submitted, **Then** it must pass a versioned eval set in CI before it can ship — a gate that exists before the first behavior-bearing slice, and whose correctness suite runs against a deterministic provider harness rather than a live model.
5. **Given** many sessions for one tenant starting simultaneously against a nearly exhausted budget, **When** they run, **Then** the ceiling is never exceeded, because each turn reserves its worst-case cost against an atomic counter before the model call and releases the unused remainder on reconciliation.
6. **Given** a completed period, **When** an owner reviews spend, **Then** cost is attributable per tenant and, within a tenant, per user, agent, and surface, reconciling to the sum of the per-turn records and naming the price-book version used.

---

### User Story 5 - Grow capability through memory and skills (Priority: P3)

Over time the agent gets more capable and cheaper per task: it remembers durable knowledge across sessions (per tenant, retention-bounded), loads reusable procedures ("skills") only when relevant, and can propose new skills after solving a hard problem — which are promoted only through a human/evaluation gate.

**Why this priority**: Compounding capability is a differentiator but not required for a first useful release; it layers onto the core once memory and governance exist.

**Independent Test**: Seed a memory file and a skill, confirm the agent uses them, confirm an agent-proposed skill is never auto-promoted, and confirm memory is scoped per tenant with a retention limit and injection screening.

**Acceptance Scenarios**:

1. **Given** durable knowledge from a prior session, **When** a new session starts, **Then** the relevant memory is injected at session start (taking effect that session, not mid-session).
2. **Given** a library of skills, **When** a task matches one, **Then** only its brief description is always visible and its full content loads on demand.
3. **Given** the agent proposes a new skill after solving a problem, **When** promotion is requested, **Then** it passes an evaluation and human review before it becomes available — never auto-promoted.
4. **Given** memory content, **When** it is injected, **Then** it is scanned for injection/exfiltration patterns first and scoped to the owning tenant with a retention limit.

---

### User Story 6 - Fit any organization by configuration, not forks (Priority: P3)

A new organization is onboarded through configuration and connectors: tenant settings (identity, roles, budgets, region, retention), an agent definition (persona + toolset profile + autonomy level), seeded skills, enabled surfaces, and per-tenant permission-scoped connectors to their systems of record. The same build runs as multi-tenant SaaS, single-tenant, self-hosted in the customer's environment, or a split control-plane/data-plane hybrid — chosen by configuration.

**Why this priority**: Deployment flexibility and config-based onboarding are what make the platform sellable across topologies, but they are meaningful only once the core, trust surface, and governance exist.

**Independent Test**: Onboard a new org with zero kernel changes (config + connectors only), and deploy the same build in at least two topologies (e.g., multi-tenant SaaS and self-hosted) by configuration.

**Acceptance Scenarios**:

1. **Given** a new organization, **When** it is onboarded, **Then** its behavior, tools, skills, surfaces, and connectors are all data/configuration and the kernel is not forked.
2. **Given** a customer with data-residency constraints, **When** they choose "data plane in my environment," **Then** it is a deployment choice, not a code change, and sensitive payloads never leave their boundary.
3. **Given** a connector to a tenant's system of record, **When** the agent uses it, **Then** it is scoped to the calling user's permissions and credentialed per tenant.

---

### User Story 7 - Survive failures, deploys, and scale (Priority: P3)

The platform keeps long-running agents alive through transient failures, provider outages, deploys, and heavy concurrency. Failures are classified before any retry, runs resume from durable checkpoints instead of restarting, deploys never cut a running agent mid-task, and the system serves thousands of concurrent sessions while degrading gracefully under load rather than collapsing.

**Why this priority**: Operational resilience and horizontal scale are essential for production SLAs but come after the platform's behavior and trust guarantees are proven.

**Independent Test**: Interrupt a long task with a simulated crash and confirm it resumes from its last checkpoint; deploy a new version during an active run and confirm the run is not cut over mid-task; drive concurrent load past capacity and confirm admission control and graceful degradation rather than failure.

**Acceptance Scenarios**:

1. **Given** a transient failure, **When** it occurs, **Then** it is classified, logged with a reason, backed off with jitter, and circuit-broken after an identical failing call repeats three times — never silently retried.
2. **Given** a run interrupted mid-task, **When** it is restarted, **Then** it resumes from the last durable checkpoint rather than starting over, preserving partial work.
3. **Given** a stuck run (repeated actions, oscillation, or zero net change), **When** detected, **Then** the loop breaks and the run terminates with a clear reason.
4. **Given** a deploy while agents are running, **When** it rolls out, **Then** in-flight runs are not cut over mid-task.
5. **Given** demand beyond capacity, **When** new runs arrive, **Then** admission control, fair scheduling across tenants, and priority load-shedding keep the system responsive instead of collapsing.

---

### User Story 8 - Connect personal messaging surfaces and systems of record (Priority: P2)

An end user reaches the agent from the consumer messaging apps they already live in — **Telegram** and **Zalo** — and authorizes the agent to act on their own accounts (e.g., **Gmail**, **Google Drive**, **Google Calendar**) through a one-time consent, exactly like the popular LLM assistants. Once connected, the user can do the common tasks those assistants do — "summarize my unread email," "find the contract in my Drive," "schedule a meeting for Thursday and send the invite," "message me on Telegram when it's done" — with every connector scoped to that user's own permissions, tokens vaulted (never shown to the model), high-impact sends gated by approval, and no kernel fork per connector or per surface.

**Why this priority**: Consumer messaging surfaces and per-user personal connectors are what make the platform recognizably useful as a day-to-day assistant (the OpenClaw/Hermes-style experience). It is high value but strictly layers onto the multi-surface loop (US2) and the trust surface (US3: connector catalog, vaulted secrets, delegated identity, Rule of Two, approval) — it adds new adapters and connectors as configuration, never new control flow.

**Independent Test**: From a Telegram (and a Zalo) chat, submit a task and confirm identical control flow / terminal reason to the API surface; complete a per-user OAuth consent for a Google connector and confirm the token is vaulted per `(tenant, user, connector)`, auto-refreshed, and revocable; confirm the model only ever sees a connector handle; confirm a "send email" action is blocked pending approval; confirm an external chat identity is bound to a platform `User` before any action runs.

**Acceptance Scenarios**:

1. **Given** a user messaging the agent from Telegram or Zalo, **When** the message arrives via the surface's webhook, **Then** it is translated by a thin adapter into the same run model and follows the identical loop, guarantees, and terminal reasons as every other surface — no per-surface fork.
2. **Given** a user who has not yet linked a connector, **When** they ask for an action needing it, **Then** the platform initiates a per-user OAuth 2.0 authorization-code (with PKCE) consent, and only after consent stores the resulting tokens in the per-tenant vault keyed by `(tenant, user, connector)`.
3. **Given** a linked connector whose access token has expired, **When** a tool call needs it, **Then** the token is refreshed automatically from the stored refresh token, and a user-initiated revoke immediately removes access.
4. **Given** any connector tool invocation (Gmail/Drive/Calendar/Notion), **When** it executes, **Then** the credential is injected at execution time from the vault and the model only ever sees a handle — never the token — and the action runs within the calling user's own permission scope.
5. **Given** a high-impact connector action (send an email, delete a file, send a calendar invite externally), **When** the agent attempts it, **Then** it is gated by scoped human approval and constrained by the Rule of Two before proceeding.
6. **Given** an inbound message from an external chat identity (Telegram/Zalo user id), **When** it is first seen, **Then** it is bound to a platform `User` within a tenant through a verified linking step, and an unverified/unlinked identity cannot run actions.

---

### Edge Cases

- **Ambiguous model output**: When the model responds in a way the code cannot categorize, the loop branches on a typed classification of the response (tool calls / content / empty), never a fragile string match, and re-prompts a bounded number of times on format errors.
- **Context window overflow**: When context approaches its limit, older history is folded into a structured checkpoint (keeping recent messages verbatim and the user's original requirements) before a hard limit is ever hit; compaction runs off the paying loop on a cheaper helper.
- **Oversized tool output**: When a tool returns a very large result, it is spilled to durable storage with a preview returned in-context plus a "do not infer success from the preview" caveat.
- **Prompt injection via tool/retrieved content**: All tool output and retrieved content is treated as untrusted and is never fed straight into execution; the Rule of Two constrains what can happen unattended.
- **Waiting on humans or long jobs**: Waits (human approval, long-running job) suspend durably at zero ongoing cost and resume on an event, without polling turns.
- **Approval never answered**: A required human approval that is not granted within a configurable timeout expires as a denial (fail-closed). The gated high-impact action never proceeds; the denial returns to the loop as a typed synthetic result so the agent can replan or finish without it, and the run terminates with `approval_expired` (still returning its best partial artifact) only when it cannot proceed. While pending, the run is suspended durably at zero token cost.
- **Erasure request against an immutable log**: A right-to-erasure/DSAR request is satisfied by destroying the subject's content-encryption key (crypto-shredding), so the payload becomes unrecoverable while the event sequence, digests, and audit chain still verify — never by deleting or rewriting events, which would break the chain and the replay guarantee.
- **Audit record altered or dropped by a compromised writer**: Because receipts are hash-chained and the chain head is anchored outside the writing system, a rewritten or missing record breaks continuity and the scheduled verifier alerts; a component that writes receipts cannot forge the chain because the signing key is sign-only in a KMS/HSM.
- **Connection pooler reassigns a database connection mid-tenant**: Tenant scope is set transaction-locally, so a pooled connection handed to another tenant's transaction carries no residual scope; row-level security returns zero rows rather than another tenant's data, and the isolation test proves it through the same pooler production uses.
- **Concurrent burst against one tenant's ceiling**: Each turn reserves its worst-case cost against an atomic per-tenant counter before the model call, so many sessions starting at once cannot collectively overshoot the ceiling in the window before usage is reported; unused reservation is released on reconciliation.
- **Provider changes its prices**: Cost is computed from a versioned price book with effective dates and each cost record names the version used, so a price change is a reviewable configuration deploy and historical costs stay reproducible rather than every ceiling and chargeback silently drifting.
- **Forged or replayed webhook delivery**: An inbound consumer-surface webhook or OAuth callback is rejected before adapter translation unless it carries a valid provider signature/secret within the replay window and passes the per-identity flood limit — an attacker cannot inject instructions or burn tokens through the ingress path.
- **Long session accumulating taint**: Once a session has touched untrusted content and private data, the Rule of Two would gate every outward action; the platform offers an explicit, audited sanitization boundary (isolating untrusted content behind a summarizing sub-agent firewall or an operator-scoped re-baseline) so the control does not degrade into approval fatigue, and every taint transition is recorded.
- **Deploy while the schema is changing**: Migrations are additive-then-cleanup (expand/contract) and are verified in CI against the immediately preceding application version, so a rolling deploy running old and new code against one database cannot break either.
- **Region-pinned tenant routed elsewhere**: A run whose placement would fall outside its tenant's pinned region fails closed rather than executing; and in a customer-boundary deployment only enumerated structure-only fields may cross the boundary, with a fully in-boundary sink selectable so nothing leaves at all.
- **Restore after data loss**: Backups meet a ≤5-minute RPO and ≤4-hour RTO, and the drill is rehearsed quarterly — after restore the audit chain verifies and the event log replays, so the availability target is a commitment rather than a claim.
- **One tenant bursting**: Per-tenant budgets, rate limits, sandbox caps, and fair scheduling prevent one tenant from starving or bankrupting others.
- **Regulated payloads**: Requests carrying regulated/sensitive data are routed deterministically (by data label, not model discretion) to a self-hosted in-environment model so the payload never leaves the trust boundary.
- **Repeated identical failing call**: Broken by a circuit breaker after three identical failures rather than looping.
- **Runaway or malicious code execution**: When agent-written code loops infinitely, forks processes, exhausts memory, or attempts unapproved network egress (e.g., a prompt-injected "upload `.env` to my server"), the sandbox's hard CPU/memory/PID/wall-clock limits and network-default-deny terminate and reclaim it with a typed reason before any host, cross-tenant, or exfiltration impact — code never runs on the host and never sees files outside its session workspace.
- **Malformed or orphaned tool history**: If a turn leaves a `tool_use` without a paired `tool_result` (or vice versa), a hygiene pass repairs the transcript — backfilling synthetic results and dropping orphans — before the next model call, so a structurally invalid request is never sent to the provider.
- **Stalled or non-conforming model stream**: A streaming response that stops making progress is aborted by an idle watchdog and retried once non-streaming, so a hung or malformed upstream cannot stall a run indefinitely.
- **Direct prompt injection in the user message**: The user-input channel itself is screened by an input guard for instruction-override / role-reassignment / delimiter-escape patterns and fails closed on a high-severity match — the direct channel is not exempt from untrusted-content handling.
- **Leaked control markup or secrets in output**: All model/tool output is sanitized before delivery — leaked `<tool_call>`/`<think>` fragments and stutter are stripped and secret-shaped tokens are redacted — so raw control markup or credentials never reach a user or a log.
- **Retried or redelivered state-changing action**: A retry, an at-least-once queue redelivery, or a resume-from-checkpoint that re-issues a payment/send/write is deduplicated on a durable per-effect idempotency key, so the external effect happens exactly once even though the call was attempted more than once.
- **Runaway delegation**: A sub-agent is a read-only context firewall returning a bounded summary; it cannot spawn a decision-making swarm over one artifact, and its token spend is metered and attributed to the parent so a fan-out cannot silently multiply the bill.
- **Poisoned retrieval corpus**: Documents entering a tenant memory/retrieval corpus are access-controlled, provenance-tracked, and anomaly-scanned before indexing, and retrieved content stays tagged untrusted and out of the instruction channel — a planted instruction or backdoor trigger in a fetched document cannot become a trusted command.
- **Silent model/connector version drift**: A provider model-snapshot change or a connector/MCP-server version bump is a pinned-dependency deploy gated by the eval suite and revertible, so an upstream update cannot silently regress behavior in production.

## Requirements *(mandatory)*

### Functional Requirements — The Kernel (agent loop)

- **FR-001**: The platform MUST implement a single agent control loop that powers every surface; surfaces MUST NOT fork or re-implement agent control flow.
- **FR-002**: The loop MUST classify each model response into a typed set of outcomes (tool calls, content, empty) and dispatch on that classification rather than on text matching.
- **FR-003**: Every tool invocation MUST have a paired result recorded before the next model call; on any cancel or error path the platform MUST record a synthetic result.
- **FR-004**: The loop MUST end in an explicit, typed terminal reason (e.g., completed, max turns, cost exhausted, error, aborted, prompt too long, hook stopped, approval expired) that callers can handle exhaustively.
- **FR-005**: The platform MUST allow a human to steer or correct an in-flight run through a mid-run input mechanism, and MUST expose the full run lifecycle — steer, cancel, suspend, resume — as first-class operations on both the external surface contract and the control-plane ↔ data-plane contract. Steering input MUST be delivered to the running session (not enqueued as a new run), drained at a turn boundary under the session's serial lock, and appended to the event log as a typed event. Every terminal reason in FR-004 MUST be reachable through a defined operation — in particular `aborted` MUST have an explicit cancel operation; a terminal state no caller can produce is a contract defect.
- **FR-006**: Agent, tool, model, and configuration objects MUST be immutable; the only mutable runtime state MUST be the conversation state, changed only by appending typed events to an append-only log.
- **FR-060**: Before each model call the loop MUST run a hygiene pass over conversation state — dropping orphaned `tool_result`s (results with no matching `tool_use`), backfilling a synthetic result for any `tool_use` still missing one, and pruning or condensing stale tool observations — so every request sent to the provider is structurally valid and no malformed history reaches the model.
- **FR-085**: The event taxonomy MUST be complete enough to replay any run from the log alone. It MUST include, at minimum: model-produced events (thought / content / tool_use), tool results, condensation/checkpoint markers, **human-originated input** (the mid-run steering message of FR-005), **approval lifecycle** events (requested / granted / denied / expired), **error** events, and a **terminal** event carrying the typed terminal reason of FR-004. A run whose steering, approval, or termination cannot be reconstructed from the event log alone violates the single-source-of-truth requirement of FR-006, and the taxonomy MUST be identical across the internal log and every externally published event contract.
- **FR-086**: Every appended event MUST carry an explicit **schema version** in its envelope, and the platform MUST maintain a documented upcasting path so that events written under an older schema remain replayable after a schema change — the value of an append-only log is multi-year replay, which a silently-evolving payload shape destroys. Mutable columns that summarize run state (session status, terminal reason, approval status, sandbox state, connector-authorization status) MUST be documented and implemented as **projections derived from the event log**, rebuildable by replay, and MUST NEVER be treated as an independent source of truth.
- **FR-088**: Every run record MUST persist the inputs that determine its behavior and its scheduling, at minimum: the resolved **agent version** (pinned at run start and held for the life of the run so a concurrent deploy cannot change behavior or bust the prompt prefix mid-run), the **data label**, the **routing decision** that was taken (routed model plus the reason), and the **execution class** (interactive vs batch, with priority). Without these persisted, deterministic routing is not auditable or replayable (FR-027, FR-037) and the priority load-shedding of FR-049 has no field to read.
- **FR-079**: The platform MUST default to a single-threaded agent with continuous context and MUST use sub-agents only as isolated, read-only context firewalls: a sub-agent runs in its own clean context and returns only a bounded distilled summary (default cap ~1–2k tokens / ~8 KB) to the parent, which retains sole decision authority; parallel sub-agents that split one coherent artifact across contexts that cannot see each other's decisions are prohibited. Delegation MUST share the relevant full trace (not just messages), MUST be cost/token-metered per sub-agent and attributed to the parent task (FR-016) because delegation multiplies token consumption, and MUST honor the capability floor (FR-076/FR-077).

### Functional Requirements — Tools

- **FR-007**: Every tool MUST be self-describing (name, description, input schema) and route through one execution pipeline that performs validation, permission checks, execution, result budgeting, and telemetry.
- **FR-008**: Tools MUST default to fail-closed (serial unless proven concurrency-safe, assume writes, deny permission unless explicitly granted).
- **FR-009**: Safety MUST be evaluated per invocation on the parsed input (e.g., a benign shell command and a destructive one are judged differently), not per tool.
- **FR-010**: Tool outputs MUST be high-signal and capped/paginated by default, with oversized results offloaded to durable storage and referenced by a preview.
- **FR-011**: Tools MUST self-register and be governed by three gates: a global permission profile, per-tool capability metadata, and a per-invocation safety check on parsed input. Tool catalog entries that are tenant-specific (per-tenant connectors and their exposed tools) MUST be tenant-scoped in the data layer under the same row-level-security policy as every other tenant-owned row (FR-039) — a tenant MUST NOT be able to enumerate or invoke another tenant's catalog entries; only genuinely global built-in definitions may be untenanted.
- **FR-012**: External connectors MUST attach only through a vetted, per-tenant, permission-scoped connector catalog.
- **FR-071**: Every state-changing tool and connector invocation MUST be idempotent under re-execution. The platform MUST derive a stable idempotency key per intended effect and deduplicate on it so that a retry (FR-023), an at-least-once queue redelivery (FR-046), or a resume-from-checkpoint (FR-024) can NEVER double-charge, double-send, or double-write; the deduplication record MUST be tenant-scoped and durable.
- **FR-061**: Each tool MUST carry concurrency metadata (at least: read-only, concurrency-safe, exclusive). Within a single turn the platform MUST partition the requested tool calls into concurrency-safe batches that MAY execute in parallel and exclusive calls that execute serially, MUST NOT run an exclusive call concurrently with any other call, and MUST return every result in the model's original submission order regardless of completion order — failing closed to fully serial execution when the metadata is absent (per FR-008).
- **FR-062**: When the available tool/connector catalog is large, the platform MUST support deferred tool disclosure — advertising only a name and brief description for deferred tools and loading a full tool schema on demand through a tool-search capability — so the cache-stable prompt prefix (FR-013) stays small and the model is not flooded with unused tool definitions.

### Functional Requirements — Built-in Tool Suite

- **FR-056**: The platform MUST provide built-in, workspace-restricted filesystem tools (list, read, search, write, edit) that operate only inside the calling session's per-tenant sandbox/workspace, enforce poka-yoke absolute-path inputs, cap/paginate outputs, and treat file contents as untrusted input subject to the Rule of Two — a tool MUST NOT read or write outside its session workspace or reach another tenant's files.
- **FR-057**: The platform MUST provide a built-in shell / code-execution tool that runs only inside the per-tenant sandbox (with hard resource limits and network default-deny per FR-059), is judged by a per-invocation safety check on parsed input (e.g., `ls` is permitted where `rm -rf /` is refused), honors an allow/blocklist and a per-command timeout, and fails closed — never executing on the host or across tenants.
- **FR-058**: The platform MUST provide built-in web search and web fetch tools whose outbound egress is domain-allowlisted (FR-037), whose returned content is treated as untrusted (Rule of Two), and which return high-signal, capped/paginated results with oversized bodies offloaded to durable storage and referenced by a preview. Web fetch/crawl SHOULD default to an LLM-friendly extraction backend (crawl4ai) that returns clean, chunked markdown rather than raw HTML.
- **FR-059**: Every code/shell execution MUST run inside an isolated sandbox with hard resource limits (CPU, memory, PID/process count, and wall-clock timeout) enforced by the runtime, and MUST default to no outbound network access — egress is enabled only when the task explicitly requires it and then only through the domain allowlist (FR-037). A sandbox that exceeds any resource limit MUST be terminated and reclaimed with a typed reason (no host impact, no cross-tenant impact), and the code sandbox filesystem view MUST be scoped to the session workspace only (FR-056). The default sandbox backend SHOULD be E2B, with Docker/microVM (Firecracker/gVisor) and local OS isolation as swappable alternatives selected by deployment topology.

### Functional Requirements — Context & Cost

- **FR-013**: The prompt MUST be structured as a byte-stable prefix followed by a volatile tail rebuilt each turn; per-turn content MUST NOT enter the prefix and the stable prompt MUST NOT be mutated mid-session.
- **FR-014**: Context management MUST target a high cache-read rate on steady-state turns (goal: >90%).
- **FR-015**: When context nears its budget, the platform MUST compact older history into a structured checkpoint (preserving recent messages and the user's original requirements) run off the paying loop; it MUST NOT hit a hard context limit.
- **FR-016**: The platform MUST meter tokens per turn at a granularity that distinguishes **uncached input, cache-read input, cache-write input, and output** tokens, and MUST attribute cost to the requesting task chain and tenant. Undifferentiated input-token totals are insufficient: the cache-read rate required by FR-014 and SC-003 MUST be derivable from recorded per-turn measurements, not estimated.
- **FR-017**: The platform MUST enforce hard per-task and per-tenant cost ceilings that terminate with an explicit cost-exhausted reason; iteration count and wall-clock time are backstops only. Enforcement MUST be pre-spend, not post-hoc (FR-083).
- **FR-018**: Quality-per-dollar and completions-per-million-tokens MUST be reported alongside quality in every release gate.
- **FR-063**: The platform MUST reserve an explicit output-token budget per model call (a bounded default `max_tokens`) and, on a truncation / `max_output_tokens` signal, MUST escalate that reservation on a bounded retry rather than silently truncating — recovering usable context headroom without emitting partial, unterminated output.
- **FR-072**: In addition to the per-turn prefix cache (FR-013/FR-014), the platform SHOULD provide an optional response cache in front of the model — an exact-match and/or semantic (vector-similarity) `request → response` cache, gated by a similarity threshold and TTL, tenant-scoped — that skips the model call entirely on repeat or near-duplicate requests. It MUST be restricted to cacheable, non-state-dependent requests, MUST NOT serve a cross-tenant hit, and MUST be bypassable per request.
- **FR-083**: Cost ceilings MUST be enforced **before** the spend, not reconstructed after it. Before each model call the platform MUST reserve that call's worst-case cost (reserved output budget plus measured input) against an **atomic per-tenant and per-task counter**, MUST refuse the call when the reservation would breach a ceiling, and MUST reconcile actual usage against the reservation once the call completes (releasing the unused remainder). The worker MUST additionally enforce a local hard per-run budget synchronously so that enforcement never depends on a round trip to another plane. Aggregating usage after a turn completes and then signalling a stop is explicitly insufficient: it permits a single expensive turn, or a burst of concurrent sessions within one tenant, to exceed the ceiling before the signal is observed.
- **FR-084**: Token cost MUST be computed from a **versioned price book** — a per-model, per-token-class (uncached input / cache-read / cache-write / output) price table with effective dates, stored as configuration rather than embedded in code. Every cost record MUST reference the price-book version used to compute it, so historical costs remain reproducible and a provider price change is a reviewable configuration deploy rather than silent drift in every ceiling, quality-per-dollar figure, and chargeback report.
- **FR-093**: Metered cost MUST be exportable for **showback/chargeback**: aggregated per tenant, and within a tenant per user, agent, and surface, over a billable period, reconcilable to the sum of the underlying per-turn cost records. A budget without an owner who sees its consumption is not a control.
- **FR-073**: The platform MUST enforce output-side generation controls alongside the input-side budget (FR-063): a bounded `max_tokens` and stop sequences on every model call, schema-/grammar-constrained decoding for the model's own reply (not just tool-call arguments), and a terse-reasoning style — so conversational filler and unbounded reasoning traces do not inflate per-turn output tokens, latency, or the reparse-and-retry loop.

### Functional Requirements — Memory & Skills

- **FR-019**: Memory MUST be file-first, injected immutably at session start (updates take effect the next session), scoped per tenant, retention-bounded (default 90-day retention, overridable per tenant and per deployment tier), and screened for injection/exfiltration before injection.
- **FR-020**: Skills MUST load by progressive disclosure (a brief description always visible, full content on demand) and be reusable across runs.
- **FR-021**: Agent-proposed skills MUST follow propose → human/evaluation gate → version → promote and MUST NEVER be auto-promoted.
- **FR-022**: Richer retrieval tiers (embeddings/episodic memory, knowledge graph) MUST be introduced only when the data shape and scale justify it, and retrieved claims MUST be grounded/cited.
- **FR-074**: Any retrieval tier (FR-022) MUST rerank candidates and inject only the top-K highest-signal chunks (default K≈2–3) rather than every match, because retrieval precision — not recall dumped into context — sets the retrieval token term and avoids context rot; the K and the rerank strategy MUST be configurable.
- **FR-075**: Ingestion into any tenant memory or retrieval corpus (FR-019/FR-022) MUST be access-controlled and provenance-tracked — each document carrying a known source, a version/checksum, and an ingestion actor — MUST be anomaly-scanned for poisoning/backdoor patterns before indexing, and all retrieved content MUST remain tagged untrusted and kept out of the instruction channel (FR-033); an open, unauthenticated ingestion path is prohibited.

### Functional Requirements — Reliability

- **FR-023**: Every failure MUST be classified into a typed class before any retry; retries MUST be logged with a reason and backed off with jitter; identical failing calls MUST be circuit-broken after three repeats; silent retries are prohibited.
- **FR-024**: Run state MUST be checkpointed to durable storage so runs resume from the last checkpoint rather than restarting, capturing partial work on failure.
- **FR-025**: Stuck detection (repeated actions, oscillation, or zero net change over K steps) MUST break the loop and terminate with a clear reason.
- **FR-026**: Deploys MUST NOT cut a running agent over mid-task (rolling/rainbow deploy).
- **FR-027**: Provider access MUST go through one abstraction with a single normalized stream contract, with retry → cooldown → failover across multiple backends; native tool-calling only (no parsing tools from free-form text).
- **FR-064**: The normalized provider contract MUST preserve and round-trip provider reasoning/thinking segments opaquely — persisting any `reasoning_content` alongside the turn and replaying it on subsequent calls that reference prior tool calls — because some providers reject tool-call history whose reasoning segments are dropped; reasoning content MUST be treated as untrusted and excluded from user-visible output unless explicitly authorized.
- **FR-065**: The provider abstraction MUST normalize tool JSON schemas per backend (stripping or rewriting keywords a given provider rejects, e.g. unsupported `pattern`, `minLength`, or `$ref`) so one tool definition works across providers without a per-provider tool fork.
- **FR-066**: Streaming model calls MUST be guarded by an idle watchdog that aborts a stalled stream after a bounded no-progress interval and retries once in non-streaming mode (the fallback disabled while speculative/streaming tool execution is active), so a hung or non-conforming upstream response cannot stall a run indefinitely.
- **FR-067**: Every fatal or terminal error path MUST resolve to a degraded success that returns the best partial artifact captured from the last durable checkpoint (FR-024) rather than a bare crash, and MUST still terminate with the appropriate typed reason (FR-004).
- **FR-076**: Model routing MUST select a backend by the orchestration features a request will exercise, not by prompt difficulty alone: a request that will spawn sub-agents, drive multi-step playbooks, or use heavy MCP tooling MUST route to a model at or above the capability floor for those features, while feature-light requests (e.g., grounded Q&A) MAY route to a cheaper tier. This routing remains deterministic and auditable and composes with the data-label routing of FR-037.
- **FR-077**: Advanced harness features (sub-agent delegation, multi-step playbooks, large MCP tool catalogs) MUST degrade gracefully by model tier — scoping down the exposed tool catalog and disabling above-floor features on models below their reliability floor — rather than presenting one interface to every model; the per-tier feature profile MUST be configuration, not a kernel fork.
- **FR-078**: Model and connector/MCP-server versions MUST be pinned and treated as production dependencies: a model-snapshot change or a connector/MCP-server version bump is a deploy that MUST pass the evaluation gate (FR-042/FR-043) before adoption, MUST be supply-chain vetted (known provenance, pinned version, scoped permissions — an over-broad scope such as read-private-plus-post-public is a rejection signal), and MUST be revertible to the prior pinned version on regression.

### Functional Requirements — Surfaces & Control Plane

- **FR-028**: The platform MUST expose the agent through thin surface adapters (at minimum CLI, chat, web, REST/gRPC API, email, cron), each translating only input/output.
- **FR-029**: A control plane MUST enforce authentication (SSO/OIDC), role-based authorization, rate limits, budget checks, and model routing in front of the runtime, separate from agent logic. The platform MUST NOT issue credentials itself: each tenant configures its own OIDC issuer/client (per-tenant `identity_config`), the control plane validates presented tokens against that issuer's JWKS (`iss`/`aud`/`exp`), and a first valid sign-in just-in-time provisions the `User` (upsert by `(tenant_id, external_subject)`, roles resolved to permission scopes via the tenant `rbac_map`) — no separate in-platform registration step. To keep provider swaps (e.g., Auth0, Casdoor, Keycloak, Entra, Okta, Ory Hydra) config-only, `identity_config` MUST also carry a per-tenant claims mapping declaring which token claims hold the stable subject and the roles/groups (default `sub` and `roles`, overridable per provider); the platform reads only OIDC-standard discovery + JWKS and MUST NOT embed provider-specific SDKs. Non-interactive surfaces (CLI, cron) MUST authenticate via OIDC client-credentials service tokens carrying a delegated, least-privilege scope.
- **FR-030**: The control plane and data plane MUST be separately deployable behind a versioned contract so the data plane can move into a customer environment by configuration, not a rewrite.
- **FR-031**: Long-running surface interactions MUST stream or poll progress rather than hold a blocked connection.

### Functional Requirements — Consumer Surfaces & Personal Connectors

- **FR-051**: The platform MUST support consumer messaging surfaces (at minimum Telegram and Zalo) as thin webhook-ingress adapters that translate only input/output into the same run model, with identical control flow, safety/cost guarantees, and typed terminal reasons as every other surface — the kernel MUST NOT be forked per surface.
- **FR-052**: The platform MUST let an individual user authorize a personal connector to their own system of record via OAuth 2.0 authorization-code flow with PKCE; the resulting access and refresh tokens MUST be stored only in the per-tenant vault keyed by `(tenant, user, connector)`, auto-refreshed on expiry, and revocable by the user, and MUST NEVER appear in a prompt, transcript, or log.
- **FR-053**: The platform MUST ship reference personal connectors (at minimum Gmail, Google Drive, Google Calendar, and Notion) in the vetted per-tenant connector catalog, each self-describing with high-signal, consolidated operations (e.g., `gmail_search`/`gmail_send`, `drive_search`, `schedule_event`, `notion_search`/`notion_create`) rather than chatty low-level calls.
- **FR-054**: Every personal-connector invocation MUST run within the calling user's own delegated permission scope with the credential injected at execution time (model sees a handle), MUST treat all connector-returned content as untrusted, MUST be constrained by the Rule of Two, and MUST gate high-impact actions (external send, deletion, sharing) behind scoped human approval.
- **FR-055**: Each inbound consumer-surface identity (e.g., Telegram/Zalo user id) MUST be bound to a platform `User` within a tenant through a verified linking step before any action runs; an unverified or unlinked external identity MUST be denied (fail-closed).

### Functional Requirements — Security & Trust

- **FR-032**: Defense MUST be layered (channel allowlist, autonomy mode, workspace restriction, shell allow/blocklist, per-tenant sandbox isolation, tamper-evident audit receipts) and fail closed.
- **FR-033**: Within a session, at most two of {process untrusted input, access private data, change state or communicate externally} MUST proceed without human approval (Rule of Two); all tool output and retrieved content MUST be treated as untrusted.
- **FR-034**: Secrets MUST NEVER be placed in the prompt; they MUST be injected at tool-execution time from a vault (model sees a handle) and isolated per tenant.
- **FR-035**: The agent MUST act with the calling user's permission scope (delegated identity), never a superuser service account, enforced at the tool boundary.
- **FR-036**: High-impact actions (payments, deletions, external sends, production changes) MUST be gated by scoped human approval. While an approval is pending, the run MUST suspend durably at zero ongoing token cost and resume on the approval event (never poll). An approval that is not granted within a configurable timeout MUST expire as a denial (fail-closed) and the high-impact action MUST NOT proceed. The expiry denies **the action**: the denial MUST be returned to the loop as a typed synthetic `tool_result` so the agent may replan or complete without it, preserving partial work; the run terminates with a typed `approval_expired` reason only when it cannot proceed without that action, and in that case MUST still return the best partial artifact per FR-067. Every outcome (granted / denied / expired) MUST be recorded as a typed event and in the audit log. Approval scopes MUST be bounded in time and blast radius — a scope MUST name the tool and the effect class it covers and MUST carry an expiry; no scope may permanently ungate a class of high-impact action.
- **FR-037**: Outbound domains MUST be allowlisted and sensitive data (PII/secrets/PHI/card data) MUST be masked by class before leaving the trust boundary; regulated payloads MUST be routable to a self-hosted in-environment model.
- **FR-068**: All model output and tool output MUST pass an egress sanitizer before delivery to a user or persistence — stripping leaked control markup (e.g., `<tool_call>` / `<think>` fragments, echoed system framing, duplicated stutter) — and a credential scrubber MUST redact secret-shaped tokens (keys, bearer tokens, connector credentials) from any output, as defense-in-depth complementing vault-only secret handling (FR-034).
- **FR-069**: Inbound user messages MUST pass a configurable input guard that screens for prompt-injection / jailbreak patterns (e.g., instruction-override, role-reassignment, delimiter/system-tag escapes) with selectable enforcement modes (off / log / warn / block); this complements the untrusted-content handling for tool and retrieved content (FR-033) by covering the direct user-input channel, and MUST fail closed in block mode on a matched high-severity pattern.
- **FR-070**: External MCP (Model Context Protocol) servers MUST run as isolated, untrusted processes reached only through the vetted per-tenant connector catalog (FR-012); MCP-provided content MUST be treated as untrusted data under the Rule of Two (FR-033) and MUST NEVER be executed as inline shell or trusted instructions, and an MCP server MUST NOT gain host, cross-tenant, or non-allowlisted network access (FR-037, FR-059).
- **FR-081**: The audit log MUST be tamper-**evident**, not merely append-intended. A per-record MAC is insufficient because the component that writes receipts holds the key and can rewrite or drop records undetectably. Receipts MUST be **hash-chained** per session (each record binding its predecessor's digest and a monotonic sequence), the chain head MUST be periodically anchored outside the writing system (an append-only external store or transparency log), and the signing key MUST be held **sign-only** in a KMS/HSM so no data-plane component can recompute a forged chain. A scheduled verifier MUST prove chain continuity and sequence completeness and MUST alert on any break, gap, or failed verification. Chain verification MUST remain valid after a lawful redaction (FR-080) — the chain binds digests, never plaintext.
- **FR-082**: Every surface that accepts unsolicited inbound traffic (consumer-messaging webhooks, OAuth redirect callbacks, inbound email, third-party event hooks) MUST authenticate the caller **before** the payload reaches any adapter translation or the kernel: verifying the provider's signature or shared secret token, rejecting replays outside a bounded timestamp/nonce window, enforcing a per-external-identity flood limit, and failing closed on any of these. Identity binding (FR-055) authorizes *who* the message is from once it is trusted and is not a substitute for proving the request originated with the provider — an unauthenticated webhook endpoint is a direct untrusted-instruction channel into the kernel and an unmetered cost-burn vector. The OAuth redirect callback MUST additionally bind single-use `state` to the initiating user session and MUST accept only pre-registered redirect URIs.
- **FR-087**: The Rule of Two (FR-033) MUST be evaluated from **declared taint metadata**, not inferred at runtime. Every tool, connector, and retrieval source MUST declare, as first-class capability metadata: whether it *returns untrusted content*, whether it *reads private/tenant data*, and whether it *changes external state or communicates outward*. The platform MUST track accumulated taint as typed session state, MUST derive the approval decision from that state plus the pending invocation's declared legs, and MUST fail closed when a tool's metadata is absent or unclassifiable. Because a long session otherwise becomes permanently tainted and triggers approval on everything (approval fatigue defeating the control), the platform MUST support explicit **sanitization boundaries** — a bounded, audited operation that reduces taint by isolating untrusted content behind a summarizing/sub-agent firewall (FR-079) or by an operator-scoped re-baseline — and MUST record every taint transition as an event.

### Functional Requirements — Multi-Tenancy, Audit & Observability

- **FR-038**: Tenant identity MUST be the first dimension of every session key, data row, workspace path, cost record, and secret.
- **FR-039**: Data isolation MUST be enforced at the data layer (row-level security), never by application access controls alone. The tenant scope that row-level security reads MUST be established **transaction-locally** (`SET LOCAL` / `SET ROLE LOCAL` within the transaction); session-level scoping is prohibited because a transaction-pooling tier reassigns a physical connection between tenants between statements. The cross-tenant isolation test MUST execute through the same connection-pooling tier used in production; a result obtained against a direct database connection does not satisfy this requirement.
- **FR-040**: Every action MUST be attributable to a user and tenant in an immutable audit log; observability MUST capture decision structure and per-turn cost/latency/token spans without requiring reads of conversation content, while keeping prompts/responses inspectable for authorized debugging. Attribution MUST be tamper-evident under the integrity requirements of FR-081, not merely append-intended.
- **FR-041**: Sessions MUST be per-session serial and cross-session concurrent, routed by session key, with per-tenant budgets, rate limits, and sandbox caps.

### Functional Requirements — Data Lifecycle, Residency & Resilience

- **FR-080**: The platform MUST reconcile the append-only event log (FR-006) with retention limits and a right to erasure. Because events are never deleted or updated, erasure MUST be implemented as **crypto-shredding**: event payloads and other customer content MUST be envelope-encrypted under a per-tenant data key (and a per-erasure-subject key at deployment tiers that require subject-level DSAR erasure), and an erasure request MUST destroy the corresponding key so the content becomes unrecoverable while the event sequence, its digests, the audit chain (FR-081), and all structure-only telemetry remain intact and verifiable. The platform MUST record erasure as a typed, audited event; MUST enforce retention expiry (FR-019) by the same mechanism; and MUST NOT satisfy a DSAR by any path that breaks chain verification or silently rewrites history. A DSAR MUST also be answerable in the *access* direction — enumerating the data held for a subject across events, memory, cost records, and connector authorizations.
- **FR-089**: Conversation content — prompts, tool arguments, tool results, memory, and offloaded artifacts — is customer data and MUST be encrypted at rest under a **per-tenant key**, with customer-managed keys (BYOK/CMK, revocable by the customer) supported at the deployment tiers that require it. Key custody MUST be separate from the data store, and key revocation MUST render the tenant's content unreadable without deleting the log (composing with FR-080).
- **FR-090**: The event log, audit chain, configuration, and vault MUST have a defined and rehearsed recovery posture: documented backup coverage, a **recovery point objective of ≤5 minutes** and a **recovery time objective of ≤4 hours** for the control plane and event log, point-in-time restore, and a restore drill rehearsed at least quarterly whose measured result is recorded. Post-restore, the audit chain MUST verify and the log MUST replay. An availability target (SC-011) without a rehearsed restore is not a commitment.
- **FR-091**: Data residency MUST be enforced by placement, not by policy text: a tenant's region pin MUST bind its event log, memory, artifacts, sandboxes, queue, and model routing to that region, and a run MUST fail closed rather than execute outside its tenant's pinned region. Everything that crosses a deployment boundary MUST be enumerated in the control-plane ↔ data-plane contract and **bounded to structure** — identifiers, counts, digests, and typed reasons, never conversation content, tool arguments, or tool results. A fully in-boundary audit and telemetry sink MUST be selectable by configuration for self-hosted/BYOC and hybrid topologies, so a customer may choose that no metadata leaves at all.
- **FR-092**: The platform's own build MUST meet the supply-chain bar it imposes on third parties (FR-078): reproducible, signed release artifacts with published provenance, a generated SBOM per release, automated dependency and container vulnerability scanning in CI with a defined severity threshold that fails the build, and pinned build-time dependencies.

### Functional Requirements — Governance & Evals

- **FR-042**: Prompts, tools, and skills MUST be treated as production config — versioned, code-reviewed, and evaluation-gated; a prompt or model change is a deploy.
- **FR-043**: An evaluation set (starting ~20 real cases) with a rubric-based judge and end-state checks MUST run in CI and gate any prompt/tool/model/skill change, with held-out grader tests the agent cannot edit to prevent spec-gaming. A change MUST clear the gate only when it achieves at least a 90% pass rate AND causes zero regressions versus the current baseline (no previously-passing case may regress). The eval set and its CI gate MUST be delivered as foundational infrastructure — in place before the first behavior-bearing slice ships — so that no phase of kernel, tool, or prompt development proceeds unmeasured.
- **FR-044**: Agents MUST NOT self-declare success; completion MUST be verified against explicit acceptance criteria.
- **FR-095**: Every stated availability and latency target (SC-008, SC-011) MUST be expressed as a measured SLO with a defined **error budget**, a documented policy for what happens when the budget is exhausted (change freeze / escalation), and **burn-rate alerting** that pages to a named runbook section. Alerting MUST cover the agent-specific golden signals in addition to infrastructure ones: queue wait and oldest-message age, run-completion rate by terminal reason, cost-ceiling breach rate, approval-expiry rate, stuck-detection rate, cache-read rate, sandbox reclamation rate, and provider throttle/failover rate.
- **FR-096**: Ownership of every operational control MUST be named and recorded: a **platform team** owns the shared harness (kernel, tools, guardrails, observability, connector and skill catalogs); an **AgentOps** function owns SLOs, on-call, evals-in-CI, cost dashboards, and behavioral incident response (replay the event log → diagnose the trajectory → patch prompt/tool → redeploy via rainbow); and a **governance/risk** function signs off new tools, connectors, and autonomy-level changes and maintains the AI risk register. A new tool, connector, or autonomy increase MUST carry a recorded governance sign-off before it is enabled for a tenant.
- **FR-097**: Because the model is a non-deterministic and metered dependency, the automated test suite MUST run against a **deterministic provider harness** — a fake/recorded-transcript provider implementing the same normalized stream contract (FR-027), including its failure, truncation, stall, and malformed-stream paths — so correctness tests are reproducible and do not bill a live provider. Recorded fixtures MUST be versioned with the contract. The transcript-hygiene invariants (FR-003, FR-060) MUST additionally be covered by **property-based tests** over generated event sequences, not by examples alone, because they are total invariants over all histories.
- **FR-094**: Database schema change MUST follow an **expand/contract** discipline compatible with the rolling deploys of FR-026: every migration is additive and backward-compatible first, with destructive cleanup deferred to a later release, so that old and new application versions run correctly against one schema simultaneously. No release may require old and new schema at the same time, and every migration MUST be verified against the immediately preceding application version in CI.
- **FR-045**: No production launch MUST occur without the go-live checklist green (attributable audit, vaulted per-tenant secrets, sandboxing with hard resource limits + network default-deny + human approval for high-impact actions, one leg of the lethal trifecta broken per risky flow, per-task/per-tenant cost ceilings, failure classification + resume + stuck detection, evals green in CI, high steady-state cache-read, documented residency/retention/no-train, rehearsed incident runbook).

### Functional Requirements — Scale & Deployment

- **FR-046**: Agent runs MUST execute as asynchronous jobs on a durable queue processed by stateless, disposable workers with externalized state, autoscaled on queue depth/age.
- **FR-047**: Sandboxes MUST be served from a warm pool with hard TTLs, reclamation on terminal/stuck state, per-tenant caps, and hard per-sandbox resource limits (CPU, memory, PID/process count, wall-clock) whose breach terminates and reclaims the sandbox with a typed reason (FR-059).
- **FR-048**: Provider rate limits MUST be handled via per-tenant rate limiting, connection pooling, failover-as-capacity, and cached prefixes.
- **FR-049**: Under overload the platform MUST apply admission control, fair scheduling across tenants, priority load-shedding, and graceful degradation (e.g., route to a smaller tier) rather than collapsing.
- **FR-050**: The same build MUST serve multi-tenant SaaS, single-tenant, self-hosted/BYOC, and hybrid topologies via configuration, with per-organization behavior expressed as data/config read at runtime and the kernel never forked per customer.

### Key Entities

- **Agent**: An immutable configuration (persona/bootstrap definition, toolset profile, autonomy level) that produces the next action from history; not code, not forked per customer.
- **Conversation / Session**: The only mutable runtime state; an append-only, event-sourced log of typed events (thoughts, actions, observations) keyed first by tenant, replayable and auditable.
- **Event**: A typed, timestamped, attributable, **schema-versioned** record appended to the log — the single source of truth. The taxonomy spans model output, tool use and results, condensation/checkpoints, human steering input, the approval lifecycle, errors, taint transitions, and the terminal event, so any run replays from the log alone. Payloads are envelope-encrypted per tenant and carry a digest that the audit chain binds.
- **Price Book**: A versioned, effective-dated per-model, per-token-class price table held as configuration; every cost record names the version used to compute it.
- **Taint State**: The typed per-session record of which Rule-of-Two legs have been engaged (untrusted input processed, private data accessed, external state changed), derived from declared per-tool metadata, reduced only through an audited sanitization boundary, and recorded as events.
- **Tool**: A self-describing capability with input schema, per-invocation safety/permission checks, and capability metadata; may be a built-in or a per-tenant permission-scoped connector.
- **Connector Authorization**: A per-user OAuth grant (access + refresh tokens, scopes, expiry) binding a `User` to an external connector, stored only in the per-tenant vault keyed by `(tenant, user, connector)`, auto-refreshed and user-revocable, never exposed to the model.
- **Surface Identity**: A verified binding from an external consumer-surface identity (e.g., Telegram/Zalo user id) to a platform `User` within a tenant; required before any action runs from that surface.
- **Model/Provider**: A pluggable backend accessed only through one abstraction with a normalized stream contract and deterministic, auditable routing.
- **Tenant**: The first-class isolation boundary for data, secrets, budgets, rate limits, workspaces, and audit.
- **User**: The delegated identity whose permission scope the agent acts within; provisioned just-in-time on first valid sign-in against the tenant's configured OIDC issuer (identified by `(tenant_id, external_subject)`), never registered separately in-platform.
- **Skill**: A versioned, progressively disclosed procedure; growable by the agent only through a human/eval promotion gate.
- **Memory**: Per-tenant, retention-bounded durable knowledge injected immutably at session start after injection screening.
- **Budget / Cost Record**: Per-task and per-tenant token/cost accounting with hard ceilings and an explicit exhaustion reason.
- **Audit Receipt**: A tamper-evident record binding a mutating action to session, tool, args, result, and timestamp, **hash-chained** to its predecessor with a monotonic sequence, signed by a sign-only KMS/HSM key, and periodically anchored outside the writing system so continuity is externally provable.
- **Encryption Key / Erasure Subject**: The per-tenant (and, at regulated tiers, per-subject) content-encryption key whose destruction constitutes erasure; key custody is separate from the data store and supports customer-managed keys.
- **Sandbox / Workspace**: Per-tenant isolated execution environment from a warm pool with TTLs, per-tenant caps, and hard per-sandbox resource limits (CPU/memory/PID/wall-clock) with network default-deny; the trust boundary for all code/shell execution (default backend E2B; Docker/microVM/local-OS isolation as swappable alternatives).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can complete a representative multi-turn, tool-using task end-to-end through the agent, with completion verified against explicit acceptance criteria on at least 90% of a 20-case evaluation set.
- **SC-002**: 100% of runs terminate with an explicit typed reason; no run exceeds its per-task or per-tenant cost ceiling, and ceiling breaches always stop with a cost-exhausted reason plus an alert (zero surprise overruns).
- **SC-003**: Steady-state turns achieve greater than 90% cache-read (measurable gate: ≥90% in CI observability). Orchestration cost and latency per completed task must be measurably lower than an unoptimized baseline; the directional target is −40% cost and −40% latency at equal quality (aspirational benchmark, not a binary pass/fail gate — tracked as η$ and CPM metrics per FR-018 and SC-009).
- **SC-004**: 100% of actions are attributable to a specific user and tenant in an immutable audit log, and cross-tenant data/secret/budget access is impossible in isolation tests (zero leakage).
- **SC-005**: 100% of high-impact actions (payments, deletions, external sends, production changes) are blocked pending scoped human approval, and no session performs all three legs of the lethal trifecta unattended.
- **SC-006**: A run interrupted mid-task resumes from its last checkpoint and completes without restarting from scratch; a deploy during active runs cuts over zero in-flight tasks mid-task.
- **SC-007**: The same build is deployed in at least two topologies (e.g., multi-tenant SaaS and self-hosted) purely by configuration, and a new organization is onboarded with zero kernel code changes.
- **SC-008**: The platform sustains ≥5,000 concurrent long-running sessions with a met queue-wait SLA — p95 queue-wait under 5s for interactive runs and under 60s for batch/async runs, with first-token latency under 2s for interactive runs — and under overload it degrades gracefully (admission control / fair scheduling / load-shedding) with zero cascading collapse.
- **SC-009**: 100% of prompt/tool/model/skill changes pass the evaluation gate in CI before release — defined as ≥90% pass rate on the eval set and zero regressions versus the current baseline — and the agent cannot edit held-out grader tests. The gate is operational before the first behavior-bearing slice ships, and the correctness suite runs against a deterministic provider harness (zero live-provider calls in CI for correctness tests).
- **SC-010**: Every failure is classified before retry with zero silent retries, and identical failing calls are circuit-broken within three attempts.
- **SC-011**: The platform meets a monthly availability SLA of ≥99.9% for the control plane / API and ≥99.5% for agent-run completion; SLA attainment is measured and reported against a defined error budget, and burn-rate alerting pages to a named runbook section on breach.
- **SC-012**: A user completes representative common tasks (e.g., summarize unread email, find a Drive document, schedule a calendar event) from both a Telegram and a Zalo chat with identical control flow and terminal reasons to the API surface; every personal connector is authorized by per-user OAuth with tokens vaulted per `(tenant, user, connector)` and never present in any prompt/transcript/log; 100% of high-impact connector actions block pending scoped approval; and an unverified external chat identity performs zero actions.
- **SC-013**: Cross-tenant isolation tests execute through the production connection-pooling tier and return zero rows in 100% of attempts, including under concurrent multi-tenant load; no test asserts isolation against a direct database connection only.
- **SC-014**: An erasure request renders the subject's content unrecoverable within the contractual window while 100% of audit-chain verifications still pass and the event log still replays; a DSAR access request enumerates all data held for the subject across events, memory, cost records, and connector authorizations.
- **SC-015**: Audit-chain verification detects 100% of injected tampering (record modification, deletion, or reordering) in a red-team test, and the chain head is externally anchored at the configured interval with zero verification gaps.
- **SC-016**: No run or tenant exceeds its cost ceiling under a concurrent-burst test (many sessions started simultaneously against a near-exhausted budget), proving pre-spend reservation rather than post-hoc detection; reserved-but-unused budget is released with zero leakage over a sustained run.
- **SC-017**: Steady-state cache-read rate is computed from recorded per-turn cache-read/cache-write/uncached token measurements (not estimated), and every cost record resolves to a price-book version so historical costs recompute identically.
- **SC-018**: A rehearsed restore drill meets RPO ≤5 minutes and RTO ≤4 hours, with the audit chain verifying and the event log replaying post-restore; the drill result is recorded at least quarterly.
- **SC-019**: 100% of inbound webhook and OAuth-callback deliveries are authenticated (signature/secret + replay window) before reaching an adapter; forged, replayed, and flooded deliveries are rejected with zero kernel invocations and zero token spend.
- **SC-020**: Every terminal reason in FR-004 is reachable through a documented operation — in particular a cancel operation produces `aborted` — and every run's steering, approval, and termination is reconstructable from the event log alone.

## Assumptions

- The feature request "build agent in draft-plan.md" refers to the complete Enterprise Agent Master Plan in [draft-plan.md](../../draft-plan.md); this specification captures that plan's WHAT/WHY. It is intentionally large and is expected to be delivered in phases (kernel → harness → reliability/context → surfaces/skills → trust surface → scale/compliance), with each phase a shippable, testable increment.
- User stories are prioritized so P1 (the reliable kernel) is a standalone MVP; each subsequent story adds an independently testable slice.
- The platform is model- and provider-agnostic; the specific models/providers are configuration and may change over the platform's life without redesign.
- "Cost" is the primary stop signal; concrete default ceilings (e.g., per-task and per-tenant limits) are configurable per tenant and per deployment tier, using industry-standard defaults where unspecified.
- Evaluation, retention, region-pinning, and compliance obligations (e.g., SOC 2, GDPR/CCPA, HIPAA, PCI-DSS) apply only at the deployment tiers that require them and are satisfied by configuration/artifacts, not kernel forks.
- The platform aligns with and is governed by the project constitution ([.specify/memory/constitution.md](../../.specify/memory/constitution.md), v1.1.0); where any detail here conflicts with the constitution, the constitution wins.
- Standard secure-by-default practices apply to unspecified details (user-friendly error handling, session-based/OAuth2 auth for web surfaces, allowlisted egress, least-privilege identity).
- **Deliberate divergence on delegation**: FR-079 is intentionally stricter than the source material it draws on (which permits nesting up to ~3 levels and ~5 concurrent children, and reports orchestrator-worker configurations outperforming a single agent on breadth-first research). This platform restricts sub-agents to read-only context firewalls returning bounded summaries, accepting the loss of the parallel-research win in exchange for a single locus of decision authority, predictable cost attribution, and a tractable Rule-of-Two taint model. Revisiting this is a governed change (FR-096), not a default.
- **Erasure applies to content, not to the fact of an action**: crypto-shredding removes conversation content and tool payloads; the structural record that an action occurred — actor, tenant, tool, timestamp, digest — is retained for audit and regulatory record-keeping obligations, which is the standard reconciliation between erasure rights and audit duties.
```
