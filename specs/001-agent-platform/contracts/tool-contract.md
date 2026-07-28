# Contract: Tool + Execution Pipeline

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

Every tool routes through **one** execution pipeline that performs validation,
permission checks, execution, result budgeting, and telemetry (FR-007). Tools
self-register at import time (no hand-maintained lists) and are governed by three
gates (FR-011). Safety is judged per invocation on parsed input (FR-009).

---

## Registration

- Tools self-register at import time into the registry.
- Built-in tools ship first-party: workspace-restricted filesystem
  (`file_read`/`file_write`/`file_search`/…), a sandboxed shell/code-execution tool
  (hard CPU/memory/PID/wall-clock limits, network default-deny — FR-059),
  and web search/fetch (egress-allowlisted, untrusted results; crawl4ai backend
  returning clean chunked markdown) — governed by the
  three gates below and per-invocation safety (FR-056–FR-059).
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
Gate 2 — Capability metadata : read-only vs mutating ; concurrency class ;
                               taint declaration {returns_untrusted,
                               reads_private_data, mutates_external} ; effect class
Gate 3 — Per-invocation check: safety classifier on PARSED input (fail closed)
```

- Any gate denies → the invocation is refused; default is deny.
- **Taint declarations are mandatory metadata** (FR-087). They are the inputs the
  Rule-of-Two evaluator combines with the session's accumulated taint state; a
  tool whose declaration is missing or unclassifiable is treated as engaging all
  three legs and therefore requires approval.

## The single execution pipeline (`checkPermissionsAndCallTool`)

Ordered steps applied to every call (FR-007, FR-010):

1. **Lookup** (alias map → canonical tool)
2. **Abort check** (cancellation / stop hook)
3. **Schema validation** (`inputSchema`) — instructive error on failure
4. **Semantic validation** (`validateInput`)
5. **Speculative permission classifier** (parsed input; Rule of Two evaluated from
   the tool's declared taint legs plus the session's accumulated taint state,
   FR-033, FR-087)
6. **Input backfill** (defaults, absolute-path coercion — poka-yoke, FR-007)
7. **PreToolUse hooks**
8. **Permission resolution chain** (profile → capability → per-invocation)
8a. **Idempotency key derivation + durable dedup** for state-changing effects, so
   a retry, redelivery, or resume executes the external effect exactly once
   (FR-071)
9. **Secret injection** at execution time from vault (model saw only a handle, FR-034)
10. **Execute** in the per-tenant sandbox — default E2B backend, hard resource
    limits (CPU/memory/PID/wall-clock; breach → terminate + reclaim) and network
    default-deny (egress only via the domain allowlist, FR-037, FR-059)
11. **Result budgeting** — cap/paginate (~25K tokens); spill oversized output to
    object storage, return a preview + "do not infer success from the preview"
    banner (FR-010)
12. **PostToolUse hooks**
13. **Emit audit receipt** for mutating actions — hash-chained to its predecessor
    and signed by a sign-only KMS/HSM key, over **digests** rather than plaintext
    so a lawful redaction cannot break verification (FR-040, FR-081)
14. **Append `tool_result`** to the event log (paired with `tool_use`, FR-003),
    payload envelope-encrypted under the tenant's content key (FR-089)
15. **Record taint transition** — the tool's declared legs are folded into the
    session's taint state as an event (FR-087)
16. **Error classification + telemetry** (typed failure class, per-turn cost span
    split by token class: uncached / cache-read / cache-write / output, FR-016)

## Invariants

- **Paired result**: every `tool_use` yields a `tool_result` before the next model
  call; on cancel/error a **synthetic** result is recorded (FR-003).
- **Submission-order results**: concurrent batches yield results in submission
  order, not completion order.
- **High-impact gate**: payments, deletions, external sends, and production changes
  require scoped human approval before execute (step 10 blocks pending approval,
  FR-036). While pending, the run **suspends durably at zero token cost**. An
  unanswered approval expires as a denial of *the action*, returned to the loop as
  a typed synthetic `tool_result` so the agent may replan; the run ends
  `approval_expired` only if it cannot proceed without it. Approval scopes name a
  tool and effect class and carry an expiry — none permanently ungates a class.
- **Untrusted output**: tool output and retrieved content are never fed straight
  into execution (FR-033).
- **Exactly-once effects**: a state-changing call re-issued by a retry,
  at-least-once redelivery, or resume-from-checkpoint executes its external effect
  once, deduplicated on a durable tenant-scoped idempotency key (FR-071).

## Example tool descriptor

```json
{
  "name": "asana_search",
  "description": "Search Asana tasks by query; returns high-signal fields only.",
  "inputSchema": { "type": "object", "properties": { "query": {"type":"string"} }, "required": ["query"] },
  "capability": "read_only",
  "concurrency": "concurrency_safe",
  "taint": {
    "returns_untrusted": true,
    "reads_private_data": true,
    "mutates_external": false
  },
  "effect_class": null,
  "response_format": ["concise", "detailed"]
}
```

A mutating counterpart declares its effect class and idempotency derivation, which
is what an approval scope binds to and what dedup keys off:

```json
{
  "name": "gmail_send",
  "capability": "mutating",
  "concurrency": "exclusive",
  "taint": {
    "returns_untrusted": false,
    "reads_private_data": true,
    "mutates_external": true
  },
  "effect_class": "external_send",
  "idempotency_key_spec": { "fields": ["to", "subject", "body_digest"], "scope": "tenant" }
}
```
