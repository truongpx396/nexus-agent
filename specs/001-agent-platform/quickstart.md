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

**Local isolation backend.** These scenarios run `isolation: container` — the same
image, the same hardening (`network=none`, all capabilities dropped, read-only
root, CPU/memory/PID/wall-clock caps), without `--runtime=runsc`. The FR-059
default (`gvisor`) is Linux-only and is not available under Docker Desktop on
macOS or Windows, so it is exercised on a Linux CI runner rather than on a
developer laptop. Every sandbox assertion below is a *policy* assertion and holds
under either backend; what a local run cannot exercise is escape resistance, which
is the one property the isolation backend exists to provide.

## Setup

```bash
# From repo root
docker compose up -d postgres redis            # state store + cache
make migrate                                   # apply migrations incl. RLS policies
make seed-tenant TENANT=acme                   # one tenant + agent + a demo skill
make run-control-plane &                        # auth, RBAC, budgets, routing
make run-worker &                               # stateless kernel worker
```

Expected: `make migrate` reports RLS enabled on every tenant-scoped table **and
that tenant scope is set transaction-locally** (`SET LOCAL`), which is what keeps
isolation intact behind the transaction-pooling tier; the control plane logs a
`v1` control/data-plane handshake
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
- The agent uses built-in workspace-restricted filesystem tools and a sandboxed
  shell; neither can escape the session workspace or reach another tenant (FR-056,
  FR-057). Code runs in a gVisor-default sandbox — `container` locally, per
  Prerequisites — with hard CPU/memory/PID/wall-clock
  limits and network default-deny — a runaway loop is killed and reclaimed, and an
  unapproved egress attempt is denied (FR-059).
- Force the ceiling (`budget_per_task_usd` small) → the *next turn's reservation is
  refused before the model call*, so the run ends `cost_exhausted` without ever
  overshooting — never a runaway (FR-083).
- Per-turn cost records split input tokens into uncached / cache-read / cache-write
  and name a `price_book_version`, so the cache-read rate is measured rather than
  estimated (FR-016, FR-084).
- Cancel the run (`POST /v1/runs/<session_id>/cancel`) → it terminates `aborted`,
  any outstanding `tool_use` gets a synthetic `tool_result`, and the best partial
  artifact is returned (FR-004, FR-067).

## Scenario 2 — Same agent, many surfaces (User Story 2, P2)

**Goal**: identical control flow and guarantees across ≥3 surfaces (FR-028, FR-031).

```bash
nexus run "summarize open incidents"                 # CLI surface
curl -sX POST localhost:8080/v1/runs -d '{...}'      # API surface
# post the same task via the chat adapter
```

**Expected**: all three produce equivalent control flow and terminal reasons; no
surface-specific fork of agent logic; long runs stream/poll (no blocked connection).

```bash
make surface-conformance SURFACE=telegram   # produce the capability descriptor
make verify-approval-routing                # a class no channel can serve is refused AT CONFIG TIME
make verify-multi-principal                 # each turn runs under its own submitter's scope
make verify-delivery-once                   # crash mid-send => exactly one user-visible message
make verify-agent-ingress                   # an agent principal resolves nothing
```

**Expected**: a surface without a conformance run is not enablable; an
`ApprovalPolicy` routing an irreversible class to channels that can render neither
blast-radius fields nor a step-up challenge is **rejected when configured**, not
when it expires; in a shared channel every turn is authorized against the principal
who submitted it and a non-submitting, non-operator steer is refused and audited; a
worker killed mid-send produces no duplicate reply and a permanently undelivered
approval request is distinguishable in the audit record from an unanswered one; and
an agent-principal caller runs under a subset scope, is source-tainted, is metered
to a named payer, and is refused as both an approval resolver and an input answerer
(FR-155–FR-159).

## Scenario 3 — Enterprise trust & isolation (User Story 3, P2)

**Goal**: tenant isolation at the data layer, attributable audit, secret handling,
and human approval on high-impact actions (FR-032–FR-040).

```bash
# Run the same task for two tenants concurrently, then attempt a cross-tenant read
# — routed THROUGH the PgBouncer transaction-pooling tier used in production:
make verify-isolation TENANT_A=acme TENANT_B=globex
# Trigger a high-impact action (e.g. external send) and leave the approval unanswered:
make verify-approval-timeout
# Prove the approval is a transaction: argument substitution after the grant,
# resolution by an unauthorized principal, and a decision arriving after cancel:
make verify-approval-binding
make verify-approval-authz
make verify-approval-invalidation
# Prove the audit log is tamper-evident, and that erasure preserves it:
make verify-audit-chain
make verify-erasure SUBJECT=<user_id>
```

**Expected**:
- Cross-tenant query returns zero rows (Postgres RLS — FR-039) **including on a
  pooled connection previously used by the other tenant**; no leakage of data,
  secrets, or budgets. A test asserting isolation against a direct connection only
  does not satisfy this scenario (SC-013).
- Every mutating action has an immutable audit receipt binding user + tenant + tool
  + args + result + timestamp, **hash-chained** to its predecessor and covered by a
  current external anchor; injected tampering (modify / delete / reorder) is
  detected by `make verify-audit-chain` (FR-040, FR-081, SC-015).
- The vault-injected credential never appears in the prompt/transcript (FR-034).
- The unanswered approval expires as a denial of **the action** after its TTL and
  its notify → remind → escalate stages: the agent receives a typed denial (with
  the approver's rationale where one exists) and may replan; only a run that cannot
  proceed ends `approval_expired`, still returning its partial artifact. The gated
  action did **not** proceed either way (FR-036, FR-067, FR-107, FR-108).
- `make verify-approval-binding` grants an approval, substitutes an argument, and
  confirms execution is refused with `approval_mismatch` and **zero** external
  effects — the grant bound the digest of the resolved call, and that same digest
  is the exactly-once dedup key (FR-103, FR-071, SC-024).
- `make verify-approval-authz` confirms a resolution is refused — and audited —
  when attempted by an agent principal, by the run's own initiator on an
  irreversible class, with a replayed channel token, or without the effect-class
  approve scope; and that step-up re-authentication is demanded where the tenant's
  policy requires it (FR-105, SC-025).
- `make verify-approval-invalidation` cancels a run holding a pending approval and
  confirms the approval is invalidated *before* the terminal event, the gated
  `tool_use` gets its paired synthetic result, and a decision arriving afterwards
  performs nothing and is recorded as a refused resolution (FR-106, SC-026).
- `make verify-erasure` destroys the subject's content key: payloads become
  unrecoverable while the event log still replays and the audit chain still
  verifies — no event row deleted or rewritten (FR-080, SC-014).

## Scenario 4 — Cost governance & observability (User Story 4, P2)

**Goal**: per-turn metering attributed to task + tenant, a content-free trace that
joins to the event log, audited content access, and an eval gate in CI (FR-016,
FR-040, FR-043, FR-117–FR-124).

```bash
make trace SESSION=<session_id>       # decision structure + per-turn cost/latency/token
make verify-telemetry-content-free    # inject content at every call site; assert nothing exports
make adapter-conformance ADAPTER=litellm   # capability matrix: supported / degraded / unsupported
make verify-no-vendor-sdk             # exactly one telemetry write path (OTLP behind the allowlist)
make verify-trace-join SESSION=<id>   # span → event range → span, both directions
make content-access SESSION=<id> PURPOSE="incident 4821"   # request a scoped, expiring grant
make evals                            # k trials/case, per-case intervals, three-valued verdict
make evals-calibrate                  # judge vs human-labelled gold set; kappa floor 0.6
make evals-baseline                   # record the current baseline + environment digest
make verify-online-scoring            # production quality score with zero content egress
```

**Expected**:
- Each turn records input/output tokens, latency, and cost attributed to the task
  chain and tenant; the trace shows structure **without** conversation content.
- `verify-telemetry-content-free` shows 100% of injected content-shaped values
  dropped by the attribute allowlist before any exporter, and no flag exists that
  would change that (FR-117, SC-033).
- `verify-trace-join` resolves every span to the exact event range it covers and
  every event back to its trace — including for a suspended run and a run whose
  worker was killed mid-turn (FR-119, FR-120, SC-035).
- `content-access` fails without an authorizer distinct from the requester, and on
  success emits a chained receipt for the grant **and** for each read; the grant
  expires and cannot satisfy an approval (FR-118, SC-034).
- `adapter-conformance` records a capability matrix per adapter; a gateway that
  cannot report cache-read tokens separately, or cannot hold cache affinity for the
  provider's real cache lifetime, is marked `degraded` and the cache-read gate is
  **not claimed** on that path rather than estimated. A model substituted by a
  gateway alias or auto-fallback is a typed failure, never a silent success
  (FR-132, FR-133, SC-041, SC-042).
- `verify-no-vendor-sdk` fails the build if any vendor tracing SDK,
  auto-instrumentation agent, or framework callback hook is present — telemetry has
  exactly one write path (FR-134, SC-043).
- The eval gate passes only at **≥90% `pass^k` on the regression class AND zero
  cases whose interval separates downward from baseline** — a *statistical*
  verdict over k trials, not a single run (FR-137). A change the corpus cannot
  resolve returns `inconclusive`, which gathers more trials and then blocks for
  human adjudication; it is never resolved as a pass. A `safety`-class case
  failing one trial blocks with no waiver. A change that holds quality while
  regressing tokens or turns beyond the declared band is blocked too (FR-145).
- `evals` refuses to compare against a baseline recorded under a different
  `eval_environment_digest`, provisions a **cold** sandbox per trial rather than
  drawing from the warm pool, and excludes `infra_error` outcomes from the
  pass-rate denominator (FR-138, SC-045). The isolation backend is inside that
  digest, so a local `container` run is refused against a CI `gvisor` baseline by
  construction — local eval numbers are for iterating on cases, never for clearing
  the gate (FR-059).
- `evals-calibrate` must have passed before the gate can block anything: a judge
  with no current calibration, or one that has drifted below the κ floor, is out
  of service — that condition reads as an instrument failure, not as an agent
  regression (FR-141, SC-048).
- `verify-online-scoring` re-runs the content-injection sweep end to end through
  the in-boundary scorer's export path: quality is scored on production traffic,
  100% of content-shaped values are dropped, the scorer's reads appear in the
  audit chain, and a seeded regression on a canary slice trips the guardrail and
  auto-rolls-back (FR-140, SC-047).
- Held-out grader tests are not agent-editable, the grader store has no route from
  any sandbox, and every run reports the visible-versus-held-out gap as a
  reward-hacking signal (FR-146, SC-053). Production
  cases enter the corpus only through the consented, redacted, governance-signed
  export — never by reading telemetry (FR-125).

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

```bash
make import-skill SRC=<registry-url> TENANT=acme   # third-party import path
make verify-skill-admission                         # unsigned/unpinned import => refused
make verify-skill-narrowing                         # a skill cannot grant itself a tool
```

**Expected**: an import is refused without publisher provenance, a valid signature
over `bundle_digest`, and a pinned upstream version — never admitted at a reduced
trust tier; every file in the bundle clears the injection scan and a bundle
declaring `executable = false` that carries executable content is refused rather
than sanitized; a bundled script is registered as a `Tool` through the ordinary
three gates or the bundle fails; and a skill declaring a tool the run does not hold
has the declaration **ignored and recorded** rather than honoured, with every
activation reconstructable from the log by `bundle_digest` (FR-151–FR-154).

## Scenario 5b — Bounded delegation (User Story 5, P3)

**Goal**: a sub-agent can only ever hold *less* than its parent, cannot launder
taint, cannot outlive its parent, and cannot hide its cost (FR-098–FR-101).

```bash
make delegate-escalation-probe          # child requests a tool/connector the parent lacks
make delegate-fanout N=32               # exceed the per-run child bound on purpose
make delegate-cancel-parent             # cancel a parent mid-fan-out
```

**Expected**: every escalation attempt fails closed (the child's resolved scope is
a proven subset of the parent's, and approval scopes never descend); the fan-out
stops at the configured bound with a **non-retryable** result and the agent routes
around it rather than looping; the fan-out never overspends its pre-reserved
envelope nor starves a concurrent sibling session in the same tenant; cancelling
the parent leaves **zero** children spending tokens and each outstanding delegation
carries a synthetic paired result; a returned summary is truncated by the platform,
schema-validated, judged against its acceptance criterion, and still counts as
untrusted content; every cost record and receipt in the tree resolves to root +
parent + depth.

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
make chaos-crash-mid-effect SESSION=<id>  # kill between dispatch and result of a paying call
make fork SESSION=<failed_id> AT_SEQ=<n> PROMPT=<patched>  # reproduce with effects disabled
make deploy-during-run                 # rainbow deploy while a run is active
make load-test CONCURRENCY=5000        # drive past capacity
make capacity-check                    # measure SC-008 SLAs + gate high-concurrency go-live
make restore-drill                     # measure RPO/RTO; chain must verify + log must replay
```

**Expected**: the run resumes from its last checkpoint preserving partial work —
restoring the in-flight claim, held reservation, sandbox handle, and pending
approval, none of which a context compaction could supply (FR-126);
`chaos-crash-mid-effect` leaves an `in_flight` claim that resume resolves by probe
or human escalation, never by re-charging and never by silent discard (FR-127,
SC-036); `make fork` reproduces the failed trajectory in a new run with external
effects disabled, leaving the source run's cost, approvals, and audit chain
untouched and reporting any harness-digest divergence (FR-128, FR-129, SC-037);
in-flight runs are not cut over mid-task; under overload the system applies
admission control / fair scheduling / load-shedding (429 + `Retry-After`) and
degrades gracefully instead of collapsing; identical failing calls circuit-break
within three attempts with logged reasons (no silent retries); the measured p95
queue-wait / first-token / completion-rate meet the SC-008 targets under sustained
concurrency; and `make restore-drill` meets RPO ≤5 min / RTO ≤4 h with the audit
chain verifying and the event log replaying after restore (FR-090, SC-018).

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
make connect-connector CONNECTOR=notion USER=<user_id>    # per-user OAuth for Notion
curl -s localhost:8080/v1/connectors -H 'Authorization: Bearer <oidc>'   # list linked accounts
curl -sX DELETE localhost:8080/v1/connectors/gmail -H 'Authorization: Bearer <oidc>'  # revoke
```

**Expected**:
- A forged, replayed, or flooding webhook delivery is rejected **before** adapter
  translation — zero kernel invocations and zero token spend — while a correctly
  signed delivery proceeds (FR-082, SC-019).
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

## Scenario 9 — Deterministic multi-step processes (User Story 9, P2)

**Goal**: a recurring process is a versioned, reviewed, eval-gated artifact whose
control flow the platform evaluates at **zero model-token cost** — the model works
inside a step, never between steps (FR-102).

```bash
make plan-validate PLAN=specs/plans/triage.yaml   # reachability, bounded loops, closed predicates, scope subset
make plan-enable PLAN=triage                       # refused until eval gate + governance sign-off
make run-plan PLAN=triage@3 INPUT=<incident_id>    # run the pinned version
make run-plan PLAN=triage@3 INPUT=<incident_id>    # run it again — same path
make plan-replay RUN=<run_id>                      # reconstruct from the event log alone
```

**Expected**: validation rejects an unreachable step, an unbounded loop, a predicate
that is not a closed expression, or a step requesting a capability the plan
principal lacks — before the plan can ever run; `plan-enable` is refused while the
plan lacks an eval-gate run or a recorded governance sign-off; the two runs take the
**identical** path and the run report shows **zero model tokens spent on transitions
between steps**; an in-flight run finishes on the agent version and model route
pinned at plan start even if a deploy lands mid-run, and an edit publishes a new
version rather than mutating the running one; an `approval_gate` step suspends
durably at zero ongoing token cost and an unanswered approval expires as a denial of
that step; `plan-replay` reconstructs every step entry, **the predicate that matched
at each transition**, each outcome, and the terminal reason from the log alone; and
an interrupted run resumes at the last completed step with its cost envelope
reconciled rather than double-reserved.

---

## Go-live gate (FR-045)

Before any production launch, confirm the checklist is green: attributable audit
with a **verifying hash chain and a current anchor**, vaulted per-tenant secrets,
**content encrypted at rest with an exercised erasure path**, sandboxing + human
approval for high-impact actions, one leg of the lethal trifecta broken per risky
flow, **pre-spend** per-task/per-tenant cost ceilings, failure classification +
resume + stuck detection, evals green in CI, >90% steady-state cache-read measured
from recorded token classes, **isolation verified through the connection pooler**,
a **restore drill within the last quarter meeting RPO/RTO**, SBOM + signed
artifacts, error-budget alerting wired to the runbook, documented
residency/retention/no-train, and a rehearsed incident runbook.

```bash
make go-live-check     # asserts every checklist item; non-green blocks launch
```
