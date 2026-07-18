# Contract: Tool + Execution Pipeline

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

Every tool routes through **one** execution pipeline that performs validation,
permission checks, execution, result budgeting, and telemetry (FR-007). Tools
self-register at import time (no hand-maintained lists) and are governed by three
gates (FR-011). Safety is judged per invocation on parsed input (FR-009).

---

## Registration

- Tools self-register at import time into the registry.
- Cache-aware ordering: `sort(builtins) ++ sort(mcpTools)` so the tool-schema
  catalog in the prompt prefix stays byte-stable (Constitution III).
- External connectors register only through the vetted, per-tenant, RBAC-scoped
  MCP catalog (FR-012).
- Personal connectors (Gmail/Drive/Calendar) are catalog entries with
  `auth_kind = per_user_oauth`: the calling user authorizes them via per-user OAuth,
  and step 9 injects that user's vaulted token by `(tenant, user, connector)` at
  execution time (model sees only a handle) (FR-052, FR-053, FR-054).

## The three gates (fail-closed)

```
Gate 1 — Global profile      : read_only | coding | messaging | full
Gate 2 — Capability metadata : read-only vs mutating ; concurrency-safe?
Gate 3 — Per-invocation check: safety classifier on PARSED input (fail closed)
```

- Any gate denies → the invocation is refused; default is deny.

## The single execution pipeline (`checkPermissionsAndCallTool`)

Ordered steps applied to every call (FR-007, FR-010):

1. **Lookup** (alias map → canonical tool)
2. **Abort check** (cancellation / stop hook)
3. **Schema validation** (`inputSchema`) — instructive error on failure
4. **Semantic validation** (`validateInput`)
5. **Speculative permission classifier** (parsed input; Rule of Two check, FR-033)
6. **Input backfill** (defaults, absolute-path coercion — poka-yoke, FR-007)
7. **PreToolUse hooks**
8. **Permission resolution chain** (profile → capability → per-invocation)
9. **Secret injection** at execution time from vault (model saw only a handle, FR-034)
10. **Execute** in the per-tenant sandbox (egress allowlisted, FR-037)
11. **Result budgeting** — cap/paginate (~25K tokens); spill oversized output to
    object storage, return a preview + "do not infer success from the preview"
    banner (FR-010)
12. **PostToolUse hooks**
13. **Emit audit receipt** for mutating actions (HMAC, FR-040)
14. **Append `tool_result`** to the event log (paired with `tool_use`, FR-003)
15. **Error classification + telemetry** (typed failure class, per-turn cost span)

## Invariants

- **Paired result**: every `tool_use` yields a `tool_result` before the next model
  call; on cancel/error a **synthetic** result is recorded (FR-003).
- **Submission-order results**: concurrent batches yield results in submission
  order, not completion order.
- **High-impact gate**: payments, deletions, external sends, and production changes
  require scoped human approval before execute (step 10 blocks pending approval,
  FR-036); an unanswered approval expires as denial → run ends `approval_expired`.
- **Untrusted output**: tool output and retrieved content are never fed straight
  into execution (FR-033).

## Example tool descriptor

```json
{
  "name": "asana_search",
  "description": "Search Asana tasks by query; returns high-signal fields only.",
  "inputSchema": { "type": "object", "properties": { "query": {"type":"string"} }, "required": ["query"] },
  "capability": "read_only",
  "concurrency_safe": true,
  "response_format": ["concise", "detailed"]
}
```
