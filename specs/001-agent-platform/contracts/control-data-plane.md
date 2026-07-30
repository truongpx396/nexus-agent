# Contract: Control Plane ↔ Data Plane (versioned)

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

A hard, versioned boundary so the data plane can move into a customer VPC by
configuration, not a rewrite (Constitution Delivery section; FR-030). The control
plane owns policy; the data plane owns execution. Neither bleeds into the other.

**Contract version**: `v1` (semantic; the control plane may talk to a slightly
older data plane during a rainbow rollout — FR-026).

---

## Responsibilities

| Control plane (hosted) | Data plane (may be in customer VPC) |
|------------------------|-------------------------------------|
| AuthN (SSO/OIDC), RBAC (FR-029, FR-035) | Kernel loop execution (FR-001) |
| Rate limiting, admission control (FR-041, FR-049) | Sandbox pool, tool execution (FR-047) |
| Budget **reservation** + ceilings (FR-017, FR-083) | Local hard per-run budget enforcement (FR-083) |
| Model routing decision (FR-037, FR-076) | Provider/model calls (FR-027) |
| Price book distribution (FR-084) | Memory read/write (FR-019) |
| Eval / skill / MCP catalog (FR-042) | Event-log append, checkpoints (FR-024) |
| Approval policy + approver identity/authz (FR-105, FR-109) | Approval **enforcement** at the tool boundary; digest re-verify (FR-103) |
| Approval routing, reminder, escalation — **filtered by surface capability** (FR-108, FR-155) | Approval context rendering under `local` mode (FR-104) |
| Surface registry + capability descriptors, conformance runs (FR-155) | Outbound delivery through the durable outbox (FR-157) |
| Skill admission: origin, provenance, signature, trust tier (FR-152) | Skill activation, capability narrowing, `skill_activated` events (FR-153) |
| Audit sink + chain anchoring (FR-040, FR-081) | Emits audit receipts + cost records upstream |
| Erasure/key-destruction orchestration (FR-080) | Holds content keys per tenant (FR-089) |
| Content-access grant authorization (FR-118) | Enforces the grant at the decryption boundary; emits a receipt per read |
| Telemetry ingest + SLO/burn-rate alerting (FR-095) | Emits **content-free** spans/metrics from the event log (FR-117, FR-120) |

## Data-egress boundary (FR-091)

When the data plane runs inside a customer boundary, **exactly** the following
leaves it, and nothing else:

| Leaves the boundary | Never leaves |
|---------------------|--------------|
| Identifiers (`tenant_id`, `session_id`, `user_id`, `tool_id`, `model_id`) | Prompts, model output, tool arguments, tool results |
| Counts and measures (token classes, latency, cost) | Memory content, retrieved documents, artifacts |
| Digests and signatures (audit chain, approved-input digests) | Any plaintext an event payload contains |
| Typed reasons (terminal reason, failure class, reclaim reason) | Secrets, connector tokens (never leave the vault at all) |
| **Approval context package — only when `approval_context_mode = upstream`** | The approval context package under `local` mode (default for BYOC) |

- `audit_sink_mode` and `telemetry_sink_mode` are per-deployment configuration:
  `upstream` (default for SaaS) or **`local`** — with `local`, the data plane
  keeps its own audit chain and anchors, and *nothing* crosses the boundary
  (required for BYOC tenants who accept no metadata egress). Because spans are
  emitted from the durable event log rather than from in-process state (FR-120),
  `local` telemetry is the same code path with a different sink — not a reduced
  feature set.
- **Telemetry is not an egress exception.** Everything on the left column is
  structural by construction: the export path applies a deny-by-default attribute
  allowlist, so a content-bearing attribute cannot cross the boundary even under a
  debug setting, because none exists (FR-117). This is what keeps the erasure
  attestation (FR-080) true across the telemetry pipeline and not merely across
  the database.
- Region pinning is enforced at admission: a run whose placement would fall
  outside the tenant's pinned region is refused, not relocated.

### `approval_context_mode` — the one content-bearing exception (FR-104, FR-091)

An approver cannot decide on an opaque UUID, and tool arguments are exactly what
this boundary exists to hold back. That tension is resolved by configuration, not
by silently leaking or by shipping an unusable gate:

| Mode | Rendering | What crosses |
|------|-----------|--------------|
| **`local`** (default for BYOC/hybrid) | The data plane renders the package and serves the approval UI in-boundary | Only `approval_id`, effect class, digests, the decision, approver identity, and typed reasons |
| `upstream` (opt-in, SaaS) | The control plane renders it | The redacted `context_package` as an **enumerated egress class**, bound to a `redaction_policy_version` |

- `upstream` MUST be refused when the tenant's residency configuration forbids it,
  and is never implied by `audit_sink_mode` or any other setting.
- Under either mode the package is produced by the tenant's declared redaction
  policy (FR-037) and the version that produced it is recorded on the approval, so
  what an approver was shown is replayable.
- **Presenting identifiers alone is a defect, not a safe default** — it manufactures
  the rubber-stamping the gate exists to prevent.

## Downstream calls (control plane → data plane)

### `AdmitRun(v1)`
Submit an authorized, budgeted run to the data plane's queue.

```
POST /v1/runs
Request:
  { tenant_id, user_id, agent_id, agent_version,     // agent_version PINNED for the run (FR-088)
    session_key, data_label, route_model_id,         // routing decided upstream
    route_reason, region,                            // auditable + region-pinned (FR-091)
    execution_class: "interactive"|"batch", priority, // what load-shedding reads (FR-049)
    surface_id, principal_kind,                      // which channel, and what kind of caller (FR-155, FR-158)
    submitting_principal_id,                         // THIS turn's authority — not the thread's (FR-156)
    audience_ref?,                                   // shared conversation: bounds delivery + memory (FR-156)
    catalog_manifest_digest,                         // the resolvable tool universe (FR-148)
    input, budget: { per_task_usd }, autonomy_level }
Response 202:
  { session_id, status: "queued" }
Errors: 402 budget_exhausted | 429 at_capacity(Retry-After) | 403 rbac_denied
      | 409 region_conflict   // placement outside the tenant's pinned region (FR-091)
      | 403 principal_kind_not_admitted   // agent ingress on a surface not declared for it (FR-158)
      | 403 identity_unverified           // no verified Surface Identity for the submitter (FR-055, FR-156)
```

- Routing (`route_model_id`) is decided in the control plane and passed as data;
  the data plane never re-decides by model discretion. `agent_version` is pinned
  here and held for the run's life so a concurrent deploy cannot shift behavior
  or bust the prompt prefix mid-run.
- **`submitting_principal_id` is per turn, not per session** (FR-156). `SendInput`
  carries its own, and the tool boundary evaluates FR-035 scope, the Rule of Two,
  and cost attribution against *that* principal. A shared conversation has no
  standing authority — inheriting it from whoever opened the thread runs one
  participant's instructions under another's permissions.
- **`catalog_manifest_digest` is a component of `harness_digest`** (FR-148,
  FR-129): it names the tools the run *may* load, so deferred disclosure never
  moves the digest mid-run.

### `SendInput(v1)` (FR-005)
Deliver mid-run steering to a **running** session — not a new run.

```
POST /v1/runs/{session_id}/input
  { message, idempotency_key,
    submitting_principal_id }        // THIS turn's authority (FR-156)
Response 202: { queued_at_seq }
Errors: 403 identity_unverified      // no verified Surface Identity for the submitter
      | 403 not_turn_principal       // steering another participant's run without the
                                     //   channel-operator role (FR-156)
```

- Delivered to the session's steering queue, drained at a turn boundary under the
  session's serial lock, and appended as a `user_message` event (FR-085).
- **Steering into a suspended run invalidates the approval it is suspended on**
  (`reason: "steered"`, FR-106): the human has just changed the plan that approval
  authorized. A suspended run has no upcoming turn boundary to drain at, so the
  invalidation is what releases it — the agent re-requests if the action is still
  needed.

### `CancelRun(v1)` (FR-004, FR-005)
The operation that makes the `aborted` terminal reason reachable.

```
POST /v1/runs/{session_id}/cancel
  { reason, drain: bool }     // drain=true finishes the in-flight tool, then stops
Response 202: { status: "aborting" }
```

- Cancellation MUST still honor the paired-result invariant: any outstanding
  `tool_use` receives a synthetic `tool_result` before termination (FR-003).
- Cancellation MUST call `InvalidateApprovals(v1)` and reap the run's children
  **before** appending the terminal event (FR-106, FR-100). An approval resolved
  after cancellation performs nothing.
- Terminates with `aborted`, returning the best partial artifact (FR-067).

### `ResumeRun(v1)` (FR-024)
Resume a suspended or interrupted run from its last durable checkpoint.

```
POST /v1/runs/{session_id}/resume
  { from_checkpoint_id? }     // default: latest
Response 202: { status: "queued", resumed_from_seq }
```

### `StreamEvents(v1)`
Subscribe to structure-only run progress (no conversation content required).

```
GET /v1/runs/{session_id}/events            (SSE / WebSocket)
Headers: Last-Event-ID: {seq}               // or ?from_seq= — resume without gaps
Emits: { seq, schema_version, type, tool_id?, terminal_reason?, ts }
```

- A dropped subscriber MUST be able to resume from its last observed `seq`
  without loss or duplication; `seq` is monotonic per session.
- The emitted `type` values are exactly the FR-085 taxonomy — the external
  contract and the internal log MUST NOT diverge.

## Upstream calls (data plane → control plane)

### `ReserveBudget(v1)` (FR-083) — **called before every model call**
```
POST /v1/budget/reservations
  { session_id, tenant_id, turn_seq, estimated_input_tokens,
    reserved_output_tokens, model_id, price_book_version }
Response 200: { reservation_id, granted_usd, ttl_seconds }
Errors: 402 would_exceed_ceiling   // refuse BEFORE the tokens are spent
```
- Held against an **atomic** per-tenant and per-task counter, so concurrent
  sessions in one tenant cannot collectively overshoot in the window before usage
  is reported. A `402` terminates the run with `cost_exhausted`.
- The worker additionally enforces a **local hard per-run budget synchronously**,
  so enforcement never depends on this round trip completing.
- Reservations are TTL-bounded: a crashed worker's hold is released, not stranded.

### `ReportCost(v1)` (FR-016, FR-084) — reconciles a reservation
```
POST /v1/telemetry/cost
  { session_id, tenant_id, user_id, agent_id, surface, turn_seq,
    reservation_id,
    input_tokens_uncached, input_tokens_cache_read, input_tokens_cache_write,
    output_tokens, price_book_version, cost_usd, latency_ms, model_id,
    parent_session_id? }
```
- Token counts are **split by class** — the FR-014/SC-003 cache-read rate is
  derived from these measurements, not estimated.
- Actuals replace the hold; the unused remainder is released. Rolling sums are a
  reconciliation and reporting path, **not** the enforcement path.
- **This call is a shipper, not the record** (FR-124). The cost record is appended
  to the event log in the same transaction as the turn and delivered from a
  **durable outbox**: at-least-once, idempotent on
  `(session_id, turn_seq, reservation_id)`, retried with backoff, and swept for
  unshipped records. A failed or lost call delays accounting; it never loses it —
  this data feeds ceilings (FR-083), chargeback (FR-093), and the release gate's
  cost metrics (FR-018). A reservation whose reconciliation never arrives expires
  **and is reported**, never silently released as though the spend had not
  happened.
- Outbox backlog and reservation-expiry-without-reconciliation are reported
  signals (FR-095).

### `AuthorizeContentAccess(v1)` (FR-118) — the only path to plaintext

```
POST /v1/content-access/grants
  { tenant_id, scope: { session_ids[] }, requester_user_id, purpose, ttl_seconds }
Response 200: { grant_id, expires_at }   // 403 when requester == authorizer on cross-tenant scope
```

- The control plane authorizes; the **data plane enforces at the decryption
  boundary** and emits a hash-chained receipt on the grant *and on every read
  under it*, so the audit answers "who read what, when, under whose
  authorization" (FR-081, FR-118).
- Agent and service principals are refused outright. A grant authorizes *reading*
  and can never satisfy an approval (FR-036).
- Refused and expired attempts are recorded and reported as a signal (FR-095) — a
  rising refusal rate is an insider-risk indicator.
- Under `local` sink mode the grant may be authorized entirely in-boundary, so a
  BYOC tenant can operate support access without any content or metadata leaving.

### `EmitAuditReceipt(v1)` (FR-040, FR-081)
```
POST /v1/audit/receipts
  { event_id, tenant_id, user_id, tool_id,
    chain_seq, prev_digest, digest, signature, ts }
```
- Receipts are **hash-chained** per session; the sink verifies continuity and
  sequence completeness on ingest and rejects a break.
- The signature comes from a **sign-only** KMS/HSM key the data plane cannot
  read, so a compromised writer cannot forge a replacement chain.
- With `audit_sink_mode=local` the data plane keeps the chain and anchors in
  boundary and this call is not made (FR-091).

### `AnchorAuditChain(v1)` (FR-081)
```
POST /v1/audit/anchors
  { tenant_id, chain_head_digest, covers_through_seq, ts }
Response 200: { anchor_id, external_ref }
```
- Commits the chain head to an append-only external store at a configured
  interval; the scheduled verifier alerts on a break, a gap, or a missing anchor.

### `ExecuteErasure(v1)` (FR-080)
```
POST /v1/erasure
  { tenant_id, subject_ref?, key_id, requested_by, ts }
Response 200: { status: "destroyed", destroyed_at }
```
- Destroys the content-encryption key so payloads become unrecoverable. **No
  event row is deleted or rewritten** — the sequence, digests, and audit chain
  stay verifiable, and the erasure itself is recorded as an event and a receipt.

### `RequestApproval(v1)` (FR-036, FR-103–FR-109)
```
POST /v1/approvals
  { session_id, tenant_id, action_ref,
    approved_input_digest,               // WHAT the grant authorizes (FR-103)
    kind: "single"|"batch"|"plan_preauth",
    member_digests[]?,                   // enumerated set for batch/pre-auth (FR-109)
    effect_class, risk_tier,             // risk_tier resolved from ApprovalPolicy
    context_package?, redaction_policy_version, context_mode: "local"|"upstream",
    assignee_ref, escalation_chain,      // never an implicit broadcast (FR-108)
    required_approvals, separation_of_duties, step_up_required,   // FR-105
    scope, scope_tool_id, scope_effect_class, scope_expires_at,
    ttl_seconds }
Response 201: { approval_id, status: "pending", resolution_token }   // single-use
Errors: 400 missing_input_digest        // an approval that binds no arguments is refused
      | 400 unenumerated_batch          // kind != single without member_digests
      | 403 context_mode_forbidden      // upstream refused by residency config (FR-091)
      | 422 policy_violation            // scope wider than the ApprovalPolicy allows
      | 422 no_capable_channel          // no channel in the chain can render this class (FR-155)
```

- **`assignee_ref` and `escalation_chain` are capability-filtered** (FR-155):
  routing selects only surfaces whose descriptor can render the context package at
  the required size, carry the single-use resolution token, and — where
  `step_up_required` — challenge for re-authentication. `422 no_capable_channel`
  is a *runtime* backstop; the primary defense is refusing the `ApprovalPolicy` at
  configuration time, because discovering an unservable class at request time still
  ends in a fail-closed expiry (SC-060).
- **Delivery is enqueued, never inline** (FR-157): each notification, reminder, and
  escalation hop becomes a `Delivery Record` appended before it is sent and resolved
  to a typed outcome, so a permanently undelivered request is distinguishable in the
  audit record from one a human simply never answered.
- `context_package` is present only when `context_mode = upstream`; under `local`
  the data plane holds it and serves the approval UI in-boundary (FR-104).
- `resolution_token` is single-use, bound to `approval_id` **and** the resolver's
  identity, invalid after first use and after the TTL. Only its hash is stored.
- Unanswered within `ttl_seconds` → `expired` (fail-closed) after the reminder and
  escalation stages of FR-108. The expiry denies **the action**, returned to the
  loop as a typed synthetic `tool_result`; the run terminates `approval_expired`
  only if it cannot proceed without it (FR-036, FR-067).

### `ResolveApproval(v1)` (FR-105, FR-107, FR-112)
The decision path. Authenticated and **authorized** in its own right — a provider
signature on an inbound channel authenticates the transport, never the decision.

```
POST /v1/approvals/{approval_id}/resolve
  { decision: "grant"|"grant_modified"|"deny",
    resolution_token,                    // single-use, bound to approval + resolver
    modified_input?,                     // grant_modified: becomes AUTHORITATIVE (FR-107)
    resolution_note?,                    // deny rationale, returned to the loop
    step_up_assertion? }                 // fresh re-auth evidence when required
Response 200: { status, approved_input_digest, receipt_id }   // recomputed on modify
Errors: 401 step_up_required        | 403 not_authorized      // lacks approve:<class>
      | 403 principal_not_human     // agent/service principals never resolve (FR-105)
      | 409 separation_of_duties    // resolver == run initiator on an irreversible class
      | 409 already_resolved        // no transition out of a resolved state
      | 410 invalidated             // run cancelled/steered/reaped (FR-106)
      | 410 token_replayed          | 410 expired
```
- Every refused attempt appends `approval_resolution_refused` and is audited — a
  failed authorization on the approval channel is a security signal (FR-095).
- `grant_modified` recomputes `approved_input_digest` over the approver's input and
  records the modification; the agent is not told it executed unmodified.
- A `deny` without a rationale is accepted but flagged: a denial with no gradient
  turns the gate into a retry loop against a human (FR-107).
- Every grant/modify/deny emits a chained **authorization receipt** (FR-112).

### `InvalidateApprovals(v1)` (FR-106)
Called by the data plane on cancel, terminal, reap, ceiling breach, and on steering
that arrives while a run is suspended on an approval.

```
POST /v1/approvals/invalidate
  { session_id, reason: "run_cancelled"|"run_terminal"|"reaped"|"steered"
                      |"ceiling_exhausted" }
Response 200: { invalidated: [approval_id], input_requests_invalidated: [id] }
```
- MUST complete **before** the run's terminal event is appended. A pending approval
  MUST NOT outlive the run state it was requested against.
- Each invalidation releases a paired synthetic `tool_result` for its gated
  `tool_use` (FR-003), so the transcript stays valid.

### `RequestInput(v1)` / `ResolveInput(v1)` (FR-110)
The agent→human **pull** channel. Same durable-suspend machinery as an approval;
**no authorization value whatsoever** — it can never satisfy FR-036.

```
POST /v1/input-requests
  { session_id, tenant_id, pair_ref, question, answer_schema, assignee_ref,
    on_expiry: "assume_default"|"terminate", default_answer?, ttl_seconds }
Response 201: { input_request_id, status: "pending" }
Errors: 400 default_required        // assume_default without a default_answer

POST /v1/input-requests/{id}/resolve
  { answer }                           // validated against answer_schema
Response 200: { status: "answered" }
Errors: 422 schema_violation
```
- `on_expiry = assume_default` resolves with the **recorded** assumption and the run
  continues; `terminate` ends the run `input_expired` with its partial artifact.
- The answer is untrusted content and passes the input guard (FR-069).

## Versioning rules

- Additive fields are backward-compatible within `v1`.
- Breaking changes bump to `/v2` and both planes negotiate the highest common
  version at handshake, enabling rainbow rollout.
- Event `schema_version` is carried per event and versioned independently of the
  transport contract; an upcasting path keeps historical events replayable
  (FR-086).
- Database schema change follows **expand/contract** (FR-094) so a rolling deploy
  can run an older data plane and a newer control plane against one schema.
- The same build serves all four topologies; only which plane runs where changes
  by configuration (FR-050).
