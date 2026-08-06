# Specification Quality Checklist: Production-Grade AI Agent Platform

**Purpose**: Validate specification completeness and quality before proceeding to planning
**Created**: 2026-07-17
**Feature**: [spec.md](../spec.md)

## Content Quality

- [x] No implementation details (languages, frameworks, APIs)
- [x] Focused on user value and business needs
- [x] Written for non-technical stakeholders
- [x] All mandatory sections completed

## Requirement Completeness

- [x] No [NEEDS CLARIFICATION] markers remain
- [x] Requirements are testable and unambiguous
- [x] Success criteria are measurable
- [x] Success criteria are technology-agnostic (no implementation details)
- [x] All acceptance scenarios are defined
- [x] Edge cases are identified
- [x] Scope is clearly bounded
- [x] Dependencies and assumptions identified

## Feature Readiness

- [x] All functional requirements have clear acceptance criteria
- [x] User scenarios cover primary flows
- [x] Feature meets measurable outcomes defined in Success Criteria
- [x] No implementation details leak into specification

## Design Review (2026-07-27)

A production-readiness review of the Phase 0/1 artifacts closed a set of gaps
where two documents were individually correct but contradicted when combined, or
where a stated success criterion was not measurable from the designed data. All
are now resolved in the spec (FR-080–FR-097), data model, contracts, and research
(§13–§24).

- [x] Erasure/DSAR reconciled with the append-only log (crypto-shredding, FR-080)
- [x] Audit log made tamper-*evident* (hash chain + external anchor + sign-only key, FR-081)
- [x] Inbound webhook/callback authenticity required before the kernel (FR-082)
- [x] Cost ceilings enforced pre-spend by reservation, not post-hoc aggregation (FR-083)
- [x] Cost measurable: token classes split + versioned price book (FR-016, FR-084)
- [x] Event taxonomy complete (steering, approvals, taint, terminal) and versioned (FR-085, FR-086)
- [x] Rule of Two given declared inputs + a sanitization boundary (FR-087)
- [x] Run determinants persisted and `agent_version` pinned (FR-088)
- [x] Content encrypted at rest with BYOK; DR RPO/RTO rehearsed; residency enforced by placement (FR-089–FR-091)
- [x] Own-build supply chain, chargeback, expand/contract migration, SLO/error budget, named ownership (FR-092–FR-096)
- [x] Deterministic provider harness + property tests for total invariants (FR-097)
- [x] Tenant isolation proven **through** the connection pooler, not around it (FR-039, SC-013)
- [x] Every terminal reason has a producer — cancel added to both contracts (FR-005, SC-020)
- [x] Eval gate moved to Foundational; MVP cut line recorded in plan.md

### Human oversight (design review 2026-07-29)

- [x] An approval authorizes the **digest of the exact resolved call**, re-verified before execute, unified with the exactly-once key (FR-103, SC-024)
- [x] Approver sees a decision-ready context package; rendering location is configuration, not an assumption that breaks the egress boundary (FR-104, FR-091)
- [x] Approval resolution is authorized as well as authenticated — human-only, separated from the requester, step-up, single-use channel token (FR-105, SC-025)
- [x] No approval outlives the run it gates: invalidated on cancel / terminal / reap / ceiling / steer (FR-106, SC-026)
- [x] Decisions are grant / grant-with-modification / deny-with-rationale, and the rationale reaches the loop (FR-107)
- [x] Every request has a declared recipient with reminder + escalation before the fail-closed expiry (FR-108)
- [x] Fatigue bounded on the effect-class axis by a versioned policy, batching, and plan pre-authorization — not by a permanent "yes" (FR-109, SC-027)
- [x] The agent can **ask**: an input-request lifecycle distinct from approval, with caller-declared expiry (FR-110, SC-028)
- [x] `autonomy_level` has normative semantics, ratchets one way, and sits in one published total resolution order where a deny is final and safety/Rule-of-Two are unconditional (FR-111)
- [x] The authorization decision is chained and the gate is red-teamed, not asserted (FR-112, SC-005, SC-029)

### Guardrails, catalog trust & classification (comparative audit 2026-07-29)

- [x] Tool/connector/MCP descriptors scanned for injected instructions at admission and on version bump (FR-113, SC-030)
- [x] Connector/MCP tokens audience-restricted; a non-restrictable provider is rejected, not worked around (FR-114, SC-031)
- [x] Stuck detection eval-gated against negative cases; first trip signals, second terminates (FR-115, SC-032)
- [x] Gate-3 safety classifier committed to a hybrid rule-then-model shape with a fail-closed timeout (FR-116, SC-032)

### Observability & state management (design review 2026-07-30)

- [x] Telemetry is a content-free signal class enforced by a deny-by-default attribute allowlist, so it stays inside the crypto-shredding erasure boundary (FR-117, SC-033)
- [x] Reading decrypted content is a scoped, expiring, receipt-emitting grant — not an ambient operator capability (FR-118, SC-034)
- [x] Trace and event log join in **both** directions; the join key ships as a foundational schema seam (FR-119, SC-035)
- [x] Span model survives long, suspendable, and killed runs: turn-scoped, log-derived, active-time SLIs (FR-120, SC-035)
- [x] Telemetry attribute schema versioned internally and mapped to a pinned `gen_ai.*` convention at the exporter (FR-121)
- [x] Metric label sets fixed; per-run detail reached via exemplars, not high-cardinality labels (FR-122)
- [x] W3C trace context propagates into sandbox, connector/MCP, provider, and child sessions (FR-123)
- [x] Cost records are log projections shipped through a durable outbox — accounting is delayed by an outage, never lost (FR-124, SC-039)
- [x] Production→eval corpus growth has a consented, redacted, governance-signed export path rather than trace reading (FR-125)
- [x] Condensation / Checkpoint / Snapshot separated, with the resume set enumerated and hydration bounded (FR-126)
- [x] Idempotency claims committed write-ahead and resolved on resume by probe or human, never by re-execution (FR-127, SC-036)
- [x] `replay` / `resume` / `fork` defined as three operations with distinct guarantees (FR-128, SC-037)
- [x] A single harness digest pins every behavior-determining artifact and doubles as the cache-prefix identity (FR-129)
- [x] Compaction fidelity eval-gated, chain depth bounded, cache boundary ordered, execution mode declared (FR-130, SC-038)

### Ecosystem integration (design review 2026-07-30)

- [x] Integrations attach through existing ports, are optional, and the platform runs complete with all disabled (FR-131, SC-040)
- [x] One authority boundary: no vendor becomes routing, ceilings, truth, the gate, the audit record, or a content path (FR-131)
- [x] A model gateway is transport and capacity only — pinned snapshot per request, aliasing/fallback disabled, budgets as defense in depth (FR-132, SC-042)
- [x] Adapters admitted by a conformance suite with a recorded capability matrix; a degraded capability withdraws the claim that depends on it (FR-133, SC-041)
- [x] Observability backends integrate via OTLP only; vendor SDKs and auto-instrumentation prohibited (FR-134, SC-043)
- [x] External eval/dataset platforms host corpora and scores; the release gate stays in the platform's CI (FR-135)
- [x] Durable-execution engines may back the queue/plan runner with the log as truth, digest-bound approvals, and write-ahead claims intact; prompt stores author but never hot-swap (FR-136)

### Evaluation & measurement (design review 2026-07-30)

- [x] The gate is statistical: k trials, `pass^k`/`pass@k` by class, per-case intervals, regression as interval separation, three-valued verdict where `inconclusive` never resolves to `pass`, published minimum detectable effect (FR-137, SC-044)
- [x] The environment is pinned as its own digest, comparison across digests refused, trials on cold sandboxes from a declared memory/skill baseline, infra errors excluded from the denominator (FR-138, SC-045)
- [x] Suite classes (regression / capability / safety / negative) carry distinct thresholds and blocking semantics; safety admits no threshold below 100%; graduation and retirement recorded; every over-fireable control has a negative set (FR-139, SC-046)
- [x] Quality is measured in production by an in-boundary scorer emitting structure-free scores through the allowlist, feeding drift alerts and a rollout guardrail — not by a vendor judge over traces (FR-140, SC-047)
- [x] The judge is pinned, cross-family, and calibrated to a published agreement floor **before** it can block a change; drift re-sampled and alerted (FR-141, SC-048)
- [x] Fork-based trajectory cases reach step-level behavior via FR-128, graded against an acceptable-action set rather than a required sequence (FR-142, SC-049)
- [x] Every behavior-bearing artifact carries its own suite, and the corpus re-runs on a schedule to catch drift with no platform-side change (FR-143, SC-050)
- [x] Grader selection rule, binary verdicts, tiered grading cost, and a case-authoring bar including a reference solution; 0%-across-k quarantined as broken (FR-144, SC-051)
- [x] Efficiency (tokens / turns / tool calls / active time) blocks the gate on the same footing as quality; η$ and CPM reported as deltas (FR-145, SC-052)
- [x] Held-out protection mechanized *and* measured: grader store unreachable from the sandbox, verdicts computed by the runner, visible-vs-held-out gap reported, contamination bounded on both channels the platform creates (FR-146, SC-053)
- [x] `EvalSuite` / `EvalCase` / `EvalTrial` / `EvalRun` / `EvalEnvironmentDigest` / `Judge` / `JudgeCalibration` exist as first-class entities — every `eval_run_id` in the data model now resolves (data-model.md)

### Channels, tools & skills (design review 2026-07-31)

- [x] Tool identity is `{namespace}/{name}@{version}` with one owning source per namespace; collision refused at admission, never resolved by registration order; the alias map is governance-signed config a descriptor can never write (FR-147, SC-054)
- [x] Deferred disclosure reconciled with pinning: the harness digest covers the resolvable universe, loads land in the volatile zone as `tool_loaded` events, and the selector is eval-gated with measured selection accuracy (FR-148, SC-055)
- [x] The sandbox is not a bypass: agent-written code reaches a capability only through a broker that re-enters the pipeline, and a direct path to a connector is a prohibited egress route (FR-149, SC-056)
- [x] MCP listing caches are advisory with the descriptor digest re-verified at use; server-initiated user input is an input request that resolves no approval; structured results are validated but stay untrusted (FR-150, SC-057)
- [x] A skill is a signed, content-addressed bundle whose every file passes the injection scan, and a bundled script registers as a `Tool` or the bundle is refused (FR-151, SC-058)
- [x] One admission gate for every origin, with provenance, signature, and a pinned version required of third-party imports and a per-origin trust tier bounding what they may carry (FR-152, SC-058)
- [x] Skills are capability-**narrowing** only — declared tools intersect the resolved catalog, never extend it — and activation is a typed event (FR-153, SC-059)
- [x] Progressive disclosure is three-tiered, bounded, relevance-selected past the cap, and measured; the digest separates loadable from activated skills (FR-154)
- [x] Surfaces publish conformance-tested capability descriptors, approval routing filters on them, and an unservable approval policy is refused at configuration time (FR-155, SC-060)
- [x] Authority is the turn-submitting principal, never the conversation's opener; steer/cancel authorize per turn; a shared conversation carries an audience label bounding delivery and memory writes (FR-156, SC-061)
- [x] Outbound delivery is a durable outbox with the log entry preceding the send; an undelivered approval request stays distinguishable from an unanswered one (FR-157, SC-062)
- [x] `principal_kind` declared per surface; agent-principal ingress is its own admission class that resolves no approval and answers no input request (FR-158, SC-063)
- [x] Each surface declares a conversation binding resolving its native thread identity into `session_key`; cross-surface continuation only for the same principal under an explicit binding (FR-159)
- [x] `Catalog Manifest` / `Skill Bundle File` / `Surface` / `Delivery Record` exist as first-class entities, and `Tool`, `Skill`, `Surface Identity`, and `Session` carry the identity and attribution fields the above depend on (data-model.md)

### Cross-artifact consistency pass (`/speckit.analyze`, 2026-07-31)

A read-only consistency analysis across spec / plan / tasks / contracts found **no
CRITICAL issues and 100% FR coverage** (159/159 requirements mapped to tasks), but
12 real defects — concentrated where a document was correct when written and was
not revisited after a later design review landed. All are now closed.

- [x] `run-api.openapi.yaml` published **8** terminal reasons; FR-004, the kernel ABI, and T014 all specify **9**. `input_expired` added — a reason the kernel can produce and the external contract cannot express is a contract defect (FR-004, SC-020)
- [x] The external API predated FR-103–FR-110: one approval path, `decision: [grant, deny]`, no argument digest, no input requests. Rebuilt — `POST /approvals` (digest-bound, batch-enumerating, capability-routed), `GET`, `/resolve` (human-only, single-use token, step-up, `grant_modified`), `/invalidate`, and the two `/input-requests` paths, with typed refusal sets (FR-103–FR-110, FR-155)
- [x] `RunEvent.type` carried 17 of the taxonomy's 51 types while the contract itself promised the log and the external contract "MUST NOT diverge". Synced and verified equal to data-model.md (FR-085)
- [x] The Constitution Check recorded **v1.1.0** against a **v1.2.0** constitution. Re-run and re-recorded, with v1.2.0's two expanded Workflow rules mapped explicitly rather than assumed covered (FR-117/FR-118, FR-126–FR-130)
- [x] Three conflicting FR counts in plan.md (146, 136, 136) against an actual 159; surface count restated as 9 classes including agent-to-agent ingress (FR-158)
- [x] The MVP cut line was stale from the 2026-07-31 review — FR-147–FR-159 appeared in neither the Increment-1 list nor the deferred table while T011c called their seams foundational. Catalog/skill/surface identity seams added to Increment 1; deferred disclosure, the in-sandbox broker, skill import, and A2A ingress added as explicit deferrals with their trigger conditions
- [x] `integration-ports.md` was referenced by no task and `tool-contract.md` by no test, though both are load-bearing. Contract tests added (T027a asserts the total permission resolution order and its two invariants; T029h asserts the six withheld authorities), and both contracts are now cited from the tasks that implement them
- [x] SC-029 (every approval decision provable, authorization history reconstructable from the log alone) had an implementation task but **no verification task** — the only approval SC without one. T061g added
- [x] 11 success criteria were behaviourally covered but carried no `SC-` tag, defeating the automated coverage check the other 52 enable. All 63 now tagged
- [x] T147's make-target enumeration had drifted 4 targets behind quickstart.md while carrying a completeness clause; backfilled and replaced with a CI check that extracts targets from quickstart rather than restating them
- [x] plan.md's documentation tree listed 4 of 6 contracts; `orchestration-plane.md` and `integration-ports.md` added
- [x] Principle V's Status cell held a narrative paragraph after the verdict; moved to the compliance column so every row's status is a bare verdict
- [x] The 4 spec Key Entities that are fields rather than tables (`Condensation`, `Harness Digest`, `Taint State`, `Surface Capability`) are now enumerated in data-model.md with where each lives and why a table would be wrong

### Cross-artifact consistency pass (`/speckit.analyze`, 2026-08-06)

A read-only consistency analysis across spec / plan / tasks / data-model found **no
CRITICAL issues and 100% FR coverage** (175/175), but 5 real defects — all the same
class as 2026-07-31's: plan.md and data-model.md were correct when last revisited
and had not been reconciled since the 2026-08-01 → 2026-08-06 design-review batch
(FR-160–FR-175, SC-056–SC-071) landed. All are now closed.

- [x] plan.md stated "170 functional requirements" in three places (Scale/Scope,
  Constitution Check result, Complexity Tracking) against an actual 175 — the same
  defect class as the 2026-07-31 pass's "three conflicting FR counts." All three
  corrected to 175
- [x] FR-175 (zero-LLM keyword recall, added 2026-08-06) had full task coverage
  (T095d/T095e) and was grouped with the core Increment-1 built-ins in
  data-model.md, but appeared in neither plan.md's "In Increment 1" list nor its
  deferred table — silence, not a decision. Recorded explicitly in the deferred
  table as a deliberate sequencing choice (bundled with US5's memory work, not a
  technical dependency), leaving tasks.md's Phase 7 placement unchanged
- [x] FR-171 (the `PreToolUse`/`PostToolUse` hook layer) ships in Increment 1
  (Phase 3, US1/MVP; 5 dedicated tasks) but was absent from plan.md entirely —
  not in Technical Context, the Increment-1 list, or the Constitution Check.
  Added to both the Increment-1 bullet list and Constitution Check row V
- [x] SC-068 was the only success criterion in its batch (SC-064–SC-071) with no
  task-level `SC-` tag, defeating the coverage check the other 70 enable — the
  same defect class as 2026-07-31's "11 success criteria... carried no SC- tag."
  Added to T041q, which already covers its acceptance criteria
- [x] data-model.md's "fields not tables" reconciliation table claimed to resolve
  spec.md's Key Entities list completely but listed 4 of what are now 6 field-only
  entities (missing Prompt Mode and Memory Tier / Resolvable Memory Set, added by
  FR-172/FR-173). Both added with their actual location in the schema

## Notes

- Items marked incomplete require spec updates before `/speckit.clarify` or `/speckit.plan`
- The specification is intentionally large; it maps the full Enterprise Agent Master Plan and is phased so P1 (the reliable kernel) is a standalone MVP.
- The spec is the *target architecture*; [plan.md](../plan.md) carries the MVP cut line stating what Increment 1 actually ships and what is deliberately deferred.
