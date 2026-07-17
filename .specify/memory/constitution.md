<!--
SYNC IMPACT REPORT
==================
Version change: (unversioned template) → 1.0.0
Bump rationale: MAJOR — initial ratification of the project constitution from the
  template placeholders, establishing the full governing principle set.

Modified principles: N/A (initial adoption; all principles newly defined)

Added sections:
  - Core Principles (I–IX)
  - Security & Trust Surface (Additional Constraints)
  - Delivery, Scale & Technology Constraints
  - Development Workflow & Quality Gates
  - Governance

Removed sections: none (template placeholders replaced in full)

Templates requiring updates:
  - .specify/templates/plan-template.md ✅ compatible (Constitution Check gate is
    generic; no principle-name coupling requiring edits)
  - .specify/templates/spec-template.md ✅ compatible (no changes required)
  - .specify/templates/tasks-template.md ✅ compatible (no changes required)
  - .github/agents/speckit.*.agent.md ✅ no outdated agent-specific references found

Follow-up TODOs: none
-->

# Nexus Agent Constitution

Nexus Agent is a single, model-agnostic, production-grade AI agent platform: one
reliable kernel loop wrapped in a carefully engineered harness, exposed through
thin surface adapters, fronted by a control plane, and grounded in a trust
surface. This constitution encodes the non-negotiable rules that govern how it is
designed, built, and shipped. When a design conflicts with a principle here, the
design loses.

## Core Principles

### I. One Loop, Many Surfaces

A single agent kernel MUST power every surface (CLI, chat, API, cron, email, IDE).
Surfaces are thin translators of input/output and MUST NOT fork or re-implement
agent control flow. The loop is the only place control flow lives and MUST be a
single async generator with typed, exhaustively-handled terminal states.

**Rationale**: Forked logic per surface multiplies bugs and defeats replay, audit,
and consistent behavior. Concentrating control flow in one small kernel keeps it
debuggable and lets every surface inherit the same guarantees.

### II. Immutable Models, Append-Only State

Agent, Tool, LLM, and configuration objects MUST be immutable. The only mutable
runtime state is the conversation state, which MUST change by appending typed
events to an append-only event log — never by in-place mutation. Every
`tool_use` MUST have a paired `tool_result` (a synthetic result on any
cancel/error path) before the next model call.

**Rationale**: An append-only event log is the single source of truth that buys
replay, deterministic debugging, tamper-evident audit, and safe parallelism. The
paired-result invariant is the #1 correctness rule; violating it corrupts
transcripts and produces production failures.

### III. Cache-Stable Context Is Architecture

The prompt MUST be structured as a byte-stable prefix (stable system prompt, tool
schema catalog, append-only transcript) followed by a volatile tail rebuilt each
turn. Anything per-turn is structurally banned from the prefix; the system prompt
MUST NOT be mutated mid-session. Context management MUST target >90% cache-read on
steady-state turns; compaction MUST be structured and run off the paying loop.

**Rationale**: Input tokens are ~90% of the bill and cache-read is the single
highest-leverage cost and throughput lever. Cache stability is architecture, not a
late optimization — every design either preserves cache hits or is rejected.

### IV. Stop on Cost, Not Vibes

Cost MUST be the primary stop signal for every run; iteration count and wall-clock
timeout are backstops only. Every task chain MUST meter input and output tokens
per turn and enforce hard per-task and per-tenant cost ceilings that terminate
with an explicit `cost_exhausted` reason. Quality-per-dollar and completions-per-
million-tokens MUST appear in every release gate alongside quality.

**Rationale**: Step counts vary ~5× across models, so counting steps is a false
signal; token usage explains most task-performance variance. Metering in the same
layer that spends tokens makes cost observable, boundable, and attributable.

### V. Safety Is Per-Invocation and Fails Closed

Safety MUST be evaluated per invocation on parsed input, not per tool
(`Bash("ls")` ≠ `Bash("rm -rf")`). Tools MUST default to fail-closed (serial
unless proven concurrency-safe, assume writes, permission denied unless explicitly
granted). Defense MUST be layered (channel, autonomy, workspace, shell, sandbox,
audit), and all tool output and retrieved content MUST be treated as untrusted and
never fed straight into execution. Within a session, at most two of {process
untrusted input, access private data, change state or communicate externally} are
permitted without human approval (the Rule of Two).

**Rationale**: Prompt injection is unsolved; a "95% blocked" guardrail is a failing
grade. Safety must be designed in by breaking the lethal trifecta and failing
closed, so a forgotten flag yields slow behavior, never data loss or a breach.

### VI. Tenant Is the First Dimension; Audit and Observability Are Day-One

Tenant identity MUST be the first dimension of every session key, database row,
workspace path, cost record, and secret. Data isolation MUST be enforced at the
database (row-level security), never by application ACLs alone. Every action MUST
be attributable to a user and tenant in an immutable audit log, and per-turn
token/cost/latency observability MUST exist from the first pilot. Secrets, budgets,
and rate limits MUST be isolated per tenant.

**Rationale**: The enterprise tax — multi-tenancy, audit, observability — is a
day-one requirement; retrofitting it is a rewrite. DB-level isolation is the only
defense that survives an application bug.

### VII. Model- and Provider-Agnostic by Abstraction

All provider access MUST go through one internal abstraction with a single
normalized stream contract; scattered SDK calls are prohibited. Native tool-calling
only — no parsing tools out of free-form text. Model routing MUST be deterministic
and auditable, decided by data label and task difficulty (including a capability
floor for feature demand), never by model discretion; regulated payloads MUST be
routable to a self-hosted in-VPC model.

**Rationale**: The model is roughly fixed for the life of the project and the
harness is the durable asset. Standardizing the harness (not the model) enables
multi-provider failover, capacity spreading, and no-egress compliance without code
forks.

### VIII. Reliability: Classify, Resume, Never Silently Retry

Every failure MUST be classified into a typed class before any retry; retries MUST
be logged with a reason, backed off with jitter, and circuit-broken after an
identical failing call repeats three times. Silent retries are prohibited. State
MUST be checkpointed to durable storage so runs resume from the last checkpoint
rather than restarting. Stuck detection (repeated actions, oscillation, zero net
change) MUST break the loop, and deploys MUST NOT cut a running agent over
mid-task.

**Rationale**: In agentic systems minor issues derail agents entirely and errors
compound; classification, durable resume, and circuit-breaking convert fragile
long runs into recoverable ones and keep failure spend bounded and observable.

### IX. Verify Against Acceptance Criteria; Govern Every Behavior Change

Agents MUST NOT self-declare success; completion MUST be verified against explicit
acceptance criteria (tests pass, build green, schema validates, end-state graded).
Prompts, tools, and skills are production config and MUST be versioned, code-
reviewed, and eval-gated; a prompt or model change is a deploy. Agent-written
skills MUST follow propose → human/eval gate → version → promote, and MUST NEVER be
auto-promoted.

**Rationale**: Non-determinism and spec-gaming make self-reported success
unreliable. Treating prompts/tools/skills as governed, eval-gated config is what
separates a demo from a system that can be safely changed and audited.

## Security & Trust Surface

These constraints make the platform signable by a security-conscious enterprise and
are binding in addition to Principle V:

- **Secrets**: Never placed in the prompt. Injected at tool-execution time from a
  vault; the model sees only a handle. Credentials MUST be isolated per tenant.
- **Identity**: The agent MUST act with the calling user's RBAC scope (act-as
  delegated identity), never a god-mode service account. RBAC is enforced at the
  tool boundary, not just the UI.
- **Audit receipts**: Mutating actions MUST produce tamper-evident tool receipts
  (HMAC over session + tool name + args + result + timestamp).
- **Egress & redaction**: Outbound domains MUST be allowlisted; PII/secrets/PHI/
  card data MUST be masked by class before leaving the trust boundary. Sensitive
  tasks SHOULD route to a self-hosted model so payloads never leave.
- **Human-in-the-loop**: Payments, deletes, external sends, and production changes
  MUST be gated by scoped human approval; the sandbox is the trust boundary.
- **Compliance**: Data residency/region pinning, retention limits, DSAR support, a
  no-train guarantee, model/prompt versioning, and an AI risk register MUST be
  maintained where the deployment tier requires them.

## Delivery, Scale & Technology Constraints

- **Control-plane / data-plane split**: The control plane (auth, RBAC, routing,
  budgets, eval/skill/MCP catalogs, audit sink) and the data plane (kernel loop,
  sandboxes, memory, provider calls) MUST remain separately deployable behind a
  versioned contract, so "move the data plane into the customer VPC" is a
  deployment flag, not a rewrite. The same build MUST serve multi-tenant SaaS,
  single-tenant, self-hosted/BYOC, and hybrid topologies via configuration.
- **Configuration, not forks**: Per-organization behavior (tenant config, agent
  definition, skills, surfaces, connectors) MUST be data/config read at runtime.
  The kernel MUST NEVER be forked per customer. Connectors MUST attach only through
  the vetted, per-tenant, RBAC-scoped MCP catalog.
- **Runtime is stateless with externalized state**: Agent runs are async jobs on a
  durable queue processed by stateless, disposable workers. Routing MUST be by
  session key (per-session serial, cross-session concurrent). Sandboxes MUST come
  from a warm pool with hard TTLs, reclamation, and per-tenant caps. Provider TPM
  MUST be handled via per-tenant rate limits, connection pooling, failover-as-
  capacity, and cached prefixes.
- **Memory is files first**: Start file-based, inject immutably at session start
  (updates take effect next session), scope per tenant with retention limits, and
  scan for injection/exfiltration before injecting. A vector DB / knowledge graph
  is introduced only when the data shape and scale (past ~1M tokens of durable
  knowledge, or genuinely graph-shaped relational data) justify it.
- **Build for the current stage**: Do not build a later stage's infrastructure
  early. Each phase MUST produce a shippable, testable increment.

## Development Workflow & Quality Gates

- **Evals as the release gate**: A real eval set (starting ~20 cases) with an
  LLM-as-judge rubric and end-state checks MUST run in CI and gate any prompt,
  tool, model, or skill change. Track pass rates over N runs; hold out grader tests
  the agent cannot edit to prevent spec-gaming.
- **Prompts/tools/skills are reviewed config**: Changes go through version control
  and code review like any release; a governance/risk function signs off new tools
  and autonomy levels.
- **Observability captures structure, not content**: Decision patterns and per-turn
  cost/latency/token spans MUST be inspectable without reading conversation
  content; the actual prompt/response MUST remain inspectable for debugging.
- **Go-live gate**: No production launch without the go-live checklist green —
  attributable audit log, vaulted per-tenant secrets, sandboxing + human approval
  for high-impact actions, at least one leg of the lethal trifecta broken per risky
  flow, per-task/per-tenant cost ceilings, failure classification + resume + stuck
  detection, evals green in CI, cache-read >90% steady-state, documented data
  residency/retention/no-train, and a rehearsed behavioral-incident runbook.

## Governance

This constitution supersedes all other development practices. Where a plan, spec,
task list, or code review conflicts with it, the constitution wins and the conflict
MUST be resolved before merge.

- **Amendments** MUST be proposed in writing with rationale, reviewed by the
  platform and governance/risk functions, and accompanied by any required migration
  plan and template/artifact updates.
- **Versioning** follows semantic versioning: MAJOR for backward-incompatible
  governance or principle removals/redefinitions, MINOR for a newly added or
  materially expanded principle/section, PATCH for clarifications and non-semantic
  refinements.
- **Compliance review**: Every PR and design review MUST verify compliance with the
  Core Principles; the Constitution Check gate in the plan template is the
  enforcement point. Any added complexity MUST be justified against these
  principles.
- **Runtime guidance**: Agent-specific and contributor guidance files are
  subordinate to this constitution and MUST be kept consistent with it.

**Version**: 1.0.0 | **Ratified**: 2026-07-17 | **Last Amended**: 2026-07-17
