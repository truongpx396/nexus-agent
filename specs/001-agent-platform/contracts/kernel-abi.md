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
  id: ToolRef                                   // {namespace}/{name}@{version} (FR-147)
  description: string                           // progressive-disclosure summary
  inputSchema: JSONSchema
  outputSchema: JSONSchema?                     // validated on return; still UNTRUSTED (FR-150)
  descriptorDigest: bytes                       // re-verified at step 1a (FR-150)
  disclosure: "resident" | "deferred"           // FR-062, FR-148
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
- **Identity is qualified, and a namespace has one owner** (FR-147): a colliding
  descriptor is refused at admission, never resolved by registration order. The
  alias map is governance-signed tenant config, never descriptor-supplied.
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

  // The ONLY route from sandbox code to a platform capability (FR-149).
  // Re-enters checkPermissionsAndCallTool at step 1; the caller BLOCKS.
  broker(handle, call: ToolCall, ctx): ToolResult
}
```

- Per-tenant isolation; caps enforced at acquire; reclaimed on terminal/stuck/TTL.
- **`broker` or nothing** (FR-149). Programmatic tool calling — agent-written code
  orchestrating tools so only final values enter context — is permitted only
  through this seam, and a brokered call is indistinguishable from a model-
  originated one in the log, the audit chain, and the taint state. A direct network
  route from sandbox code to a connector or MCP endpoint is a prohibited egress
  path (FR-037, FR-059), because the sandbox is the one place that could otherwise
  reach a capability with no permission chain, no approval, no claim, and no
  receipt.
- **A brokered approval suspends the program, not just the loop**: the calling
  process blocks on the same durable mechanism (FR-036) and the sandbox handle
  rides on the checkpoint (FR-126), so a six-hour approval does not depend on a
  sandbox surviving it.

## `Channel` / `Surface` — thin adapter (FR-001, FR-028, FR-031, FR-155–FR-159)

A surface translates and nothing else — but "thin" describes what it may *not* do,
not what it must be *able* to do. Fifteen requirements route human decisions through
here, so the adapter declares its capabilities and the platform routes on them.

```
interface Surface {
  capabilities(): SurfaceCapability          // declared, versioned, conformance-tested
  // Translates external input into a run submission; NO control-flow logic.
  // MUST resolve the SUBMITTING principal per message (FR-156).
  toRequest(external_input): RunRequest
  // Streams or polls progress; never holds a blocked connection.
  emit(event: Event): void
  // Outbound to a human. Enqueues through the delivery outbox; never sends inline.
  deliver(msg: OutboundMessage) -> DeliveryId          // FR-157
  // Resolves the surface's native thread identity into the session key (FR-159).
  bindConversation(external_ref): SessionKey
}

type SurfaceCapability = {
  principal_kind: "human" | "service" | "agent"   // undeclared => most restricted
  can_render_approval_context: bool               // FR-104
  max_render_bytes: int
  supports_step_up_auth: bool                     // FR-105
  supports_resolution_token: bool
  supports_structured_input: bool                 // FR-110 schema-declared answers
  supports_streaming: bool                        // FR-031
  max_message_bytes: int
  conformance_run_id: RunId                       // absent => not enablable
}

type OutboundMessage = {
  session_id, seq                       // the event this carries — APPENDED FIRST
  recipient_ref: string                 // idempotency: (session, seq, surface, recipient)
  kind: "reply" | "approval_request" | "reminder" | "escalation"
      | "input_request" | "completion"
  audience_ref: string?                 // FR-156 — suppression, not truncation
}
```

- A new surface is a new adapter with **zero** kernel changes.
- **Routing filters on capability** (FR-155). Approval and input-request delivery
  (FR-108) select from the assignee/escalation chain by what each surface can
  actually do, and an `ApprovalPolicy` naming an effect class no configured channel
  can serve is refused **at configuration time**. Without this, the chain can route
  an irreversible-class approval to a channel that can render neither the
  blast-radius fields nor a step-up challenge — producing the rubber stamp SC-025
  forbids, or a silent fail-closed expiry.
- **Authority is per turn, never per conversation** (FR-156). `toRequest` resolves
  the submitting principal from a verified `Surface Identity`; FR-035 scope,
  FR-033 Rule of Two, and FR-093 attribution are evaluated against *that* principal
  for *that* turn. `steer` and `cancel` authorize against the turn's submitter or a
  declared channel-operator role — never against presence in the conversation.
  Binding a thread to whoever opened it runs one participant's instructions under
  another's authority and makes FR-105's separation-of-duties check unevaluable.
- **`deliver` enqueues; it does not send** (FR-157). The event is appended before
  the message leaves, delivery is at-least-once and idempotent on
  `(session_id, seq, surface, recipient)`, and the outcome is typed. A
  `failed_permanent` on an approval request MUST stay distinguishable from an
  unanswered one — to a tenant they look identical and mean opposite things.
- **Output adaptation happens after the egress sanitizer** (FR-068): chunking,
  truncation, and attachment spilling reshape what was sanitized, never re-open it.
- **`principal_kind = agent` is its own admission class** (FR-158): an external
  agent is admitted only through a surface declared for it, under a provable subset
  scope (FR-098), source-tainted as untrusted (FR-087), metered to a named payer,
  and **incapable of resolving an approval or answering an input request** — the
  caller is a party to the situation, not oversight of it.

## `Skills` — the capability-narrowing seam (FR-020, FR-021, FR-151–FR-154)

A skill is procedural knowledge, not a permission grant. It is admitted as a signed
bundle and it may only *narrow* what the run can already do.

```
interface Skills {
  // Tier 1 always resident (bounded); tier 2 on activation; tier 3 per file.
  resident(tenant_id, ctx): SkillMetadata[]        // capped; relevance-selected
  activate(skill_id, version, ctx): SkillBody      // emits `skill_activated`
  reference(skill_id, path, ctx): FileContent      // tier 3, individually loaded

  // Admission is one gate for EVERY origin (FR-152).
  admit(bundle: SkillBundle, origin: SkillOrigin) -> AdmissionResult
}

type SkillBundle = {
  manifest: { name, description, version, triggers }
  body: text
  files: { path, kind: "reference"|"asset"|"script", digest }[]
  bundle_digest: bytes                  // content address over EVERY file
  executable: bool                      // false + executable content => refused
  declared_tool_ids: string[]           // INTERSECTED with the run's catalog
  signature: bytes?                     // required for third_party_import
  publisher_ref: string?                // required for third_party_import
}

type SkillOrigin = "first_party" | "tenant_authored"
                 | "agent_proposed" | "third_party_import"
```

- **One gate, every origin** (FR-152). FR-021's propose → human/eval gate →
  version → promote applies to all four. An import additionally requires
  provenance, a verified signature over `bundle_digest`, and a pinned upstream
  version on the FR-078 dependency footing. Gating only `agent_proposed` gates the
  source least likely to be adversarial and leaves the supply chain open.
- **Every file is scanned** (FR-113, FR-151). A skill body is read by the model
  exactly like a tool description and is typically far larger.
- **A script is a tool or the bundle is refused** (FR-151). `kind = "script"`
  registers through `tool-contract.md` — three gates, per-invocation safety, taint
  declaration, governance sign-off — or admission fails. Code that arrived as an
  attachment to a document has no execution path.
- **`declared_tool_ids` intersects, never unions** (FR-153). An entry the run does
  not already hold is ignored and recorded as `skill_capability_ignored`. A skill
  may never touch autonomy, approval policy, egress, data label, or region —
  otherwise "load this skill" is the tool-widening lever FR-111's ratchet closes,
  arriving through a different door.
- **`harness_digest` pins what could load; events record what did** (FR-154), so a
  fork or trajectory case attributes a behaviour to a skill version rather than to
  a library.

## `RunControl` — lifecycle operations (FR-005, FR-004)

Every terminal reason must be reachable through a defined operation; a terminal
state no caller can produce is a contract defect.

```
interface RunControl {
  steer(session_id, message, idempotency_key): void   // drained at a turn boundary
  cancel(session_id, reason, drain: bool): void       // the ONLY producer of `aborted`
  resume(session_id, from_checkpoint_id?): void       // resume, never restart (FR-024)
  tightenAutonomy(session_id, level): void            // ratchet only — never widens (FR-111)

  // Three distinct operations over the log — NOT one undifferentiated "replay" (FR-128)
  replay(session_id, to_seq?): ProjectedState         // side-effect-free; no model call, no tool
  fork(session_id, at_seq, overrides, actor): SessionId  // new run; external effects DISABLED
}
```

- `steer` delivers to the **running** session's queue under its serial lock and
  appends a `user_message` event — it is not a new run submission. Steering into a
  **suspended** run additionally invalidates the approval or input request it is
  suspended on (FR-106): a suspended run has no upcoming turn boundary to drain at,
  and the human has just changed the plan that approval authorized.
- `cancel` still honors the paired-result invariant: any outstanding `tool_use`
  receives a synthetic `tool_result` before termination, and the run returns its
  best partial artifact (FR-003, FR-067).
- `cancel` and every terminal path **reap the run's children** via
  `Delegation.reap` **and invalidate every outstanding approval and input request**
  via `Oversight.invalidate` before the parent terminates (FR-100, FR-106). A child
  outliving its parent is a defect; so is an authorization outliving the run it
  authorized — the second is worse, because it can still mutate the world.
- `tightenAutonomy` is one-way. There is no widening operation on this interface,
  and no other seam exposes one: a mid-run autonomy widening would be a direct
  prompt-injection lever (FR-111).
- `replay` is **pure**: it calls no model, executes no tool, emits no external
  effect, and appends nothing. It is how projections are rebuilt and how the
  FR-086 upcasting path is verified. A `replay` that can mutate anything is a
  defect, not a feature.
- `resume` continues **the same run** — same `session_id`, `agent_version`,
  `harness_digest`, budget, and audit chain — from its last `Checkpoint` (FR-024).
  It MUST resolve any `in_flight` idempotency claim by probe or human escalation
  before the next turn, never by re-execution (FR-127).
- `fork` creates a **new run** from `at_seq` with declared `overrides` (agent
  version, prompt, tool catalog, model), recording `forked_from_session_id` and
  `fork_seq`, and appends a `forked` event to the source run. External effects are
  disabled or confined to a scratch sandbox; the fork inherits **no** approvals
  (FR-106), draws its own budget, and writes its own audit chain — the source
  run's cost attribution and receipts are untouched. This is the primitive the
  behavioral-incident runbook needs (FR-096): reproduce a production failure
  against a candidate fix without touching the run that failed. Forking another
  tenant's run requires a content-access grant (FR-118), and a fork whose
  `harness_digest` differs from the source reports the divergence rather than
  presenting a different configuration's result as a reproduction (FR-129).

## `Oversight` — the human seam (FR-036, FR-103–FR-112)

Approval and elicitation share one durable-suspend mechanism and nothing else. An
unanswered *question* is not a denied *action*, so they are distinct lifecycles
with distinct expiry semantics — and only one of them authorizes anything.

```
interface Oversight {
  // Blocks the invocation; the run suspends durably at ZERO token cost.
  requestApproval(req: ApprovalRequest, ctx: RunContext) -> ApprovalOutcome
  // Agent -> human question. Carries NO authorization value (FR-110).
  requestInput(req: InputRequest, ctx: RunContext) -> InputOutcome
  // Called on cancel / terminal / reap / ceiling breach / steer-into-suspension.
  invalidate(session_id, reason: InvalidationReason): void
}

type ApprovalRequest = {
  action_ref: EventId                // WHICH call was gated — identity only
  approved_input_digest: bytes       // WHAT a grant authorizes (FR-103)
  kind: "single" | "batch" | "plan_preauth"
  member_digests: bytes[]            // enumerated set for batch / pre-auth (FR-109)
  effect_class: EffectClass
  context: ApprovalContext           // decision-ready, redaction-bound (FR-104)
  context_mode: "local" | "upstream" // where it may be rendered (FR-091)
  assignee: AssigneeRef              // never an implicit broadcast (FR-108)
  escalation: EscalationChain        // notify -> remind -> escalate -> expire
  required_approvals: int            // >1 for multi-party classes (FR-105)
  separation_of_duties: bool         // resolver != run initiator
  step_up_required: bool             // fresh re-auth at resolution time
  scope: ApprovalScope               // once | session | standing — ALWAYS expiring
  ttl_seconds: int
}

type ApprovalOutcome = {
  decision: "granted" | "granted_modified" | "denied" | "expired" | "invalidated"
  approved_input_digest: bytes       // RECOMPUTED on granted_modified (FR-107)
  authoritative_input: json?         // the approver's input, when modified
  rationale: string?                 // returned to the loop on denial (FR-107)
  resolver: { user_id, authn_method, channel }?   // humans only (FR-105)
  receipt_id: ReceiptId?             // chained authorization receipt (FR-112)
}

type InputOutcome = {
  status: "answered" | "expired_assumed_default" | "expired" | "invalidated"
  answer: json?                      // schema-validated; UNTRUSTED (FR-069)
  assumed_default: json?             // recorded, so the assumption is auditable
}

type InvalidationReason = "run_cancelled" | "run_terminal" | "reaped"
                        | "steered" | "ceiling_exhausted"
```

- **A grant authorizes a digest, not an intent** (FR-103). The pipeline recomputes
  the canonical digest immediately before execution and refuses divergence with
  `approval_mismatch`. `action_ref` survives a change of arguments; the digest does
  not, which is the whole point.
- **`granted_modified` makes the approver authoritative** (FR-107): the returned
  `authoritative_input` is what executes and what the recomputed digest binds, and
  the agent is not told it executed unmodified.
- **`Oversight` never runs on the model's word.** No `DelegationSpec`, tool input,
  hook, or steering message can construct a grant, widen a scope, or mark an
  approval satisfied. Approval scopes do not descend into children (FR-098).
- **Suspension is durable and free**: the run is checkpointed and evicted, not
  parked on a held connection or a polling turn (FR-036, FR-046). The checkpoint
  carries the pending approval and its bound digest (FR-126), the suspended
  interval is excluded from every latency SLI (FR-120), and rehydrate-to-next-call
  latency on resolution is itself measured — it is user-visible wait on the
  oversight path.
- **Every outcome is paired** (FR-003): grant, denial, expiry, mismatch, and
  invalidation each produce exactly one `tool_result` before the next
  `Provider.stream`.

## The loop terminal contract (FR-002, FR-004)

```
type Classification = TOOL_CALLS | CONTENT | EMPTY   // dispatch on this, not text

type TerminalReason =
  | completed | max_turns | cost_exhausted | error
  | aborted | prompt_too_long | hook_stopped | approval_expired | input_expired
```

- Callers MUST handle `TerminalReason` exhaustively.
- Every `tool_use` MUST have a paired `tool_result` (synthetic on cancel/error)
  before the next `Provider.stream` call. This is a **total invariant over all
  histories** and is therefore property-tested over generated event sequences,
  not by examples alone (FR-097).
- Every terminal reason maps to a producer: `aborted` ← `RunControl.cancel`;
  `cost_exhausted` ← a refused budget reservation (FR-083); `approval_expired` ←
  an approval TTL the run could not proceed without (FR-036); `input_expired` ←
  an `Oversight.requestInput` with `on_expiry = terminate` that went unanswered
  (FR-110); the rest from the loop's own guards.
- `approval_expired` and `input_expired` stay distinct because they mean different
  things to a caller: nobody *authorized* an action, versus nobody *answered* a
  question. Collapsing them would either fail runs that could have proceeded on a
  declared default or hide an assumption nobody recorded.

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

## `Persistence` — the three artifacts (FR-024, FR-126, FR-127)

One log, three derived artifacts that are never interchangeable.

```
interface Persistence {
  append(event: Event): Seq                     // the only write of truth (FR-006)
  checkpoint(session_id): CheckpointId          // machine-facing resume record (FR-024)
  snapshot(session_id, at_seq): SnapshotId      // disposable projection cache (FR-126)
  hydrate(session_id): SessionState             // snapshot + replay of the tail

  claim(idempotency_key, pair_ref): Claim       // write-ahead, BEFORE the effect (FR-127)
  resolveClaim(claim_id, resolution): void      // probe_confirmed | probe_absent | human_resolved
}
```

- **`checkpoint` ≠ `condensation`.** A checkpoint carries the covered `seq`, the
  open `in_flight` claim, the held reservation, the sandbox handle, the pending
  approval digest, the in-flight provider request id, open delegations, and the
  `harness_digest`. A condensation is model-facing context (FR-015) and cannot
  answer whether an external effect completed — the question a resume must answer.
- **`snapshot` is disposable.** Deleting every snapshot costs hydration time and
  changes nothing else. If dropping one changes behavior, it was being used as
  truth (FR-086).
- **`hydrate` is bounded** by `head_seq − snapshot.at_seq`, not by run length —
  replaying an unbounded history on every worker pickup is not a recovery strategy.
- **`claim` is committed before the effect leaves the process**, never after. A
  claim written on success protects against a retried *call* but not a crash
  *during* one. `resolveClaim` never re-executes (FR-127).

## `Telemetry` — the content-free signal class (FR-117–FR-123)

Telemetry is not a debugging back door. It is a separate signal class with a
narrower contract than the log, because it leaves through an export path the
per-tenant content key does not reach.

```
interface Telemetry {
  // Attributes are ALLOWLISTED by key. An unlisted key is dropped, not truncated.
  span(name, attrs: AllowlistedAttrs, links: SpanLink[]): Span
  metric(name, value, labels: FixedLabelSet, exemplar?: TraceRef): void
}
```

- **No content, no flag to add it** (FR-117). Prompt text, model output, tool
  arguments and results, memory, retrieved documents, and approval context
  packages never appear on a span, metric, or operational log. Free-text provider
  errors are reduced to typed classes and digests before export. There is no
  environment variable, debug mode, or support flag that changes this — because a
  content-bearing span is an unencrypted copy of customer data outside the
  crypto-shredding boundary (FR-080), and an erasure attestation that does not
  cover it is false.
- **Content is reachable only through the log**, under a `Content Access Grant`
  that emits a chained receipt on grant and on every read under it (FR-118).
- **Turn-scoped traces, emitted from the log** (FR-119, FR-120). A run is not one
  long root span: a span exports only when it ends, so a six-hour approval
  suspension or a killed worker would export late or never. Each turn (or plan
  step) is its own trace, linked to its predecessor and carrying `session.id`,
  `root_session_id`, `depth`, `tenant`, and the covered `seq` range; events carry
  the reciprocal `trace_id`/`span_id`. Emission is driven from durable events, so
  telemetry survives a worker kill and can be produced entirely in-boundary when
  `telemetry_sink_mode = local`.
- **Active time, not wall-clock** (FR-120). Duration is reported split; every
  latency SLI is computed on active time, with durable suspension measured on the
  human-oversight axis instead (FR-095).
- **Fixed label sets and exemplars** (FR-122). Session, user, task, and request
  identifiers are span/event dimensions, never metric labels; a metric reaches an
  individual run through an exemplar.
- **Context propagates outward** (FR-123): sandbox exec, connector/MCP calls, the
  provider request, and child sessions all carry W3C trace context, so external
  latency lands under the turn that caused it rather than as unattributed
  wall-clock — carrying correlation identifiers only, never tenant identity or
  content.
- **The attribute schema is versioned** (FR-121): the internal attribute model is
  the source of truth and is mapped to a pinned external convention
  (`gen_ai.*`) at the exporter, with dual-emit across a convention rename.
