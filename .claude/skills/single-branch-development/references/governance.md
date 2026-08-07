# Governance Discovery & the Persisted Governance Bundle

The standing rules a run must satisfy — the project **constitution**, the matched
**`.github/instructions/*`**, the **design artefacts** for frontend work — are not review
paperwork. They are *maker* constraints: the brief that produces the code must already carry them,
or you pay a review round-trip for every violation. This reference owns the full procedure. The
SKILL body carries only the summary and the hard rules.

**Run this once, at execution-core entry, before any code is written or any subagent is
dispatched.** It is a main-session, in-context read — see [Why it can't be delegated](#why-it-cant-be-delegated).

## The problem this procedure solves twice

There are two distinct failure modes, and the fix for one is not the fix for the other.

1. **The governance was never read.** A maker brief that says *"follow `go.instructions.md`"*
   passes a filename to an agent whose context does not contain that file. The subagent has nothing
   to act on. This is the round-trip that ships hardcoded `POSTGRES_PASSWORD` in a bootstrap PR.
2. **The governance was read, then lost.** This pipeline runs long — an execution core spans N
   maker + reviewer dispatches. Somewhere in there the session's context gets **compacted**, and
   bulk pasted file content is the first thing compaction drops. Afterwards the model remembers
   *that* it read the instructions but no longer holds the excerpts, so it silently degrades into
   failure mode 1 — while believing it complied.

Reading the files fixes (1). Only **persisting the distilled bundle to disk** fixes (2).

## Step 1 — Discover

Read these, in this order. Each is a **valid no-op when the file genuinely does not exist** — but
the *check* must happen, and its outcome must be stated. Never no-op by omission.

1. **Constitution** — `.specify/memory/constitution.md`. Extract the principles that bear on this
   task's surface. Absent → note it and continue.
2. **Matched instructions** — list `.github/instructions/` and read every file whose `applyTo` glob
   overlaps the paths this task batch will touch.
   - **Always**: `code-review-generic.instructions.md` (`applyTo: '**'`) — the baseline rubric.
   - By surface: `go.instructions.md` (`**/*.go`) · `reactjs.instructions.md` +
     `state-management.instructions.md` (`**/*.tsx`, `**/*.ts`) · `python.instructions.md`
     (`**/*.py`) · `devops-cicd.instructions.md` (Dockerfiles / Compose / CI) ·
     `backing-services.instructions.md` (infra / backing-service config).
3. **Design context (frontend only)** — when the surface includes `**/*.tsx`, `**/*.ts`, `**/*.jsx`,
   `**/*.css` or any other frontend file, read the design artefacts if present (pass silently if
   absent, never fail): `.stitch/designs/<page>.html` for the page being built, and the
   `design-system/` master + page spec. Generated UI must match the approved design from the start
   rather than diverging into a separate alignment pass.
4. **Security** — for any cluster touching a trust boundary (auth, secrets, network, persistence,
   deploy config): `security-and-owasp.instructions.md`.

### Budget the read — distil, don't hoard

The matched set is large: `security-and-owasp` alone is ~1,000 lines, and a Go+React+compose surface
can match ~3,000 lines across seven files. Holding all of that raw in the main session for the whole
core is the single biggest context-pressure source in this pipeline, and it is exactly what
compaction evicts.

So **read fully, then distil immediately**. What you carry forward is not the files — it is the set
of *binding constraints that apply to this diff*, each one concrete enough to act on
(`pin image tags, never :latest`, not `follow container best practice`). Typically 30–60 lines total.
Write those to the bundle (Step 2) and let the raw text go.

## Step 2 — Persist the bundle

Write the distilled constraints to **`runs/<RUN_ID>.governance.md`**, then pin it into the run
record:

```bash
bash .github/hooks/track-note.sh governance "runs/$RUN_ID.governance.md"
```

`runs/` is gitignored, so the bundle never pollutes the diff or shifts the evidence fingerprint.
`track-note.sh governance` records the path **and a sha**, so a later reader can tell whether the
bundle changed after the briefs were built; `track-reconcile.sh` reports
`position.governance_bundle_present:false` and tells you to re-run discovery if the file has since
vanished.

### Bundle format

```markdown
# Governance bundle — run <RUN_ID>
Surface: backend-go/**, deploy/compose.yml

## Constitution (.specify/memory/constitution.md) — PRESENT
- kernel/ must not import from internal/product/** (principle 3)
- coverage floor 80% on new packages (principle 7)

## code-review-generic.instructions.md — ALWAYS
- <the specific rules that bite this diff>

## go.instructions.md — matched **/*.go
- errors wrapped with %w, never %v
- no naked returns in exported funcs

## security-and-owasp.instructions.md — matched (compose touches secrets + network)
- pinned image digests, never :latest
- no default credentials committed; env placeholder + documented dev fallback

## Design (.stitch/designs/…, design-system/…) — ABSENT (no frontend surface)

## Cluster → binding sections  (only when the core fans out to parallel makers)
- go cluster (cmd/, kernel/, internal/, go.mod): Constitution I/II, code-review-generic, go
- deploy cluster (compose.yml, Caddyfile, .env*): code-review-generic, devops-cicd, backing-services, security-and-owasp
```

State **ABSENT** explicitly for every check that no-opped. An absent line is proof the check ran; a
missing line is indistinguishable from a skipped check.

### Pre-slice governance to the clusters that will consume it

The per-file sections above are organized by *instruction file*, but a fan-out core
(`dispatching-parallel-agents`) dispatches **one brief per disjoint-file cluster** — and each brief
carries only the governance that binds *its* files, not the whole bundle. Whenever the core fans out
to more than one parallel maker (scaffold `generate`, story RED-authoring, refactor pin-green),
append a **`## Cluster → binding sections`** map so that slicing is done **once, in the bundle**,
not re-derived per dispatch. Each row names a cluster (by its file surface) and lists exactly which
sections above bind it — including `ABSENT`/design notes where they matter (a frontend cluster with
no design artefact should say so). A single-brief core (a lone story task, N=1) does not need the
map: there is only one consumer. See [`scaffold-mode.md`](scaffold-mode.md) GENERATE for the
cluster-brief contract this feeds.

## Step 3 — Push it into every brief

Every subagent brief — `dispatching-parallel-agents` fan-out makers **and**
`subagent-driven-development` per-task makers and reviewers — embeds the **bundle's content**, and
says it is binding: the code it returns must *already* satisfy these (pinned image tags, no
committed default credentials, secure headers, strict type/lint, parameterized queries).

When the bundle carries a **`## Cluster → binding sections`** map, a fan-out brief embeds **only the
sections that map names for its cluster** — the whole bundle re-pasted into every brief is context
waste and buries the constraints that actually bite. The map is the routing table; the per-file
sections are the content it points at. Embed content, never the filename or the bare row.

Governance therefore gates **both ends** — the maker brief prevents the violation, the review
catches what slipped through. That is deliberate defense-in-depth, not redundancy. Review is the
backstop, never the first place governance is consulted.

## Step 4 — Re-anchor after a compaction

If the context was compacted (or the session crashed and resumed) at any point during the core:
**re-read `runs/<RUN_ID>.governance.md` from disk before dispatching the next subagent.** It is a
~50-line read, it is authoritative, and it costs nothing next to shipping an ungoverned brief.
`track-reconcile.sh`'s `resume_action` says this explicitly on every resume.

## Why it can't be delegated

- **Not a subagent task.** Subagents have isolated context. "Read the instructions, then brief
  yourself" does not survive the process boundary — whatever the subagent learned dies with it.
- **Not a filename reference.** Passing `go.instructions.md` to an agent that cannot open it, or
  whose context does not hold it, transfers nothing. Pass content.
- **Editor auto-injection does not propagate.** VS Code's `applyTo` injection populates the *main*
  session only; it reaches no dispatched subagent. Claude Code does not auto-inject at all. Both
  are why this procedure is skill-driven rather than editor-driven — the gate is identical on
  either surface.

## Checklist

- [ ] Constitution read, or explicitly noted absent
- [ ] `code-review-generic.instructions.md` read (always in scope)
- [ ] Every `applyTo`-matching instruction file read
- [ ] Design artefacts read for any frontend surface, or noted absent
- [ ] `security-and-owasp.instructions.md` read for any trust-boundary surface
- [ ] Constraints distilled and written to `runs/<RUN_ID>.governance.md`
- [ ] `## Cluster → binding sections` map added when the core fans out to parallel makers
- [ ] `track-note.sh governance <path>` called
- [ ] Bundle content embedded in every maker and reviewer brief
