# Contract: Deterministic Orchestration Plane

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

Multi-step orchestration is a **declarative plan** the platform evaluates, not a
sequence the model talks itself through (FR-102). The model is invoked *inside* a
step; it never decides the route *between* steps. Routing therefore spends **zero
model tokens**, and the control flow is reviewable, versionable, and replayable
like any other behavior-bearing config.

This is also where the platform gets its parallelism back. FR-079 withholds
fan-out from the model's discretion; a plan may fan out within the bounds of
FR-099 because the decision authority sits in reviewed configuration instead.

---

## Why this is a plane and not a prompt

| Property | Model-driven orchestration | Declarative plan |
|----------|---------------------------|------------------|
| Routing cost | Tokens per hop, every run | **Zero** |
| Determinism | Varies per sample | Same plan → same control flow |
| Reviewability | Read a prompt and hope | Diffable, versioned artifact |
| Eval gate | Nothing concrete to gate | The plan version is the gated unit |
| Replay | Reconstruct intent from text | Step transitions are typed events |
| Blast radius | Whatever the model decides | Declared per step, bounded before it runs |

A plan is **data** (FR-050) — a new plan is a config row, never a kernel fork.

## Plan model

```
type Plan = {
  plan_id: UUID
  tenant_id: UUID                    // RLS-scoped like every tenant-owned row
  version: int                       // immutable; a change is a new version
  status: "draft" | "gated" | "enabled" | "retired"
  steps: Step[]
  cost_envelope_usd: numeric         // reserved before step 1 (FR-099, FR-083)
}

type Step = {
  id: string
  kind: "agent" | "delegate_fanout" | "approval_gate" | "condition" | "loop"
  agent_id: UUID?                    // pinned at enable time (FR-088)
  agent_version: int?                // pinned; a deploy cannot shift it mid-plan
  route_model_id: string?            // pinned route; deterministic (FR-027, FR-037)
  scope: ScopeRef                    // subset of the plan principal's scope (FR-098)
  next: Transition[]                 // evaluated by the PLATFORM, no model call
  on_error: "fail" | "skip" | "retry"    // retry is bounded + classified (FR-023)
  acceptance: AcceptanceCriterion?   // no step self-declares success (FR-044)
}

type Transition = {
  when: Predicate                    // over typed step output + run state ONLY
  to: string                         // target step id, or "end"
}
```

- **`Predicate` is a closed, total expression language** over typed step outputs,
  terminal reasons, and run metadata. It has no model call, no I/O, no unbounded
  loop, and no free-text evaluation — otherwise "zero-token routing" is a claim
  rather than a property.
- **`loop` carries a hard iteration bound**; an unbounded plan loop is rejected at
  validation, not discovered at runtime.
- **A plan is a DAG plus bounded loops.** Cycles without a bound fail validation.

## Lifecycle gates

```
draft ──validate──► gated ──eval gate (FR-043) + governance sign-off (FR-096)──► enabled
                                                                                    │
                                                                              retired ◄┘
```

1. **Validate** — schema, reachability (every step reachable, `end` reachable from
   every step), bounded loops, closed predicates, and scope-subset proof per step.
2. **Eval gate** (FR-042, FR-043) — the plan version is the gated unit: ≥90% pass
   and zero regressions versus the current baseline.
3. **Governance sign-off** (FR-096) — recorded before a tenant may enable it,
   exactly as for a new tool, connector, or autonomy increase.
4. **Pinning** — `agent_version` and `route_model_id` are resolved and frozen at
   enable time (FR-088), so a concurrent deploy cannot change a running plan's
   behavior or bust its prompt prefix.

An enabled plan version is **immutable**. Editing means publishing a new version
through the same gates; in-flight runs finish on the version they started with
(FR-026).

## Execution semantics

- **Cost**: the plan reserves `cost_envelope_usd` before step 1 (FR-083); steps
  and any fan-out children draw from that envelope (FR-099). Envelope exhaustion
  terminates `cost_exhausted` with the partial artifact (FR-067).
- **Delegation**: a `delegate_fanout` step calls `Delegation.delegate` per
  `kernel-abi.md` — descent invariant, bounds, return validation, and reaping all
  apply unchanged. The plan holds decision authority; children stay read-only.
- **Approval**: an `approval_gate` step suspends the run **durably at zero token
  cost** and resumes on the approval event; an unanswered approval expires as a
  denial of that step (FR-036).
- **Checkpoint/resume**: each step boundary is a checkpoint (FR-024). An
  interrupted plan resumes at the last completed step, never from step 1.
- **Replay**: step entry, transition taken, and step outcome are typed events
  (FR-085), so a plan run reconstructs from the log alone — including *which*
  predicate branch fired and why.

## Events

| Event | Carries |
|-------|---------|
| `plan_started` | `plan_id`, `version`, resolved pins, reserved envelope |
| `plan_step_entered` | `step_id`, `kind`, pinned agent/model, scope |
| `plan_transition` | `from`, `to`, the predicate that matched (structure only) |
| `plan_step_exited` | `step_id`, outcome, acceptance result, usage |
| `plan_completed` | terminal reason (FR-004), envelope reconciliation |

All five are in the FR-085 taxonomy and identical across the internal log and any
externally published event contract.

## Invariants

- **Zero-token routing**: no `Provider.stream` call occurs while evaluating a
  transition. This is asserted by test against the deterministic provider (FR-097)
  — a routing hop that calls the model is a contract violation, not an
  optimization miss (SC-023).
- **No step self-declares success**: a step with an `acceptance` criterion is
  judged against it before its output flows onward (FR-044).
- **Scope descent**: every step's scope is a subset of the plan principal's scope,
  proven at validation *and* enforced at execution (FR-098) — a plan cannot be a
  privilege-escalation path around the tool boundary.
- **Tenant-scoped**: plans are tenant-owned rows under the same RLS policy as
  every other tenant-owned row (FR-011, FR-039); one tenant can neither enumerate
  nor execute another's plans.
- **Config, not fork**: adding a plan requires zero kernel changes (FR-050).
