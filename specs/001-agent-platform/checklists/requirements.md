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

### Cross-artifact consistency pass (`/speckit.analyze`, 2026-08-09)

A read-only consistency analysis across spec / plan / tasks / data-model / contracts
found **no CRITICAL issues, 100% FR coverage** (190/190), zero unmapped tasks, and
zero dangling task references — but 16 defects, again the same class: spec.md,
tasks.md, data-model.md, and contracts/ were reconciled on 2026-08-09 while plan.md,
research.md, quickstart.md, and this checklist were last touched on 2026-08-06, so
the three review batches after it (FR-176; FR-177–FR-178; FR-179–FR-190) had landed
in four artifacts and not in four others. All 16 are now closed.

- [x] **Conflicting requirement**: FR-093, US4 acceptance scenario 6, T092b, and
  T092c all required chargeback to reconcile **to the sum of per-turn cost
  records** — exactly what FR-180 forbids ("rounding MUST NOT be applied per record
  and then summed"). T092c asserted it as an integration test while T092y1 asserted
  the opposite, so the two tests could not both pass. FR-093 rewritten around
  recompute-from-quantities, scenario 6 and SC-076 amended, T092b/T092c rewritten
  (T092c now carries a control assertion that the naive sum **diverges**, so the
  forbidden implementation cannot pass), T094g's delegation roll-up corrected the
  same way
- [x] **Entity name collision**: FR-184's commercial `Plan` and FR-102's
  `Orchestration Plan` were two different entities both keyed `plan_id` /
  `plan_version`, with `Session.plan_id` meaning the orchestration one — an
  ambiguity a Go type and a SQL join both resolve silently and wrongly. Renamed
  `Billing Plan` (`billing_plan_id` / `billing_plan_version`) in FR-184, Key
  Entities, data-model.md, T011e, T092x, and control-data-plane.md, with the reason
  recorded so it is not re-collapsed
- [x] **Constitution tension**: FR-188 enumerated `enforcement_disabled` and
  `provider_exempt` as `skip` reasons and required them to be *alerted*, but no
  requirement said when — or whether — the pre-spend gate may be disabled, against
  the constitution's unqualified "every turn MUST reserve … before the model call."
  FR-188 now bounds every skip reason: `no_matching_budget` and `price_unresolved`
  **refuse** rather than admit unmetered, `enforcement_disabled` is unsettable for a
  tenant with a finite ceiling and in any multi-tenant topology, `provider_exempt`
  is restricted to non-cash paths and still writes a list-priced usage record. A
  `skip` is a recorded defect state, never a supported configuration
- [x] plan.md stated "175 functional requirements" in three places against an actual
  190 — the third recurrence of this exact defect (170→175 on 2026-08-06,
  conflicting counts on 2026-07-31). All three corrected to 190
- [x] FR-176–FR-190 appeared nowhere in plan.md: not in Constitution Check, not in
  the MVP cut line, not in Technical Context, not in Project Structure. Added —
  Principle IV row rewritten around the full cost/billing contract, Principle V row
  extended with the tenant-scoped tool profile, extensible effect-class taxonomy,
  and MCP authorization flow; an Increment-1 bullet for the money and metering
  seams; five deferral rows (non-token emitters, period close, reservation chunking,
  credit issuance, override administration) each stating what is deferred and what
  seam is not; `internal/billing/` added to the tree and `internal/cost/` rewritten
- [x] **Missing event types**: `budget_decision` (required by FR-085 and FR-188) and
  `memory_loaded` (asserted by plan.md to be in the Foundational taxonomy) were both
  absent from data-model.md's Event taxonomy table — the table that declares itself
  "identical to the externally published event contract." Both added as new Cost &
  budget / Memory groups, and T014's taxonomy list rewritten to match the table
  group for group rather than paraphrasing a subset
- [x] T014 specified a **9-value** `TerminalReason` enum against the 10 that FR-004,
  kernel-abi.md, and run-api.openapi.yaml all define — `credit_exhausted` (FR-182)
  was missing, so the Foundational task would have built the enum one value short of
  the contract SC-020 requires it to match. Corrected to 10 and enumerated
- [x] **Missing currency**: `Billing Period.asserted_total` / `late_arrival_value`
  and `Budget Decision.estimated_amount` were bare `numeric(20,10)` against FR-180's
  "every monetary amount MUST carry an explicit currency" — the only money-bearing
  entities without one. `currency` added to both, plus to T092w/T026a6 and SC-076
- [x] **Missing plan provenance**: FR-184 makes `billable_meters` decide what is
  charged at all, but no record named the billing-plan version in effect, so
  FR-183's recompute at close was not reproducible across a mid-period plan change.
  `billing_plan_version` added to `Billing Period`, `Cost Record`, `Usage Record`,
  FR-184, T011e, and T092w
- [x] **Duplicate task ID**: two consecutive `T095d` lines (a stub and the full
  task), both FR-175 — 439 unique ids across 440 task lines. Stub removed
- [x] 13 same-phase `[P]` task pairs wrote the same file, contradicting tasks.md's
  own `[P]` rule; two carried an explicit stated dependency (T010b "extends T010a",
  T092y "extends T092b"). `[P]` dropped from the extending task in each pair
  (T010b, T014b, T022a, T086a, T092d, T092y, T100d, T117b, T131a, T143) — zero
  same-phase file collisions remain
- [x] US4 owns all 12 billing FRs but its acceptance scenarios covered none of them.
  Five scenarios added (credit exhaustion vs ceiling, non-token meters, closed-period
  late arrival, gate-decision recording incl. skip, every-applicable-budget-binds),
  and the Independent Test extended with the commercial half
- [x] SC-068 was **still** the only success criterion with no task-level tag — the
  2026-08-06 pass recorded adding it to T041q, but the tag did not survive the
  FR-171 hook-layer batch. Re-added to T041q along with FR-166; SC coverage is now
  84/84
- [x] research.md stopped at §32 with no rationale or rejected alternatives for five
  review batches. §33 (domain portability of the permission and approval
  vocabularies) and §34 (billing, credit ledger, non-token metering) added, plus two
  rows in the resolved-unknowns summary. §16's "a hard ceiling is a safety control,
  not an invoice" annotated as **still true** — FR-182 adds a second control beside
  the ceiling rather than reversing it, which is why `credit_exhausted` and
  `cost_exhausted` stay distinct
- [x] quickstart.md had no scenario for the commercial half. Scenario 4b added
  (meters report, credit balance as a fold, money-exactness recompute, period close
  refusal and late-arrival adjustment, budget-decision listing incl. bounded skips,
  counter rebuild under a flushed epoch)
- [x] spec.md's Key Entities omitted `FX Rate`, which data-model.md models as an
  entity and FR-180 makes mandatory for multi-currency. Added

## Notes

- Items marked incomplete require spec updates before `/speckit.clarify` or `/speckit.plan`
- The specification is intentionally large; it maps the full Enterprise Agent Master Plan and is phased so P1 (the reliable kernel) is a standalone MVP.
- The spec is the *target architecture*; [plan.md](../plan.md) carries the MVP cut line stating what Increment 1 actually ships and what is deliberately deferred.
