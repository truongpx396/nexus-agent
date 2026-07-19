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

## User Scenarios & Testing *(mandatory)*

### User Story 1 - Complete a real task through a reliable agent (Priority: P1)

An end user submits a request (e.g., "triage this bug and propose a fix") to the agent. The agent works through an observe → think → act loop, calling tools as needed, and returns a result whose completion is verified against explicit acceptance criteria rather than self-declared. The run stops when it reaches a cost ceiling, completes, or hits a bounded backstop — never runs away.

**Why this priority**: This is the irreducible core — a reliable single-agent loop that completes tasks under a cost bound. Without it, nothing else has value. It is a viable MVP on its own: a user can get real work done safely.

**Independent Test**: Give the agent a multi-turn task requiring at least one tool call; confirm it holds the conversation, pairs every tool invocation with a result, stops on the configured cost cap, and reports a typed terminal reason (completed / cost exhausted / max turns / error / aborted).

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
5. **Given** a flow that would combine processing untrusted input, accessing private data, and changing state/communicating externally, **When** all three would occur in one session, **Then** the platform requires human approval (no more than two of the three proceed unattended).

---

### User Story 4 - Govern cost and observe behavior (Priority: P2)

A platform/ops owner needs every run's token usage and cost metered per turn and attributed to the requesting task and tenant, with hard per-task and per-tenant ceilings. They can inspect decision structure, latency, and cost without reading private conversation content, and any change to prompts, tools, or models must pass an evaluation gate before release.

**Why this priority**: "Stop on cost, not vibes" and "you can't operate what you can't see" are core operating requirements. This makes the platform affordable and safe to change, but depends on the loop and trust surface.

**Independent Test**: Run a workload and confirm per-turn token/cost metering attributed to task and tenant, enforcement of a per-tenant ceiling, a structure-only trace view, and a CI gate that blocks a prompt/model change failing the eval set.

**Acceptance Scenarios**:

1. **Given** a running task chain, **When** each turn completes, **Then** input and output tokens, latency, and cost are recorded and attributed to that task and tenant.
2. **Given** a tenant that reaches its cost ceiling, **When** the ceiling is crossed, **Then** further runs stop with an explicit cost-exhausted reason and an alert, not a surprise bill.
3. **Given** an operator investigating a run, **When** they open its trace, **Then** they can see decision patterns and per-turn cost/latency/token spans without reading conversation content, and can still inspect the actual prompt/response when debugging is authorized.
4. **Given** a proposed change to a prompt, tool, model, or skill, **When** it is submitted, **Then** it must pass a versioned eval set in CI before it can ship.

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
- **Approval never answered**: A required human approval that is not granted within a configurable timeout expires as a denial (fail-closed); the run terminates with a typed `approval_expired` reason and an audit record, and the gated high-impact action never proceeds.
- **One tenant bursting**: Per-tenant budgets, rate limits, sandbox caps, and fair scheduling prevent one tenant from starving or bankrupting others.
- **Regulated payloads**: Requests carrying regulated/sensitive data are routed deterministically (by data label, not model discretion) to a self-hosted in-environment model so the payload never leaves the trust boundary.
- **Repeated identical failing call**: Broken by a circuit breaker after three identical failures rather than looping.
- **Runaway or malicious code execution**: When agent-written code loops infinitely, forks processes, exhausts memory, or attempts unapproved network egress (e.g., a prompt-injected "upload `.env` to my server"), the sandbox's hard CPU/memory/PID/wall-clock limits and network-default-deny terminate and reclaim it with a typed reason before any host, cross-tenant, or exfiltration impact — code never runs on the host and never sees files outside its session workspace.
- **Malformed or orphaned tool history**: If a turn leaves a `tool_use` without a paired `tool_result` (or vice versa), a hygiene pass repairs the transcript — backfilling synthetic results and dropping orphans — before the next model call, so a structurally invalid request is never sent to the provider.
- **Stalled or non-conforming model stream**: A streaming response that stops making progress is aborted by an idle watchdog and retried once non-streaming, so a hung or malformed upstream cannot stall a run indefinitely.
- **Direct prompt injection in the user message**: The user-input channel itself is screened by an input guard for instruction-override / role-reassignment / delimiter-escape patterns and fails closed on a high-severity match — the direct channel is not exempt from untrusted-content handling.
- **Leaked control markup or secrets in output**: All model/tool output is sanitized before delivery — leaked `<tool_call>`/`<think>` fragments and stutter are stripped and secret-shaped tokens are redacted — so raw control markup or credentials never reach a user or a log.

## Requirements *(mandatory)*

### Functional Requirements — The Kernel (agent loop)

- **FR-001**: The platform MUST implement a single agent control loop that powers every surface; surfaces MUST NOT fork or re-implement agent control flow.
- **FR-002**: The loop MUST classify each model response into a typed set of outcomes (tool calls, content, empty) and dispatch on that classification rather than on text matching.
- **FR-003**: Every tool invocation MUST have a paired result recorded before the next model call; on any cancel or error path the platform MUST record a synthetic result.
- **FR-004**: The loop MUST end in an explicit, typed terminal reason (e.g., completed, max turns, cost exhausted, error, aborted, prompt too long, hook stopped, approval expired) that callers can handle exhaustively.
- **FR-005**: The platform MUST allow a human to steer or correct an in-flight run through a mid-run input mechanism.
- **FR-006**: Agent, tool, model, and configuration objects MUST be immutable; the only mutable runtime state MUST be the conversation state, changed only by appending typed events to an append-only log.
- **FR-060**: Before each model call the loop MUST run a hygiene pass over conversation state — dropping orphaned `tool_result`s (results with no matching `tool_use`), backfilling a synthetic result for any `tool_use` still missing one, and pruning or condensing stale tool observations — so every request sent to the provider is structurally valid and no malformed history reaches the model.

### Functional Requirements — Tools

- **FR-007**: Every tool MUST be self-describing (name, description, input schema) and route through one execution pipeline that performs validation, permission checks, execution, result budgeting, and telemetry.
- **FR-008**: Tools MUST default to fail-closed (serial unless proven concurrency-safe, assume writes, deny permission unless explicitly granted).
- **FR-009**: Safety MUST be evaluated per invocation on the parsed input (e.g., a benign shell command and a destructive one are judged differently), not per tool.
- **FR-010**: Tool outputs MUST be high-signal and capped/paginated by default, with oversized results offloaded to durable storage and referenced by a preview.
- **FR-011**: Tools MUST self-register and be governed by three gates: a global permission profile, per-tool capability metadata, and a per-invocation safety check on parsed input.
- **FR-012**: External connectors MUST attach only through a vetted, per-tenant, permission-scoped connector catalog.
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
- **FR-016**: The platform MUST meter input and output tokens per turn and attribute cost to the requesting task chain and tenant.
- **FR-017**: The platform MUST enforce hard per-task and per-tenant cost ceilings that terminate with an explicit cost-exhausted reason; iteration count and wall-clock time are backstops only.
- **FR-018**: Quality-per-dollar and completions-per-million-tokens MUST be reported alongside quality in every release gate.
- **FR-063**: The platform MUST reserve an explicit output-token budget per model call (a bounded default `max_tokens`) and, on a truncation / `max_output_tokens` signal, MUST escalate that reservation on a bounded retry rather than silently truncating — recovering usable context headroom without emitting partial, unterminated output.

### Functional Requirements — Memory & Skills

- **FR-019**: Memory MUST be file-first, injected immutably at session start (updates take effect the next session), scoped per tenant, retention-bounded (default 90-day retention, overridable per tenant and per deployment tier), and screened for injection/exfiltration before injection.
- **FR-020**: Skills MUST load by progressive disclosure (a brief description always visible, full content on demand) and be reusable across runs.
- **FR-021**: Agent-proposed skills MUST follow propose → human/evaluation gate → version → promote and MUST NEVER be auto-promoted.
- **FR-022**: Richer retrieval tiers (embeddings/episodic memory, knowledge graph) MUST be introduced only when the data shape and scale justify it, and retrieved claims MUST be grounded/cited.

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
- **FR-036**: High-impact actions (payments, deletions, external sends, production changes) MUST be gated by scoped human approval. An approval that is not granted within a configurable timeout MUST expire as a denial (fail-closed), terminating the run with a typed `approval_expired` reason recorded in the audit log; the high-impact action MUST NOT proceed on timeout.
- **FR-037**: Outbound domains MUST be allowlisted and sensitive data (PII/secrets/PHI/card data) MUST be masked by class before leaving the trust boundary; regulated payloads MUST be routable to a self-hosted in-environment model.
- **FR-068**: All model output and tool output MUST pass an egress sanitizer before delivery to a user or persistence — stripping leaked control markup (e.g., `<tool_call>` / `<think>` fragments, echoed system framing, duplicated stutter) — and a credential scrubber MUST redact secret-shaped tokens (keys, bearer tokens, connector credentials) from any output, as defense-in-depth complementing vault-only secret handling (FR-034).
- **FR-069**: Inbound user messages MUST pass a configurable input guard that screens for prompt-injection / jailbreak patterns (e.g., instruction-override, role-reassignment, delimiter/system-tag escapes) with selectable enforcement modes (off / log / warn / block); this complements the untrusted-content handling for tool and retrieved content (FR-033) by covering the direct user-input channel, and MUST fail closed in block mode on a matched high-severity pattern.
- **FR-070**: External MCP (Model Context Protocol) servers MUST run as isolated, untrusted processes reached only through the vetted per-tenant connector catalog (FR-012); MCP-provided content MUST be treated as untrusted data under the Rule of Two (FR-033) and MUST NEVER be executed as inline shell or trusted instructions, and an MCP server MUST NOT gain host, cross-tenant, or non-allowlisted network access (FR-037, FR-059).

### Functional Requirements — Multi-Tenancy, Audit & Observability

- **FR-038**: Tenant identity MUST be the first dimension of every session key, data row, workspace path, cost record, and secret.
- **FR-039**: Data isolation MUST be enforced at the data layer (row-level security), never by application access controls alone.
- **FR-040**: Every action MUST be attributable to a user and tenant in an immutable audit log; observability MUST capture decision structure and per-turn cost/latency/token spans without requiring reads of conversation content, while keeping prompts/responses inspectable for authorized debugging.
- **FR-041**: Sessions MUST be per-session serial and cross-session concurrent, routed by session key, with per-tenant budgets, rate limits, and sandbox caps.

### Functional Requirements — Governance & Evals

- **FR-042**: Prompts, tools, and skills MUST be treated as production config — versioned, code-reviewed, and evaluation-gated; a prompt or model change is a deploy.
- **FR-043**: An evaluation set (starting ~20 real cases) with a rubric-based judge and end-state checks MUST run in CI and gate any prompt/tool/model/skill change, with held-out grader tests the agent cannot edit to prevent spec-gaming. A change MUST clear the gate only when it achieves at least a 90% pass rate AND causes zero regressions versus the current baseline (no previously-passing case may regress).
- **FR-044**: Agents MUST NOT self-declare success; completion MUST be verified against explicit acceptance criteria.
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
- **Event**: A typed, timestamped, attributable record (action / observation / thought / tool receipt) appended to the log — the single source of truth.
- **Tool**: A self-describing capability with input schema, per-invocation safety/permission checks, and capability metadata; may be a built-in or a per-tenant permission-scoped connector.
- **Connector Authorization**: A per-user OAuth grant (access + refresh tokens, scopes, expiry) binding a `User` to an external connector, stored only in the per-tenant vault keyed by `(tenant, user, connector)`, auto-refreshed and user-revocable, never exposed to the model.
- **Surface Identity**: A verified binding from an external consumer-surface identity (e.g., Telegram/Zalo user id) to a platform `User` within a tenant; required before any action runs from that surface.
- **Model/Provider**: A pluggable backend accessed only through one abstraction with a normalized stream contract and deterministic, auditable routing.
- **Tenant**: The first-class isolation boundary for data, secrets, budgets, rate limits, workspaces, and audit.
- **User**: The delegated identity whose permission scope the agent acts within; provisioned just-in-time on first valid sign-in against the tenant's configured OIDC issuer (identified by `(tenant_id, external_subject)`), never registered separately in-platform.
- **Skill**: A versioned, progressively disclosed procedure; growable by the agent only through a human/eval promotion gate.
- **Memory**: Per-tenant, retention-bounded durable knowledge injected immutably at session start after injection screening.
- **Budget / Cost Record**: Per-task and per-tenant token/cost accounting with hard ceilings and an explicit exhaustion reason.
- **Audit Receipt**: A tamper-evident record binding a mutating action to session, tool, args, result, and timestamp.
- **Sandbox / Workspace**: Per-tenant isolated execution environment from a warm pool with TTLs, per-tenant caps, and hard per-sandbox resource limits (CPU/memory/PID/wall-clock) with network default-deny; the trust boundary for all code/shell execution (default backend E2B; Docker/microVM/local-OS isolation as swappable alternatives).

## Success Criteria *(mandatory)*

### Measurable Outcomes

- **SC-001**: A user can complete a representative multi-turn, tool-using task end-to-end through the agent, with completion verified against explicit acceptance criteria on at least 90% of a 20-case evaluation set.
- **SC-002**: 100% of runs terminate with an explicit typed reason; no run exceeds its per-task or per-tenant cost ceiling, and ceiling breaches always stop with a cost-exhausted reason plus an alert (zero surprise overruns).
- **SC-003**: Steady-state turns achieve greater than 90% cache-read, and orchestration delivers a materially lower cost and latency per completed task versus an unoptimized baseline (target directionally: roughly −40% cost and −40% latency at equal quality).
- **SC-004**: 100% of actions are attributable to a specific user and tenant in an immutable audit log, and cross-tenant data/secret/budget access is impossible in isolation tests (zero leakage).
- **SC-005**: 100% of high-impact actions (payments, deletions, external sends, production changes) are blocked pending scoped human approval, and no session performs all three legs of the lethal trifecta unattended.
- **SC-006**: A run interrupted mid-task resumes from its last checkpoint and completes without restarting from scratch; a deploy during active runs cuts over zero in-flight tasks mid-task.
- **SC-007**: The same build is deployed in at least two topologies (e.g., multi-tenant SaaS and self-hosted) purely by configuration, and a new organization is onboarded with zero kernel code changes.
- **SC-008**: The platform sustains thousands of concurrent long-running sessions with a met queue-wait SLA — p95 queue-wait under 5s for interactive runs and under 60s for batch/async runs, with first-token latency under 2s for interactive runs — and under overload it degrades gracefully (admission control / fair scheduling / load-shedding) with zero cascading collapse.
- **SC-011**: The platform meets a monthly availability SLA of ≥99.9% for the control plane / API and ≥99.5% for agent-run completion; SLA attainment is measured and reported, and breaches trigger an alert.
- **SC-009**: 100% of prompt/tool/model/skill changes pass the evaluation gate in CI before release — defined as ≥90% pass rate on the eval set and zero regressions versus the current baseline — and the agent cannot edit held-out grader tests.
- **SC-010**: Every failure is classified before retry with zero silent retries, and identical failing calls are circuit-broken within three attempts.
- **SC-012**: A user completes representative common tasks (e.g., summarize unread email, find a Drive document, schedule a calendar event) from both a Telegram and a Zalo chat with identical control flow and terminal reasons to the API surface; every personal connector is authorized by per-user OAuth with tokens vaulted per `(tenant, user, connector)` and never present in any prompt/transcript/log; 100% of high-impact connector actions block pending scoped approval; and an unverified external chat identity performs zero actions.

## Assumptions

- The feature request "build agent in draft-plan.md" refers to the complete Enterprise Agent Master Plan in [draft-plan.md](draft-plan.md); this specification captures that plan's WHAT/WHY. It is intentionally large and is expected to be delivered in phases (kernel → harness → reliability/context → surfaces/skills → trust surface → scale/compliance), with each phase a shippable, testable increment.
- User stories are prioritized so P1 (the reliable kernel) is a standalone MVP; each subsequent story adds an independently testable slice.
- The platform is model- and provider-agnostic; the specific models/providers are configuration and may change over the platform's life without redesign.
- "Cost" is the primary stop signal; concrete default ceilings (e.g., per-task and per-tenant limits) are configurable per tenant and per deployment tier, using industry-standard defaults where unspecified.
- Evaluation, retention, region-pinning, and compliance obligations (e.g., SOC 2, GDPR/CCPA, HIPAA, PCI-DSS) apply only at the deployment tiers that require them and are satisfied by configuration/artifacts, not kernel forks.
- The platform aligns with and is governed by the project constitution ([.specify/memory/constitution.md](.specify/memory/constitution.md)); where any detail here conflicts with the constitution, the constitution wins.
- Standard secure-by-default practices apply to unspecified details (user-friendly error handling, session-based/OAuth2 auth for web surfaces, allowlisted egress, least-privilege identity).
```
