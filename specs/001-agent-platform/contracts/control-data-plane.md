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
| Budget checks, cost ceilings (FR-017) | Provider/model calls (FR-027) |
| Model routing decision (FR-037) | Memory read/write (FR-019) |
| Eval / skill / MCP catalog (FR-042) | Event-log append, checkpoints (FR-024) |
| Audit sink (FR-040) | Emits audit receipts + cost records upstream |

## Downstream calls (control plane → data plane)

### `AdmitRun(v1)`
Submit an authorized, budgeted run to the data plane's queue.

```
POST /v1/runs
Request:
  { tenant_id, user_id, agent_id, agent_version,
    session_key, data_label, route_model_id,   // routing decided upstream
    input, budget: { per_task_usd }, autonomy_level }
Response 202:
  { session_id, status: "queued" }
Errors: 402 budget_exhausted | 429 at_capacity(Retry-After) | 403 rbac_denied
```

- Routing (`route_model_id`) is decided in the control plane and passed as data;
  the data plane never re-decides by model discretion.

### `StreamEvents(v1)`
Subscribe to structure-only run progress (no conversation content required).

```
GET /v1/runs/{session_id}/events            (SSE / WebSocket)
Emits: { seq, type, tool_id?, terminal_reason?, ts }   // structure, not content
```

## Upstream calls (data plane → control plane)

### `ReportCost(v1)` (FR-016)
```
POST /v1/telemetry/cost
  { session_id, tenant_id, turn_seq, input_tokens, output_tokens,
    cost_usd, latency_ms, model_id }
```
- Control plane accumulates against per-task/per-tenant budgets; on breach it
  signals the data plane to stop the run with `cost_exhausted` + alert.

### `EmitAuditReceipt(v1)` (FR-040)
```
POST /v1/audit/receipts
  { event_id, tenant_id, user_id, tool_id, hmac, ts }
```
- Immutable, tamper-evident; the control plane is the durable audit sink.

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
- The same build serves all four topologies; only which plane runs where changes
  by configuration (FR-050).
