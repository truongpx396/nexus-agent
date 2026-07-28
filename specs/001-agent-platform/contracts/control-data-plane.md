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
| Audit sink + chain anchoring (FR-040, FR-081) | Emits audit receipts + cost records upstream |
| Erasure/key-destruction orchestration (FR-080) | Holds content keys per tenant (FR-089) |

## Data-egress boundary (FR-091)

When the data plane runs inside a customer boundary, **exactly** the following
leaves it, and nothing else:

| Leaves the boundary | Never leaves |
|---------------------|--------------|
| Identifiers (`tenant_id`, `session_id`, `user_id`, `tool_id`, `model_id`) | Prompts, model output, tool arguments, tool results |
| Counts and measures (token classes, latency, cost) | Memory content, retrieved documents, artifacts |
| Digests and signatures (audit chain) | Any plaintext an event payload contains |
| Typed reasons (terminal reason, failure class, reclaim reason) | Secrets, connector tokens (never leave the vault at all) |

- `audit_sink_mode` and `telemetry_sink_mode` are per-deployment configuration:
  `upstream` (default for SaaS) or **`local`** — with `local`, the data plane
  keeps its own audit chain and anchors, and *nothing* crosses the boundary
  (required for BYOC tenants who accept no metadata egress).
- Region pinning is enforced at admission: a run whose placement would fall
  outside the tenant's pinned region is refused, not relocated.

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
    input, budget: { per_task_usd }, autonomy_level }
Response 202:
  { session_id, status: "queued" }
Errors: 402 budget_exhausted | 429 at_capacity(Retry-After) | 403 rbac_denied
      | 409 region_conflict   // placement outside the tenant's pinned region (FR-091)
```

- Routing (`route_model_id`) is decided in the control plane and passed as data;
  the data plane never re-decides by model discretion. `agent_version` is pinned
  here and held for the run's life so a concurrent deploy cannot shift behavior
  or bust the prompt prefix mid-run.

### `SendInput(v1)` (FR-005)
Deliver mid-run steering to a **running** session — not a new run.

```
POST /v1/runs/{session_id}/input
  { message, idempotency_key }
Response 202: { queued_at_seq }
```

- Delivered to the session's steering queue, drained at a turn boundary under the
  session's serial lock, and appended as a `user_message` event (FR-085).

### `CancelRun(v1)` (FR-004, FR-005)
The operation that makes the `aborted` terminal reason reachable.

```
POST /v1/runs/{session_id}/cancel
  { reason, drain: bool }     // drain=true finishes the in-flight tool, then stops
Response 202: { status: "aborting" }
```

- Cancellation MUST still honor the paired-result invariant: any outstanding
  `tool_use` receives a synthetic `tool_result` before termination (FR-003).
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

### `RequestApproval(v1)` (FR-036)
```
POST /v1/approvals
  { session_id, tenant_id, action_ref, scope, ttl_seconds }
Response: { approval_id, status: "pending" }
```
- If unanswered within `ttl_seconds`, resolves `expired` (fail-closed); the data
  plane terminates the run with `approval_expired` and does not perform the action.

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
