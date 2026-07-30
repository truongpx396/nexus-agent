# Contract: Integration Ports & the Authority Boundary

**Feature**: `001-agent-platform` | **Phase 1** | **Plan**: [../plan.md](../plan.md)

Every third-party framework attaches through a port that already exists for the
platform's own implementation, is selectable by configuration, and is **optional**
— the platform runs complete with all of them disabled (FR-131, FR-050).

This document is the compatibility surface: what may be plugged in where, what
each integration is allowed to *become*, and what it must prove before a tenant
can use it.

---

## The authority boundary (FR-131)

An integration may supply **transport, capacity, storage, or presentation**. Six
responsibilities are never delegated:

| The platform always owns | Why it cannot move | Anchor |
|---|---|---|
| **Routing authority** | Data-label and capability-floor routing must be deterministic, auditable, and decided *before* the call — a vendor's runtime choice is neither | FR-037, FR-076, FR-088 |
| **Cost-ceiling authority** | A ceiling enforced only in a component the platform does not own is not a ceiling; enforcement is pre-spend and worker-local | FR-083 |
| **Source of truth** (state, cost) | Replay, audit, fork, and chargeback all derive from the append-only log; a vendor store is a projection | FR-006, FR-124 |
| **The release gate** | A gate hosted where the agent's ecosystem can write reopens spec-gaming; a vendor outage must never become a release decision | FR-043 |
| **The audit record** | Receipts are hash-chained and externally anchored under sign-only key custody | FR-081 |
| **Access to content** | Telemetry is content-free; plaintext is reachable only under a receipt-emitting grant | FR-117, FR-118 |

> **Adopt the tool, keep the authority.** Every one of these six is a control an
> enterprise buyer verifies directly. Trading one for a vendor feature swaps a
> governed control for an unaudited dependency.

---

## Port map

| Port | Built-in default | Known-good optional adapters | May become | Must never become |
|---|---|---|---|---|
| `Provider` (FR-027) | Native Anthropic / OpenAI-compatible / Bedrock / Vertex adapters | **LiteLLM**, OpenRouter, cloud model gateways, self-hosted vLLM / Ollama | Transport + capacity multiplexer, failover-as-capacity | The router; the ceiling; the model-choice authority (FR-132) |
| Durable queue (FR-046) | NATS JetStream | SQS, Redis Streams, **Temporal / Restate / Inngest / DBOS** | Scheduler + delivery guarantee | The event log; the approval mechanism; the exactly-once mechanism (FR-136) |
| Plan runner (FR-102) | Platform plan evaluator | Same durable-execution engines | Step scheduling and durability | The source of branch decisions, which must replay from the log |
| Telemetry export (FR-117) | OTLP → self-hosted collector | **Langfuse**, Arize/Phoenix, Braintrust, Grafana/Tempo, Datadog, Honeycomb | A view of structure, latency, tokens, and cost | A content store; an audit record; a second write path (FR-134) |
| Eval / datasets (FR-043) | Platform eval runner + judge in CI | Langfuse datasets, Braintrust, Promptfoo, DeepEval | Corpus hosting, score storage, analysis | The gate (FR-135); the judge's calibration (FR-141); the trial statistics or verdict (FR-137) |
| `Workspace` / sandbox (FR-047) | E2B | Docker, Firecracker, gVisor, local-OS isolation | The execution boundary | A path around the resource limits or egress allowlist |
| Connector catalog (FR-012) | Built-in connectors | MCP servers, per-tenant connectors | Capability | Unvetted, unscanned, or audience-unrestricted access (FR-113, FR-114) |
| Memory / retrieval (FR-019, FR-022) | File-first per-tenant memory | pgvector, Qdrant, Weaviate, a knowledge graph | A retrieval tier when scale justifies it | A trusted instruction channel — retrieved content stays untrusted (FR-087) |
| Prompt / config source (FR-042) | Version control + review | Prompt-management tools | An authoring surface | A runtime source — versions pin into the harness digest at start (FR-136, FR-129) |
| Vault / KMS | Deployment vault | HashiCorp Vault, cloud KMS/HSM | Secret custody | A component that can read the sign-only audit key (FR-081) |

---

## Model gateways (FR-132)

A gateway is attractive for exactly the reasons it is dangerous: it also does
routing, fallbacks, budgets, and caching. Those overlap responsibilities the
platform holds constitutionally, so the adapter is configured to *give them up*.

**Required posture:**

- The routing decision (data label → capability floor → snapshot) is made and
  recorded **before** the call (FR-088). The request names **one resolved,
  pinned model snapshot** (FR-078).
- Gateway-side **aliasing, automatic model substitution, and silent fallback are
  disabled**. A response whose model does not match the recorded decision is a
  typed failure — not a substitution. Otherwise a `regulated` payload can leave
  the boundary and the cost record prices the wrong model (FR-037, FR-084).
- Gateway budgets and rate limits **may** be enabled as defense in depth. They
  never replace the worker-local pre-spend reservation (FR-083).
- Failover across gateway backends is capacity management (FR-048), and each
  failover hop is recorded — it is not a routing decision.

**Conformance-critical behaviors** (each verified by FR-133, each a real,
observed failure mode in shipping gateways):

| Behavior | Why it matters | If unsupported |
|---|---|---|
| Per-class token reporting (uncached / cache-read / cache-write / output) | The >90% cache-read gate is derived from measurement, never estimated | Cache-read gate **not claimed** on that path; recorded on the adapter (FR-016, SC-003) |
| Cache-breakpoint preservation + affinity for the provider's real cache lifetime | A router that re-picks a backend after its own short affinity window expires sends a cold prefix to a provider still holding a warm one | Cache-read gate not claimed; cost model records the degradation |
| Native tool-calling round-trip | Tools are never parsed from free-form text | Adapter rejected — not a degradation the platform accepts (FR-027) |
| JSON-schema normalization per backend | One tool definition must work across providers without a fork | Adapter must declare which keywords it rewrites (FR-065) |
| Opaque reasoning round-trip | Some providers reject tool-call history whose reasoning segments were dropped; a gateway that strips a `thinking` parameter changes model behavior silently | Adapter rejected for backends that require it (FR-064) |
| Stream ordering and typed failure surfacing | The idle watchdog and failure classifier depend on it | Adapter rejected (FR-066, FR-023) |

---

## Observability backends (FR-134)

**One write path.** Telemetry reaches a backend only through the platform's OTLP
export, so the FR-117 deny-by-default attribute allowlist is the single choke
point. Vendor SDKs, auto-instrumentation agents, and framework callback hooks are
**prohibited** — they write directly and bypass it.

What a backend receives, and what it therefore can and cannot do:

| Lights up | Stays dark — by design |
|---|---|
| Trace tree, timeline, latency, TTFT | Observation input/output content |
| Per-class tokens and cost by model / tenant / surface | Prompt playground / replay |
| Sessions grouping a run's turn-scoped traces (FR-120) | LLM-as-judge over production traces |
| Scores pushed from the eval gate | "Add to dataset" from a live trace |
| Online quality scores from the in-boundary scorer (FR-140) | The scorer itself — it runs in-boundary, never in the backend |
| Terminal-reason, stuck-rate, approval-fatigue signals (FR-095) | Annotation of real conversations |

The right-hand column is not a gap awaiting a fix. Re-enabling it means content
in a system whose access control is its own (not the platform's row-level
security) and whose reads leave **no FR-118 receipt**.

- Tenant maps to the backend's isolation primitive (project / workspace).
- The export target is per-deployment configuration and **may be fully
  in-boundary** for BYOC (FR-091) — spans are emitted from the event log
  (FR-120), so a local sink is the same code path with a different destination.
- The legitimate content route to these platforms is the FR-125 export: consented,
  redacted, governance-signed cases pushed as **datasets** — never trace mining.

---

## Durable-execution engines (FR-136)

Permitted behind the queue port or the plan runner, with three lines held:

1. **The event log stays the source of truth.** The engine's history is a journal
   of how work was scheduled; projections, audit, replay, and fork derive from the
   platform's log (FR-006, FR-128).
2. **Approvals stay digest-bound** (FR-103), not engine-native signals — an
   engine signal authorizes a *step*, while the platform must authorize an exact
   resolved *invocation*.
3. **The write-ahead claim stays the exactly-once mechanism** (FR-127). An
   engine's idempotency is additive: it knows whether the *step* ran, never
   whether the *external effect* occurred.

---

## Admission (FR-133)

An adapter is enabled for a tenant only when:

- the conformance suite passes against the same normalized contracts the built-in
  adapters satisfy (FR-097), on the deterministic-provider harness where
  applicable;
- its **capability matrix** — supported / degraded / unsupported per contract
  feature — is recorded and versioned with the adapter;
- no claimed success criterion depends on a capability recorded as degraded or
  unsupported; and
- governance sign-off exists, as for any new tool or connector (FR-096).

A capability regression on an adapter version bump is a **failed dependency
deploy** (FR-078): revert to the pinned prior version. Undeclared partial support
is the failure this closes — an integration that silently drops a parameter or
coalesces a token class leaves a platform reporting metrics it no longer measures.
