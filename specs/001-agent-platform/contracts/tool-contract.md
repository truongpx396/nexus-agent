# Contract: Tool + Execution Pipeline

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

Every tool routes through **one** execution pipeline that performs validation,
permission checks, execution, result budgeting, and telemetry (FR-007). Tools
self-register at import time (no hand-maintained lists) and are governed by three
gates (FR-011). Safety is judged per invocation on parsed input (FR-009).

---

## Identity (FR-147)

A tool is `{namespace}/{name}@{version}`, and that string — never a bare name — is
what an invocation resolves, an approval scope binds (`scope_tool_id`), an audit
receipt records, and the harness digest pins.

- **One owner per namespace per tenant**: a namespace belongs to exactly one source
  (the built-in set, one connector, or one MCP server). A descriptor claiming a name
  inside a namespace it does not own is **refused at admission**.
- **Collision is never resolved by order**: not last-writer-wins, not
  first-registered-wins, not silent shadowing. A hostile source shadowing a
  legitimate tool re-points every prior approval scope and receipt that named it,
  and any policy keyed on registration order is decided by connection timing.
- **The alias map at step 1 is tenant configuration** under governance sign-off
  (FR-096) and is **never** populated from descriptor contents. An alias a catalog
  source can write is a rename primitive handed to the party the catalog exists to
  distrust.
- **Version is a pinned dependency** (FR-078): a bump is an eval-gated, revertible
  deploy, re-scanned under FR-113, and it changes the harness digest.

## Disclosure (FR-062, FR-148)

The catalog splits into a **resident** tier materialized into the byte-stable
prefix and a **deferred** tier advertised as name + description until loaded.

- **The digest pins the resolvable universe, not the materialized set.** The run's
  `harness_digest` covers a `Catalog Manifest` — the sorted `(tool_id,
  descriptor_digest)` set the run *may* load. Loading a schema mid-run appends it
  to the **volatile** zone and emits a `tool_loaded` event; `harness_digest` does
  not move, so FR-088 is satisfied and FR-013's prefix stays byte-stable.
- **The selector is behaviour-bearing config**, gated and measured exactly like the
  stuck heuristic (FR-115) and the condenser (FR-130): a declared engagement
  threshold, a resident token budget, a per-run load ceiling, its own case set
  (FR-143), and reported tool-selection accuracy / wrong-tool / no-tool-found rates
  (FR-095). An unmeasured selector fails as capability loss, not as an error.

## Registration

- Tools self-register at import time into the registry.
- Built-in tools ship first-party: workspace-restricted filesystem
  (`file_read`/`file_write`/`file_search`/…), a sandboxed shell/code-execution tool
  (hard CPU/memory/PID/wall-clock limits, network default-deny — FR-059),
  web search/fetch (egress-allowlisted, untrusted results; crawl4ai backend
  returning clean chunked markdown), and **document conversion** (PDF/DOCX/PPTX/
  XLSX/CSV → the same chunked markdown, FR-160) — governed by the
  three gates below and per-invocation safety (FR-056–FR-059, FR-160).
- **Extraction and conversion backends run inside the sandbox**, never in the
  runtime worker: a headless-browser extractor renders attacker-supplied HTML and
  script, and PDF/Office parsers are a first-tier memory-safety surface fed bytes
  the attacker chose. Their output is `returns_untrusted_content` — conversion is
  decoding, not sanitization — and where conversion invokes a model (image
  description, transcription, OCR) that call goes through the `Provider` port so
  it is routed by data label, reserved pre-spend, and metered like any other
  (FR-160, FR-047, FR-087, FR-027).
- Cache-aware ordering: `sort(builtins) ++ sort(mcpTools)` over the **resident**
  tier so the tool-schema catalog in the prompt prefix stays byte-stable
  (Constitution III). Deferred tools contribute name + description only, and a
  loaded schema lands in the volatile zone — never re-ordering the prefix (FR-148).
- External connectors register only through the vetted, per-tenant, RBAC-scoped
  MCP catalog (FR-012).
- **Catalog admission scan (FR-113)**: before any descriptor (built-in, connector,
  or MCP) is added to the registry — or re-admitted on a version bump — its name,
  description, parameter docs, and schema are scanned for injected instructions,
  the same posture FR-075 requires of ingested documents. `catalog_scan_status`
  moves `pending → clean` (enumerable to the model) or `pending → flagged/rejected`
  (never enumerable, fail closed); the result and the scan policy version are
  recorded against the tool's FR-096 governance sign-off. A tool description is
  read by the model exactly like retrieved content — vetting the server's
  provenance (FR-078) and treating its runtime output as untrusted (FR-070) does
  not, by itself, catch an attacker who plants the payload in the listing itself.
- Personal connectors (Gmail/Drive/Calendar) are catalog entries with
  `auth_kind = per_user_oauth`: the calling user authorizes them via per-user OAuth,
  and step 9 injects that user's vaulted token by `(tenant, user, connector)` at
  execution time (model sees only a handle) (FR-052, FR-053, FR-054).
- **Audience-bound tokens (FR-114)**: every token minted for a connector or MCP
  server — `tenant_service` (`Connector.token_audience`) or `per_user_oauth`
  (`Connector Authorization.resource_audience`) — MUST be restricted to that one
  connector/server (RFC 8707 resource indicator or the provider's narrowest
  equivalent scope). A connector whose provider cannot support this is rejected at
  registration rather than issued a tenant-wide credential as a fallback, because a
  vaulted-but-unrestricted token still lets a compromised connector replay it
  against a different upstream resource once injected at step 9.

## The three gates (fail-closed)

```
Gate 1 — Global profile      : read_only | coding | messaging | full
Gate 2 — Capability metadata : read-only vs mutating ; concurrency class ;
                               taint declaration {returns_untrusted,
                               reads_private_data, mutates_external} ; effect class
Gate 3 — Per-invocation check: safety classifier on PARSED input (fail closed)
```

**Gate 3 is a hybrid, layered classifier, not one mechanism (FR-116)**: a fast
deterministic rule pass (allow/deny/blocklist over the parsed input) runs first
in-process and resolves the common case with no external call; only input the
rule pass cannot classify falls through to a model-based classifier. That
model-based leg carries its own bounded timeout and fails closed to `ASK` — never
`ALLOW` — on timeout, error, or an unparseable verdict. Rules alone under-block
novel or obfuscated attacks; a model call on every invocation would put a
non-deterministic, metered round-trip on the hot path that the cache-read and
cost goals (FR-014, FR-017) cannot absorb — the hybrid is required.

- Any gate denies → the invocation is refused; default is deny.
- **Taint declarations are mandatory metadata** (FR-087). They are the inputs the
  Rule-of-Two evaluator combines with the session's accumulated taint state; a
  tool whose declaration is missing or unclassifiable is treated as engaging all
  three legs and therefore requires approval.

## The permission resolution order (FR-111) — one published total order

An undefined interaction between two gates is a fail-open waiting to be found, so
the chain is total and ordered. Every invocation walks it top to bottom:

| # | Layer | May resolve to | Notes |
|---|-------|----------------|-------|
| 1 | **Deny rules** (tenant/tool/pattern) | `DENY` (final) | Evaluated first; nothing below may overturn a deny |
| 2 | `PreToolUse` hooks | `DENY` (final) · `ASK` · `DEFER` | A hook may *tighten* or force a prompt; a hook `ALLOW` is a defer, never a bypass |
| 3 | **Autonomy level** (pinned, ratcheting) | `DENY` · `ASK` · `DEFER` | `read_only` denies every mutating capability; `supervised` forces `ASK` on every mutating invocation; `full` defers |
| 4 | Gate 1 — global profile | `DENY` · `DEFER` | |
| 5 | Gate 2 — capability metadata | `DENY` · `ASK` · `DEFER` | Effect class routes to the approval policy |
| 6 | Gate 3 — **per-invocation safety** on parsed input | `DENY` · `ASK` · `DEFER` | **Always evaluated. No exception, ever** |
| 7 | **Rule of Two** (session taint + declared legs) | `ASK` · `DEFER` | **Always evaluated. No exception, ever** |
| 8 | Approval policy (FR-109) | `AUTO` · `ASK(once/session/multi_party)` | Deterministic; recorded on the approval |
| 9 | Standing scope / batch / plan pre-authorization | `SATISFIES an ASK` | May only *answer* an ask raised above; may never suppress one |
| 10 | Otherwise | `ALLOW` | |

Two invariants make this order load-bearing rather than decorative:

- **A deny at any layer is final.** No later layer, autonomy setting, standing
  scope, batch, plan pre-authorization, or operator mode may overturn it. There is
  no `bypass` mode in this platform.
- **Steps 6 and 7 are unconditional.** A standing scope, batch, plan
  pre-authorization, or `full` autonomy may satisfy an *ask* — it may never cause
  the per-invocation safety check (FR-009) or the Rule-of-Two evaluation
  (FR-033/FR-087) to be skipped. A remembered "yes" is an answer to a question,
  not permission to stop asking it.
- **Autonomy ratchets** (FR-111): pinned at run start, tightenable mid-run by an
  operator, and widenable by nothing — not model output, not a tool result, not a
  steering message, not a hook, not a delegation parameter, **not a skill**.

**Skills sit below this table, not inside it** (FR-153). An activated skill's
declared tool set is applied as an **intersection** over the catalog resolved at
run start — a pre-filter that can only remove candidates before the chain runs. It
is not a layer, because a layer could resolve to `ALLOW`: a skill declaring a
capability the run does not hold has that declaration ignored and recorded
(`skill_capability_ignored`), and no skill may touch autonomy, approval policy,
egress, data label, or region. Ecosystem skill formats carry tool-permission
declarations that *grant*; adopting that semantics here would make "load this
skill" a widening primitive reachable from injected content — the same lever the
autonomy ratchet closes, arriving through a different door.

## Callers of the pipeline (FR-149)

The pipeline is model-facing by construction, which makes any capability reachable
*without* it a complete bypass of every control in this document. There are exactly
two callers, and they are indistinguishable downstream of step 1:

| Caller | Origin | Notes |
|---|---|---|
| The loop | A `tool_use` chunk from `Provider.stream` | The ordinary path |
| The **in-sandbox broker** | Agent-written code calling a platform capability (the "code execution" / programmatic tool-calling pattern) | Authenticated to the run's identity; the calling program **blocks** on the same durable suspension an approval raises, with the sandbox handle carried on the checkpoint (FR-126) |

- **There is no third caller.** A direct network path from sandbox code to a
  connector or MCP endpoint is a **prohibited egress route** under FR-037/FR-059,
  not a permitted shortcut — the token savings the pattern offers are not a reason
  to accept a path with no permission chain, no safety check, no approval, no
  claim, no receipt, and no taint transition.
- **Brokered calls are indistinguishable in the record**: same events, same
  receipts, same taint folding, same result budgeting (FR-010) per call — even
  though only a final value returns to the model.

## The single execution pipeline (`checkPermissionsAndCallTool`)

Ordered steps applied to every call (FR-007, FR-010):

1. **Lookup** — resolve `{namespace}/{name}@{version}` through the
   governance-signed alias map (FR-147); a deferred tool not yet loaded resolves
   only after its `tool_loaded` event (FR-148)
1a. **Descriptor re-verification** — compare the source's current descriptor digest
   against the one admitted under FR-113. A mismatch fails closed as an
   **unadmitted descriptor** rather than executing, because a protocol-level
   listing cache lets a server change what it advertises after the scan (FR-150)
2. **Abort check** (cancellation / stop hook)
3. **Schema validation** (`inputSchema`) — instructive error on failure
4. **Semantic validation** (`validateInput`)
5. **Speculative permission classifier** (parsed input via the hybrid rule/model
   Gate 3 of FR-116; Rule of Two evaluated from the tool's declared taint legs
   plus the session's accumulated taint state, FR-033, FR-087)
6. **Input backfill** (defaults, absolute-path coercion — poka-yoke, FR-007)
7. **PreToolUse hooks**
8. **Permission resolution chain** — the total order above (FR-111)
8a. **Canonical digest + idempotency key derivation** over `tool_id` + the fully
   resolved input. **One artifact** serves three jobs: it is what an approval
   binds (FR-103), what durable dedup keys off so a retry, redelivery, or resume
   executes the external effect exactly once (FR-071), and what step 10 re-verifies
8b. **Approval gate** when step 8 resolved to `ASK` — request approval carrying the
   digest, the context package, the assignee, and the TTL (FR-036, FR-104, FR-108);
   the run **suspends durably at zero token cost** here. A standing scope, batch, or
   plan pre-authorization may satisfy the ask only by matching an enumerated,
   unexpired, digest-bound entry; it never skips steps 6–7
9. **Secret injection** at execution time from vault (model saw only a handle, FR-034)
9a. **Digest re-verification** — recompute the canonical digest and compare it to
   the approved one. Divergence → refuse with a typed `approval_mismatch` synthetic
   result; never silently re-request approval within the same turn (FR-103)
9b. **Write-ahead idempotency claim** for state-changing invocations — commit the
   claim durably in `in_flight` state, keyed on the step-8a digest, **before** the
   effect leaves the process (FR-127). An existing `completed` claim short-circuits
   to its recorded result rather than executing; an existing `in_flight` claim is a
   crash-recovery case, not a retry, and is resolved by step 10a
10. **Execute** in the per-tenant sandbox — default E2B backend, hard resource
    limits (CPU/memory/PID/wall-clock; breach → terminate + reclaim) and network
    default-deny (egress only via the domain allowlist, FR-037, FR-059)
10a. **Claim resolution** — move the claim to `completed` / `failed` on a recorded
    outcome. On resume, a claim still `in_flight` is resolved by querying the
    external system where the provider supports it, or by escalating to a human as
    a typed decision where it does not — **never** by re-executing and never by
    discarding it as unattempted (FR-127)
11. **Result budgeting** — cap/paginate (~25K tokens); spill oversized output to
    object storage, return a preview + "do not infer success from the preview"
    banner (FR-010)
12. **PostToolUse hooks**
12a. **Emit authorization receipt** when this invocation was approval-gated —
    binding approver identity, authn method, channel, approved digest, scope, and
    decision into the same chain (FR-112)
13. **Emit audit receipt** for mutating actions — hash-chained to its predecessor
    and signed by a sign-only KMS/HSM key, over **digests** rather than plaintext
    so a lawful redaction cannot break verification (FR-040, FR-081)
14. **Append `tool_result`** to the event log (paired with `tool_use`, FR-003),
    payload envelope-encrypted under the tenant's content key (FR-089)
15. **Record taint transition** — the tool's declared legs are folded into the
    session's taint state as an event (FR-087)
16. **Error classification + telemetry** (typed failure class, per-turn cost span
    split by token class: uncached / cache-read / cache-write / output, FR-016).
    The span carries structure only — tool id, effect class, typed outcome, sizes,
    durations, digests — and the events it covers carry the reciprocal
    `trace_id`/`span_id` (FR-117, FR-119). Tool arguments and results never appear
    on a span, and the cost record is appended in this transaction and shipped
    through the outbox (FR-124)

## Invariants

- **Paired result**: every `tool_use` yields a `tool_result` before the next model
  call; on cancel/error a **synthetic** result is recorded (FR-003).
- **Submission-order results**: concurrent batches yield results in submission
  order, not completion order.
- **High-impact gate**: payments, deletions, external sends, and production changes
  require scoped human approval before execute (step 8b blocks pending approval,
  FR-036). While pending, the run **suspends durably at zero token cost**. An
  unanswered approval expires as a denial of *the action* — after notification,
  reminder, and escalation (FR-108) — returned to the loop as a typed synthetic
  `tool_result` carrying the approver's rationale where present (FR-107), so the
  agent may replan; the run ends `approval_expired` only if it cannot proceed
  without it. Approval scopes name a tool and effect class and carry an expiry —
  none permanently ungates a class.
- **An approval authorizes a digest, not an intent** (FR-103): step 9a re-verifies
  the canonical digest of the resolved input against the approved one and refuses
  on divergence with a typed `approval_mismatch`. The `tool_use` identifier alone
  is never sufficient — it survives a change of arguments, which is precisely the
  substitution the gate exists to catch.
- **One artifact, three jobs**: the digest derived at step 8a is what the approval
  binds, what dedup keys off, and what step 9a re-verifies — so the approved
  invocation, the executed invocation, and the de-duplicated invocation are
  provably one invocation.
- **The claim precedes the effect** (FR-127): the dedup record is committed
  `in_flight` at step 9b, before execution, and closed at step 10a. A record
  written only on success protects against a *retried call* but not a *crash
  mid-call* — the case a checkpoint cannot see, because a checkpoint records where
  the run was, never whether the payment was taken. A resume resolves `in_flight`
  by probe or by human decision; re-execution is never a resolution.
- **Only an authorized human resolves** (FR-105): agent and service principals
  never grant; irreversible classes require a resolver distinct from the run's
  initiator and, where the tenant's policy says so, fresh step-up authentication;
  the resolution channel carries a single-use token bound to the approval and the
  resolver. Refused resolutions are audited.
- **No approval outlives its run** (FR-106): cancel, terminal, reap, ceiling breach,
  and steering-into-suspension invalidate outstanding approvals before the run
  terminates, each releasing a paired synthetic `tool_result`.
- **The gate is pipeline-enforced, never prompt-enforced**: no model-facing
  parameter grants, widens, skips, or pre-satisfies an approval, and no standing
  scope, batch, or plan pre-authorization suppresses the per-invocation safety
  check or the Rule of Two (FR-111). Injected content asserting that consent was
  already given changes nothing, and that suppression attempt is a held-out eval
  case (FR-112).
- **Input requests are not approvals** (FR-110): an agent-initiated,
  schema-declared question suspends the run on the same machinery but carries
  **zero** authorization and can never satisfy this gate.
- **Untrusted output**: tool output and retrieved content are never fed straight
  into execution (FR-033).
- **Exactly-once effects**: a state-changing call re-issued by a retry,
  at-least-once redelivery, or resume-from-checkpoint executes its external effect
  once, deduplicated on a durable tenant-scoped idempotency key (FR-071) derived
  from the same canonical digest the approval bound.
- **No path around the pipeline** (FR-149): the loop and the in-sandbox broker are
  the only callers. A capability reachable from a shell the agent controls without
  traversing steps 1–16 is not a performance optimization, it is the absence of the
  control surface — so the broker exists or the path does not.
- **Names are not identity** (FR-147): resolution, approval scopes, receipts, and
  the harness digest bind `{namespace}/{name}@{version}`. A namespace has one owner
  per tenant and a colliding descriptor is refused at admission, never resolved by
  registration order.
- **An admitted descriptor stays the admitted descriptor** (FR-150): step 1a
  re-verifies the descriptor digest, so a listing cache or a server-side refresh
  cannot widen what the model was told a tool does after the scan cleared it.
- **A skill's script is a tool** (FR-151): executable content shipped in a skill
  bundle enters through this same registration path and these same gates, or the
  bundle is refused. There is no execution path for code that arrived as an
  attachment to a document.

## Example tool descriptor

```json
{
  "id": "asana/search@1.2.0",
  "description": "Search Asana tasks by query; returns high-signal fields only.",
  "source_ref": "connector:asana",
  "disclosure": "deferred",
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
  "id": "gmail/send@2.0.1",
  "source_ref": "connector:gmail",
  "disclosure": "resident",
  "capability": "mutating",
  "concurrency": "exclusive",
  "taint": {
    "returns_untrusted": false,
    "reads_private_data": true,
    "mutates_external": true
  },
  "effect_class": "external_send",
  "idempotency_key_spec": { "fields": ["to", "subject", "body_digest"], "scope": "tenant" },
  "approval_binding": {
    "digest_fields": ["to", "cc", "bcc", "subject", "body_digest", "attachment_digests"],
    "blast_radius_fields": ["to", "cc", "bcc", "attachment_digests"]
  }
}
```

`approval_binding` is what makes the gate a transaction rather than a flag:

- **`digest_fields`** are canonicalized into the digest an approval binds and step 9a
  re-verifies (FR-103). They MUST be a superset of `idempotency_key_spec.fields`, so
  no field can change the external effect without invalidating the consent that
  authorized it — a mutating tool whose `digest_fields` omit a
  blast-radius-determining argument fails registration.
- **`blast_radius_fields`** are the arguments an approver is always shown in the
  context package (FR-104) — the recipient, the amount, the target resource, the
  record count. A human approving `gmail_send` is approving *who receives it*.
