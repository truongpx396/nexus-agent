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
  | { kind: "tool_use",  id: string, tool: string, input: json }
  | { kind: "usage",     input_tokens: int, output_tokens: int }
  | { kind: "done",      reason: "stop" | "max_output" | "error" }
```

- **Rule**: routing decides which `Provider`/model by data label + difficulty,
  deterministically and auditably (never model discretion).
- **Failover**: caller layers retry → cooldown → failover across implementations.

## `Tool` — self-describing capability (FR-007, FR-008, FR-009, FR-011)

```
interface Tool {
  name: string                                  // namespaced, e.g. "asana_search"
  description: string                           // progressive-disclosure summary
  inputSchema: JSONSchema
  isConcurrencySafe(input): bool                // PER INVOCATION, default false
  checkPermissions(input, ctx): PermissionResult
  validateInput(input, ctx): ValidationResult
  call(input, ctx): ToolResult
}
```

- **Fail-closed defaults**: serial unless proven safe, assume writes, permission
  denied unless explicitly granted.
- **Safety is per invocation on parsed input** (`Bash("ls")` ≠ `Bash("rm -rf")`).

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

## The loop terminal contract (FR-002, FR-004)

```
type Classification = TOOL_CALLS | CONTENT | EMPTY   // dispatch on this, not text

type TerminalReason =
  | completed | max_turns | cost_exhausted | error
  | aborted | prompt_too_long | hook_stopped | approval_expired
```

- Callers MUST handle `TerminalReason` exhaustively.
- Every `tool_use` MUST have a paired `tool_result` (synthetic on cancel/error)
  before the next `Provider.stream` call.
