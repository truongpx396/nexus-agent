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
| Eval platform / datasets (FR-043) | Platform eval runner + judge in CI | Langfuse datasets, Braintrust, Arize | Corpus hosting, score storage, analysis | The gate (FR-135); the judge's calibration (FR-141); the trial statistics or verdict (FR-137) |
| *Grader libraries* — **not a port** (FR-135) | Platform code + model graders | **DeepEval, Promptfoo, Ragas** — pinned in-tree under `ml-python/` | Assertions and metrics *beneath* the platform's statistics | The verdict; an uncalibrated judge wearing a metric's name (FR-137, FR-141, FR-144) |
| `Workspace` / sandbox (FR-047) | E2B | Docker, Firecracker, gVisor, local-OS isolation | The execution boundary | A path around the resource limits or egress allowlist |
| Connector catalog (FR-012) | Built-in connectors | MCP servers, per-tenant connectors | Capability | Unvetted, unscanned, or audience-unrestricted access (FR-113, FR-114) |
| Memory / retrieval (FR-019, FR-022) | File-first per-tenant memory, then **pgvector** | Qdrant, Weaviate, a knowledge graph | A retrieval tier when scale justifies it | A trusted instruction channel (FR-087); an index that survives an erasure (FR-162) |
| Prompt / config source (FR-042) | Version control + review | Prompt-management tools | An authoring surface | A runtime source — versions pin into the harness digest at start (FR-136, FR-129) |
| Vault / KMS | Deployment vault | HashiCorp Vault, cloud KMS/HSM | Secret custody | A component that can read the sign-only audit key (FR-081) |
| `Surface` — frontend (FR-028, FR-155) | Platform SSE/WS event stream | **AG-UI** adapters, CopilotKit-style frontends, chat platform SDKs | Presentation and a wire format | The event log; a second progress source; a path around the capability descriptor (FR-155) |
| `Surface` — agent ingress (FR-158) | *None enabled by default* | **A2A** endpoints, agent-gateway products | An authenticated caller under a subset scope | An oversight resolver; an unmetered principal; a trusted instruction source (FR-105, FR-098) |
| Skill source (FR-152) | Version control + review | Skill registries and marketplaces | An authoring and distribution source | The admission gate — provenance, signature, pinned version, scan, and eval are the platform's (FR-151, FR-113) |

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

## Frontend and agent-ingress protocols (FR-155, FR-158)

The 2026 stack is three protocols: MCP for tools, A2A between agents, AG-UI to
frontends. The platform is already a thorough MCP *client*; the other two attach as
surfaces, and each meets a rule that already exists here.

- **AG-UI** is a presentation adapter. The platform's own `StreamEvents(v1)` is
  structure-only with `Last-Event-ID` resume — a **stronger** durability guarantee
  than a typed SSE stream with no sequence contract — so an AG-UI adapter renders
  that stream rather than replacing it. It may not become a second progress source,
  and it does not exempt a surface from declaring its capabilities (FR-155).
- **A2A** is an ingress class, not a peer relationship. An external agent is an
  **agent principal**, and FR-105 already forbids one from resolving an approval.
  Admission therefore requires: a declared `principal_kind = agent` surface, a
  delegated scope provably a subset of the authorizing tenant principal's (FR-098),
  source taint as untrusted content (FR-087), a named payer (FR-017, FR-083), and
  no ability to resolve an approval **or answer an input request** (FR-110) — a
  question answered by the agent that raised the situation is not oversight.
  Disabled by default; enabling it is a governance decision (FR-096).

## Evaluation: a platform and a library are different things (FR-135)

The eval ecosystem ships in two shapes that are routinely conflated at
integration time, and they carry different risk. The port map has a row for each.

**A hosted platform** — Langfuse datasets, Braintrust — is an adapter on the
`eval` port. It stores corpora and receives scores. It is admitted like any
adapter and holds no gate authority; `no_gate_authority` and `score_only_egress`
are recorded conformance dimensions rather than policy prose.

**A grader library** — DeepEval, Promptfoo, Ragas — is *not* an adapter. It is a
pinned in-tree dependency of the eval runner under FR-078, and it gets no port
row because it runs inside the platform's own CI. What it supplies is
**assertions and metrics beneath the platform's statistics**:

| The library provides | The platform still owns |
|---|---|
| Assertion primitives, metric implementations, RAG metrics, test scaffolding | The k-trial design, exact intervals, interval-separation regression definition, and the three-valued verdict (FR-137) |
| A convenient way to express a check | Which checks are deterministic and which need a judge (FR-144) |
| Model-graded metrics (G-Eval, model-graded rubrics, faithfulness, answer relevancy) | Those *are judges* — pinned snapshot, different model family, calibration floor met before blocking (FR-141) |

Two failure modes this closes. Adopting a library's per-assertion pass/fail as
the gate's decision silently replaces a statistical gate with a point estimate —
these libraries do not implement FR-137 and are not trying to. And enabling a
model-graded metric by name in a config file installs an **uncalibrated
instrument with a credible label**; it reaches the gate as a `Judge` row or not
at all.

## Adversarial discovery, off the gate (FR-163)

Automated red-team scanners — Garak, a grader library's red-team module, an
internal generator — close a real hole: the `safety` class tests only the attacks
someone already imagined, and FR-143's scheduled re-run discovers none. They
attach as **scheduled discovery**, never as an adapter and never as a gate.

- **Findings are candidates, not verdicts.** A finding is triaged by a human and
  promoted into the versioned `safety` class, where it becomes gating in the
  ordinary way. Nothing generated at scan time blocks a release: probe generation
  is non-deterministic, which is irreconcilable with FR-137's per-case trials and
  FR-138's refusal to compare across environment digests.
- **The scan runs through a real surface**, as a declared non-human
  `principal_kind` (FR-158) against a dedicated eval tenant. A scan pointed at a
  bare provider endpoint measures the model; every control worth testing here —
  the Rule of Two, the digest-bound approval, the egress allowlist, the broker,
  taint propagation — lives between the surface and the model.
- **Coverage is recorded as partial.** A model-level scanner substantially
  exercises the FR-069 input guard and the FR-068 egress sanitizer and reaches
  almost none of the oversight, permission, or effect machinery. A clean scan is
  reported as what it is; it is never read as adversarial assurance.
- Finding rate, triage backlog, and promotion count are signals on the FR-095
  footing — a scan nobody triages is a report, not a control.

## Retrieval backends (FR-022, FR-162)

The retrieval port is the one place where adopting the obvious tool costs
guarantees the built-in store gives for free. Postgres already carries the
platform's trust model; a dedicated vector database carries none of it and must
re-earn each one:

| Guarantee | pgvector | A dedicated vector store |
|---|---|---|
| Tenant isolation (FR-039) | The same row-level security, transaction-locally scoped | A collection per tenant, or a payload filter the application supplies — the latter is an **application ACL**, the enforcement model Principle VI rejects |
| Erasure (FR-080, FR-162) | Rows deleted in the same transaction that destroys the key | A second erasure path in a second system, and vectors it never encrypted |
| Residency (FR-091) | The tenant's pinned region, already enforced by placement | A second placement story to build and prove |
| Restore (FR-090) | One PITR, one drill, one RPO/RTO | A second backup posture inside the same objectives |

So: **pgvector is the default when the file-first tier stops being enough, and a
dedicated store is the escape hatch scale justifies** — adopted deliberately,
with `tenant_isolation_primitive`, `subject_level_delete`, `region_placement`,
and `pitr_backup` recorded in its capability matrix. An adapter that cannot
enumerate and delete a subject's rows is not admissible for a tenant with an
erasure obligation, whatever its recall.

Independent of backend, the index is a **projection of the log, never a store of
record** (FR-162). That is what makes it deletable at all, and encrypting it is
not a substitute — published inversion attacks recover substantive text from
embeddings, so a vector surviving a crypto-shred is retained content.

And it is measured: no retrieval tier is enabled, and no embedding-model,
chunking, reranker, or top-K change ships, without the `retrieval` suite class of
FR-161 — context precision/recall, first-relevant rank, and citation validity by
code graders; groundedness by a judge under FR-141; retrieved tokens per query on
the FR-145 efficiency footing, because raising recall by injecting more context
is a cost regression a quality metric reports as a win.

## Content conversion is a tool, not a port (FR-058, FR-160)

Web extraction (crawl4ai) and document conversion (MarkItDown, Docling,
Unstructured) are **built-in tools**, not adapters — they supply no platform
capability that a port abstracts, and swapping one changes a tool's
implementation rather than a plane's authority. The rules they inherit are
already written; three are worth stating because placement is easy to get wrong:

- **They execute in the sandbox** (FR-047, FR-059), never in the runtime worker.
  A headless-browser extractor renders attacker-supplied HTML and script, and PDF
  and Office parsers are a first-tier memory-safety surface fed bytes the
  attacker chose. This is also what resolves the language seam: the extraction
  and conversion backends are an in-sandbox concern, not a Python dependency of
  the Go worker.
- **Their output is untrusted** (FR-087 `returns_untrusted_content`). A document
  carries injected instructions and hidden text exactly as a web page does.
  Conversion is decoding, not sanitization; ingestion into memory or a corpus
  still clears FR-075 provenance and poison screening afterwards.
- **A model-backed conversion is a model call.** Image description, chart
  interpretation, transcription, and multimodal OCR go through the `Provider`
  port (FR-027) — routed by data label, reserved pre-spend, metered per token
  class. A converter holding its own provider client is an unrouted, unmetered,
  unceilinged call arriving under a parser's name, and it would carry a
  `regulated` document across the boundary FR-091 pins.

## Skill registries (FR-152)

A registry may distribute; it may never admit. Provenance, signature verification
over `bundle_digest`, version pinning, the FR-113 file-level scan, the per-skill
suite (FR-143), and the trust tier are all platform-side, and an unsigned or
unprovenanced import is refused rather than admitted at a reduced tier. The
measured malicious population in public skill registries is why this port is
listed as a *source*, on the same footing as a prompt-management tool: it may
author, never authorize.

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
  feature — is recorded and versioned with the adapter, on the dimensions
  declared **for its port**: a `retrieval` adapter is tested on isolation
  primitive, subject- and tenant-level delete, region placement, and restore
  posture; an `eval` adapter on score-only egress and the absence of gate
  authority; a `surface` adapter on the FR-155 descriptor and `principal_kind`;
  a `skill_source` on provenance, signature, and pinning. A port whose
  dimensions are undeclared admits no adapters — one provider-shaped list says
  nothing about a store or a gate;
- no claimed success criterion depends on a capability recorded as degraded or
  unsupported; and
- governance sign-off exists, as for any new tool or connector (FR-096).

A capability regression on an adapter version bump is a **failed dependency
deploy** (FR-078): revert to the pinned prior version. Undeclared partial support
is the failure this closes — an integration that silently drops a parameter or
coalesces a token class leaves a platform reporting metrics it no longer measures.
