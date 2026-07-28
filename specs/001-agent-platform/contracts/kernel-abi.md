# Contract: Kernel ABI (swappable interfaces)

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

The kernel exposes a small set of trait/interface seams, each with ≥1 swappable
implementation (Constitution I, VII). Signatures are language-neutral pseudocode;
the Go implementation lives in `backend-go/kernel` and `backend-go/internal/*`.

---

## `Provider` — model access (FR-027)

One abstraction; native tool-calling only; every backend normalized to one stream
contract.

```
interface Provider {
  // Streams normalized chunks; MUST NOT leak vendor JSON into the loop.
  stream(prompt: Prompt, tools: ToolSchema[], ctx: RunContext) -> Stream<Chunk>
}

type Chunk =
  | { kind: "content",   text: string }
  | { kind: "reasoning", opaque: bytes }              // round-tripped, never shown (FR-064)
  | { kind: "tool_use",  id: string, tool: string, input: json }
  | { kind: "usage",     input_uncached: int, input_cache_read: int,
                         input_cache_write: int, output_tokens: int }   // FR-016
  | { kind: "done",      reason: "stop" | "max_output" | "error" }
```

- **Rule**: routing decides which `Provider`/model by data label + difficulty,
  deterministically and auditably (never model discretion).
- **Failover**: caller layers retry → cooldown → failover across implementations.
- **Usage is split by token class** — an undifferentiated `input_tokens` total
  makes the >90% cache-read gate (FR-014, SC-003) unmeasurable.
- **A deterministic implementation is mandatory** (FR-097): a recorded/fake
  provider satisfying this same contract — including its truncation, stall,
  malformed-stream, and failover paths — backs the correctness suite so tests are
  reproducible and do not bill a live provider.

## `Tool` — self-describing capability (FR-007, FR-008, FR-009, FR-011)

```
interface Tool {
  name: string                                  // namespaced, e.g. "asana_search"
  description: string                           // progressive-disclosure summary
  inputSchema: JSONSchema
  isConcurrencySafe(input): bool                // PER INVOCATION, default false
  taint: TaintDeclaration                       // REQUIRED — inputs to Rule of Two
  effectClass: "payment" | "delete" | "external_send"
             | "prod_change" | "other" | null   // what an approval scope may cover
  checkPermissions(input, ctx): PermissionResult
  validateInput(input, ctx): ValidationResult
  call(input, ctx): ToolResult
}

type TaintDeclaration = {                       // FR-087; every field defaults TRUE
  returns_untrusted: bool                       // leg A: output is untrusted content
  reads_private_data: bool                      // leg B: touches tenant/private data
  mutates_external: bool                        // leg C: changes state / communicates out
}
```

- **Fail-closed defaults**: serial unless proven safe, assume writes, permission
  denied unless explicitly granted, **all three taint legs assumed engaged** when
  the declaration is missing or unclassifiable.
- **Safety is per invocation on parsed input** (`Bash("ls")` ≠ `Bash("rm -rf")`).
- **The Rule of Two reads declarations, not guesses** — the evaluator combines a
  session's accumulated `TaintState` with the pending invocation's declaration.
  Taint is reduced only through an audited sanitization boundary (a summarizing
  sub-agent firewall or an operator-scoped re-baseline), and every transition is
  appended to the event log.

## `Delegation` — the sub-agent seam (FR-079, FR-098–FR-101)

A sub-agent is a second locus of execution holding real credentials and spending
real money, so delegation is a **tool invocation through the same pipeline**
(`tool-contract.md`), not a side channel around it. It therefore inherits the
paired-result invariant, the permission chain, and the audit receipt for free.

```
interface Delegation {
  // Scope MUST be provably a subset of the parent's at call time (FR-098).
  delegate(spec: DelegationSpec, ctx: RunContext) -> DelegationResult
  // Reaps children on parent terminal/cancel/ceiling breach (FR-100).
  reap(parent_session_id, reason: ReapReason): void
}

type DelegationSpec = {
  goal: string                       // the child's whole instruction
  context: string                    // relevant trace, not just messages (FR-079)
  scope: ScopeRef                    // subset selector over the PARENT's scope
  return_schema: JSONSchema          // validated on return, not trusted
  acceptance: AcceptanceCriterion    // checked before folding in (FR-044, FR-100)
  max_summary_tokens: int            // default ~1-2k / ~8 KB; platform truncates
  max_iterations: int                // per-child loop bound
  ceiling_usd: numeric               // per-child draw from the parent envelope
}

type DelegationResult = {
  summary: string                    // ALWAYS untrusted content (FR-087, FR-100)
  taint_engaged: TaintDeclaration    // folded into the parent on return
  usage: Usage                       // metered to parent AND root (FR-101)
  outcome: "accepted" | "rejected_schema" | "rejected_acceptance"
         | "bound_exceeded" | "child_error" | "reaped"
}

type ReapReason = "parent_terminal" | "parent_cancelled" | "ceiling_exhausted"
```

- **Descent is one-way** (FR-098): `scope` selects a **subset** of the parent's
  live scope. There is no model-facing parameter that widens tools, connectors,
  egress, data label, or region — a compromised parent cannot escalate through a
  child. Approval scopes never descend.
- **Taint ascends** (FR-087): `taint_engaged` folds into the parent's `TaintState`
  on return. A `summary` never clears the untrusted leg; only an operator-scoped
  re-baseline does.
- **Bounds fail closed** (FR-099): `depth ≤ 1`, `concurrent_children ≤ 3`,
  `children_per_run ≤ 16` by default, configured per tier (FR-077) and per tenant.
  A bound breach returns `bound_exceeded` as a **non-retryable** synthetic result —
  the classifier must not drive a retry loop against a hard cap.
- **Cost is reserved as an envelope** (FR-099, FR-083): the parent reserves the
  aggregate worst case **before the first child starts** and children draw from it.
  Per-child reservation against the tenant counter is prohibited — it lets one
  fan-out starve every sibling session in the tenant.
- **Every result is paired** (FR-003): reap, timeout, bound breach, and child error
  all produce a synthetic `tool_result` before the parent's next `Provider.stream`.
- **Chain, not parent** (FR-101): `root_session_id` + `parent_session_id` + `depth`
  ride on every session, cost record, receipt, and span in the tree.

## `Memory` — durable knowledge (FR-019)

```
interface Memory {
  // Immutable snapshot injected at session start; screened first.
  loadForSession(tenant_id, session_key): MemorySnapshot
  // Writes take effect NEXT session (never mid-session — cache stability).
  append(tenant_id, entry): void
  search(tenant_id, query): MemoryHit[]         // L1 episodic, when justified
}
```

- Scoped per tenant, retention-bounded, injection/exfiltration screened before use.

## `Workspace` / `Sandbox` — the trust boundary (FR-047)

```
interface Workspace {
  acquire(tenant_id, session_id): SandboxHandle  // from warm pool
  exec(handle, command, ctx): ExecResult         // egress-controlled, allowlisted
  release(handle): void                          // reclamation; hard TTL enforced
}
```

- Per-tenant isolation; caps enforced at acquire; reclaimed on terminal/stuck/TTL.

## `Channel` / `Surface` — thin adapter (FR-001, FR-028, FR-031)

```
interface Surface {
  // Translates external input into a run submission; NO control-flow logic.
  toRequest(external_input): RunRequest
  // Streams or polls progress; never holds a blocked connection.
  emit(event: Event): void
}
```

- A new surface is a new adapter with **zero** kernel changes.

## `RunControl` — lifecycle operations (FR-005, FR-004)

Every terminal reason must be reachable through a defined operation; a terminal
state no caller can produce is a contract defect.

```
interface RunControl {
  steer(session_id, message, idempotency_key): void   // drained at a turn boundary
  cancel(session_id, reason, drain: bool): void       // the ONLY producer of `aborted`
  resume(session_id, from_checkpoint_id?): void       // resume, never restart (FR-024)
}
```

- `steer` delivers to the **running** session's queue under its serial lock and
  appends a `user_message` event — it is not a new run submission.
- `cancel` still honors the paired-result invariant: any outstanding `tool_use`
  receives a synthetic `tool_result` before termination, and the run returns its
  best partial artifact (FR-003, FR-067).
- `cancel` and every terminal path **reap the run's children** via
  `Delegation.reap` before the parent terminates (FR-100). A child outliving its
  parent is a defect, not a leak to reconcile later.

## The loop terminal contract (FR-002, FR-004)

```
type Classification = TOOL_CALLS | CONTENT | EMPTY   // dispatch on this, not text

type TerminalReason =
  | completed | max_turns | cost_exhausted | error
  | aborted | prompt_too_long | hook_stopped | approval_expired
```

- Callers MUST handle `TerminalReason` exhaustively.
- Every `tool_use` MUST have a paired `tool_result` (synthetic on cancel/error)
  before the next `Provider.stream` call. This is a **total invariant over all
  histories** and is therefore property-tested over generated event sequences,
  not by examples alone (FR-097).
- Every terminal reason maps to a producer: `aborted` ← `RunControl.cancel`;
  `cost_exhausted` ← a refused budget reservation (FR-083); `approval_expired` ←
  an approval TTL the run could not proceed without (FR-036); the rest from the
  loop's own guards.

## Budget reservation — the pre-spend gate (FR-083)

```
interface BudgetGate {
  // Called BEFORE every Provider.stream; refusal terminates `cost_exhausted`.
  reserve(session_id, est_input_tokens, reserved_output_tokens, model_id) -> Reservation
  reconcile(reservation_id, actual_usage: Usage) -> void   // releases the remainder
}
```

- The worker additionally enforces a **local hard per-run budget synchronously**,
  so a ceiling never depends on a round trip to another plane completing.
