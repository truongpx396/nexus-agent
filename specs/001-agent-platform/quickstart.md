# Quickstart: Validating the Agent Platform

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [plan.md](plan.md)

This is a **validation / run guide** — runnable scenarios that prove each user
story works end-to-end. It references [data-model.md](data-model.md) and
[contracts/](contracts/) instead of duplicating them. Implementation code lives in
`tasks.md` and the implementation phase, not here.

The scenarios map 1:1 to the spec's user stories and are ordered by priority, so
the Phase 0 (P1) kernel is independently testable before later slices exist.

---

## Prerequisites

- Go 1.23, Python 3.12, Node 20+ (for the web surface)
- Docker (Postgres, Redis, sandbox images)
- A configured provider credential in the vault (never in env/prompt — FR-034)

## Setup

```bash
# From repo root
docker compose up -d postgres redis            # state store + cache
make migrate                                   # apply migrations incl. RLS policies
make seed-tenant TENANT=acme                   # one tenant + agent + a demo skill
make run-control-plane &                        # auth, RBAC, budgets, routing
make run-worker &                               # stateless kernel worker
```

Expected: `make migrate` reports RLS enabled on every tenant-scoped table; the
control plane logs a `v1` control/data-plane handshake
([contracts/control-data-plane.md](contracts/control-data-plane.md)).

---

## Scenario 1 — Reliable single-agent loop (User Story 1, P1)

**Goal**: a multi-turn, tool-using task completes with a typed terminal reason and
stops on cost, not vibes (FR-001–FR-004, FR-016, FR-017).

```bash
curl -sX POST localhost:8080/v1/runs \
  -H 'Authorization: Bearer <oidc>' \
  -d '{"agent_id":"<id>","input":"triage this bug and propose a fix","data_label":"internal"}'
# → 202 { session_id, status: "queued" }

curl -N localhost:8080/v1/runs/<session_id>/events   # SSE, structure only
```

**Expected outcomes**:
- At least one `tool_use` event, each paired with a `tool_result` before the next
  model call (inspect `pair_ref`; synthetic result on any error path).
- Terminal event carries a typed `terminal_reason` from the enum in
  [contracts/kernel-abi.md](contracts/kernel-abi.md).
- Force the ceiling (`budget_per_task_usd` small) → run ends `cost_exhausted` with
  an alert, never a runaway.

## Scenario 2 — Same agent, many surfaces (User Story 2, P2)

**Goal**: identical control flow and guarantees across ≥3 surfaces (FR-028, FR-031).

```bash
nexus run "summarize open incidents"                 # CLI surface
curl -sX POST localhost:8080/v1/runs -d '{...}'      # API surface
# post the same task via the chat adapter
```

**Expected**: all three produce equivalent control flow and terminal reasons; no
surface-specific fork of agent logic; long runs stream/poll (no blocked connection).

## Scenario 3 — Enterprise trust & isolation (User Story 3, P2)

**Goal**: tenant isolation at the data layer, attributable audit, secret handling,
and human approval on high-impact actions (FR-032–FR-040).

```bash
# Run the same task for two tenants concurrently, then attempt a cross-tenant read:
make verify-isolation TENANT_A=acme TENANT_B=globex
# Trigger a high-impact action (e.g. external send) and leave the approval unanswered:
make verify-approval-timeout
```

**Expected**:
- Cross-tenant query returns zero rows (Postgres RLS — FR-039); no leakage of data,
  secrets, or budgets.
- Every mutating action has an immutable audit receipt binding user + tenant + tool
  + args + result + timestamp (FR-040).
- The vault-injected credential never appears in the prompt/transcript (FR-034).
- The unanswered approval expires as denial after its TTL → run ends
  `approval_expired`; the gated action did **not** proceed (FR-036).

## Scenario 4 — Cost governance & observability (User Story 4, P2)

**Goal**: per-turn metering attributed to task + tenant, a structure-only trace,
and an eval gate in CI (FR-016, FR-040, FR-043).

```bash
make trace SESSION=<session_id>       # decision structure + per-turn cost/latency/token
make evals                            # runs the ~20-case set with the LLM-as-judge
```

**Expected**:
- Each turn records input/output tokens, latency, and cost attributed to the task
  chain and tenant; the trace shows structure **without** conversation content.
- The eval gate passes only at **≥90% pass AND zero regressions** vs baseline; a
  prompt/model/tool/skill change that regresses any previously-passing case is
  blocked in CI (FR-043); held-out grader tests are not agent-editable.

## Scenario 5 — Memory & skills (User Story 5, P3)

**Goal**: per-tenant memory injected immutably at session start, progressive-
disclosure skills, gated skill promotion (FR-019–FR-021).

```bash
make seed-memory TENANT=acme FILE=MEMORY.md
make run "use our deploy runbook"     # relevant skill loads on demand
make verify-skill-promotion            # an agent-proposed skill is NOT auto-promoted
```

**Expected**: memory is injected at session start (not mid-session), scoped to the
tenant, screened for injection first; only a skill's brief description is always
visible; an agent-proposed skill requires human + eval approval before promotion.

## Scenario 6 — Config, not forks (User Story 6, P3)

**Goal**: onboard a new org and deploy in ≥2 topologies with zero kernel changes
(FR-050, FR-030, FR-012).

```bash
make onboard-org ORG=initech          # tenant config + agent def + skills + surfaces + connectors
make deploy TOPOLOGY=saas
make deploy TOPOLOGY=byoc              # same build, data plane in a separate VPC
```

**Expected**: behavior, tools, skills, surfaces, and connectors are all data/config;
the kernel binary is byte-identical across topologies; connectors are per-tenant
RBAC-scoped via the MCP catalog.

## Scenario 7 — Survive failures, deploys, scale (User Story 7, P3)

**Goal**: classify-before-retry, durable resume, stuck detection, rainbow deploy,
and graceful degradation under overload (FR-023–FR-026, FR-046–FR-049).

```bash
make chaos-crash SESSION=<long_task>   # kill the worker mid-run
# → job re-queues; resumes from last checkpoint, not from scratch
make deploy-during-run                 # rainbow deploy while a run is active
make load-test CONCURRENCY=5000        # drive past capacity
```

**Expected**: the run resumes from its last checkpoint preserving partial work;
in-flight runs are not cut over mid-task; under overload the system applies
admission control / fair scheduling / load-shedding (429 + `Retry-After`) and
degrades gracefully instead of collapsing; identical failing calls circuit-break
within three attempts with logged reasons (no silent retries).

## Scenario 8 — Consumer surfaces & personal connectors (User Story 8, P2)

**Goal**: reach the same kernel from Telegram/Zalo and let a user authorize personal
connectors (Gmail/Drive/Calendar) via per-user OAuth, with tokens vaulted, handle-only
credentials, and approval-gated sends (FR-051–FR-055).

```bash
# Link an external chat identity to a platform user (verified binding, FR-055):
make link-surface SURFACE=telegram EXTERNAL_ID=<tg_user_id> USER=<user_id>
# Message the agent from Telegram and Zalo (webhook ingress → same run model):
#   send "summarize my unread email" from the Telegram/Zalo chat
# Authorize a personal connector via per-user OAuth (auth-code + PKCE, FR-052):
make connect-connector CONNECTOR=gmail USER=<user_id>     # opens consent URL, stores token in vault
curl -s localhost:8080/v1/connectors -H 'Authorization: Bearer <oidc>'   # list linked accounts
curl -sX DELETE localhost:8080/v1/connectors/gmail -H 'Authorization: Bearer <oidc>'  # revoke
```

**Expected**:
- Telegram and Zalo messages produce identical control flow and terminal reasons to
  the API surface — thin adapters, no per-surface fork (FR-051).
- The OAuth consent stores access+refresh tokens in the vault keyed by
  `(tenant, user, connector)`, auto-refreshes on expiry, and revoke removes access;
  the token never appears in a prompt/transcript/log (FR-052).
- A connector tool (e.g. `gmail_search`, `drive_search`, `schedule_event`) runs in
  the calling user's own scope with the credential injected at execution time (model
  sees a handle only, FR-054).
- A high-impact action (`gmail_send`, external calendar invite, file delete) blocks
  pending scoped approval and is constrained by the Rule of Two (FR-054).
- An unverified/unlinked Telegram/Zalo identity performs zero actions (FR-055).

---

## Go-live gate (FR-045)

Before any production launch, confirm the checklist is green: attributable audit,
vaulted per-tenant secrets, sandboxing + human approval for high-impact actions,
one leg of the lethal trifecta broken per risky flow, per-task/per-tenant cost
ceilings, failure classification + resume + stuck detection, evals green in CI,
>90% steady-state cache-read, documented residency/retention/no-train, and a
rehearsed incident runbook.

```bash
make go-live-check     # asserts every checklist item; non-green blocks launch
```
