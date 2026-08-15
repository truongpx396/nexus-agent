# Scaffold Mode (Optional) — Batch In-Session Fan-Out

Scaffold mode is **one of the skill's three execution cores** (the others are
[story mode](story-mode.md) and [refactor mode](refactor-mode.md)).
Story mode handles behavioral work that adds or changes behavior — it authors a failing RED test batch,
then greens implementation via `subagent-driven-development` (SDD); refactor mode handles
behavior-preserving change to existing code (keep-green). Scaffold mode is the **non-behavioral
counterpart**, for a narrow, explicitly-declared class of work: *mechanical, non-behavioral bootstrap
files with no test obligation and no trust-boundary surface.*

Everything **around** the core is unchanged — the same preflight, isolation, run-log/`RUN_ID`,
hooks bundle, governance gate, evidence gate, and draft-PR finish. Only the core gates swap out.

## Why it exists

Bootstrap stages (project skeleton, dependency manifests, lint/format configs, compose/proxy files,
`Makefile` targets, test-harness scaffolding) are:

- **`[P]`-heavy** — many disjoint one-file tasks with no interdependencies, *and*
- **non-behavioral** — there is nothing to test-first; a `docker-compose.yml` or `.golangci.yml` has
  no red/green cycle. Forcing them through SDD's per-task TDD + two-stage review loop is pure
  overhead for zero quality gain.

Scaffold mode exploits the `[P]` disjointness for **parallel generation latency** while keeping the
one guarantee that still matters for bootstrap: *the scaffold actually builds and comes up.*

## The hard boundary — why `[P]` is NOT the trigger

`[P]` means "different files, no incomplete-task dependency." It says **nothing** about whether a
task is behavioral. In a real plan, `[P]` sits on both:

- **Non-behavioral scaffolds** — e.g. compose files, lint configs, manifests. ✅ eligible.
- **Behavior-bearing, security-critical code** — e.g. an access-control filter (often a release
  blocker), hybrid retrieval, auth middleware. These **must** go through story mode's RED-batch TDD +
  spec review + security review. ❌ never eligible.

If scaffold mode ever keys on `[P]`, it will eventually eat a security-critical task and **silently
skip test-first + the two-stage review** on exactly the path you can least afford. So the trigger is
an **explicit allowlist / `scaffold_only` flag on the batch**, plus a refusal guard (below) — never
the `[P]` marker itself.

### Eligibility guard (refuse the whole batch on any hit)

Before generating anything, assert **every** task in the batch is non-behavioral. Refuse and route
the batch to **story mode** if **any** task:

1. has a contract/integration/unit **test obligation**, or
2. touches a **trust boundary** — input handling, auth/authz, secrets, DB/persistence, or network, or
3. carries a requirement/spec ID tied to a security or correctness success-criterion.

The guard is all-or-nothing: one behavioral task in the set disqualifies the *batch*, not just that
task. When in doubt, treat a task as behavioral and refuse — the cost of a wrong refusal is one
story-mode run; the cost of a wrong acceptance is unreviewed security code.

## Pipeline (scaffold core)

The preflight/isolate/governance entry and the draft-PR boundary are identical to the universal
bracket. Only the core differs. **Gates are named, not numbered** — the SKILL body numbers its
bracket 1–8 and a bare number means different things in the two documents:

```
[bracket]   Preflight & isolate branch              [reuse: track-preflight.sh, using-git-worktrees]
[bracket]   GOVERNANCE GATE — discover, distil,
            persist runs/<RUN_ID>.governance.md      [reuse: references/governance.md]
MODE GUARD  assert every batched task is
            non-behavioral                           [refuse → story mode]
RESOLVE     ├ PROBE the installed toolchain and every
            │ version the batch will materialize     [parallel ✅  read-only, DELEGABLE]
            └ PIN the versions, resolve the conflicts,
              run any required install, optionally lay
              down a pinned generator's BASELINE tree
              (Bash only) as fan-out INPUT; append to
              the bundle, re-pin it                  [serial, one human confirm, NOT delegable]
GENERATE    fan out N read-only subagents, one per
            INDEPENDENT DOMAIN / DISJOINT-FILE
            CLUSTER (not one-per-file, not
            one-per-task); each RETURNS file bodies
            as strings, no disk writes               [parallel ✅  dispatching-parallel-agents]
APPLY       controller writes all returned bodies    [serial, single writer, instant]
MATERIALIZE controller runs the pinned RESOLVERS over
            the applied manifests to produce real
            lockfiles (Bash only, never hand-authored) [serial → go mod tidy / uv lock / npm install]
REVIEW GATE ONE code review over the whole diff      [serial → requesting-code-review]
CONVERGENCE freeze, then ONE batch verify against
   GATE     the converged tree: build (all runtimes)
            + lint + bring-up health check           [serial → verification-before-completion]
[bracket]   Draft-PR finish                          [overrides finishing-a-development-branch]
```

### Which superpowers skill runs at which gate

Every gate's owning skill is **explicit** — nothing is implied by a `[P]` marker or inferred at
runtime. The two SDD-core skills (`test-driven-development`, `subagent-driven-development`) are
**deliberately absent**: the mode guard proved the batch is non-behavioral, so there is no test-first
cycle and no per-task implement↔review loop to run.

| Gate | Action | Skill ("—" = no skill) | Why this skill / why none |
|---|---|---|---|
| bracket | Preflight & mint `RUN_ID` | `track-preflight.sh` (this skill's bundle) | Durable run identity + prereq gate — a script, not a superpowers skill |
| bracket | Isolate branch/worktree | `using-git-worktrees` | Never start on main; one branch, one worktree |
| bracket | Governance | — (in-session read) | Constitution + matched instructions, distilled and persisted before any brief is built |
| mode guard | Eligibility | — (local refusal guard) | All-or-nothing non-behavioral assertion; routes to story mode on any hit |
| generate | Fan-out generation | `dispatching-parallel-agents` | One subagent per independent domain / disjoint-file cluster returns its file bodies in parallel — safe because nothing writes |
| apply | Apply bodies | — (controller = single writer) | Collapses N proposals into one tree; serial application, no skill |
| materialize | Run the pinned resolvers | — (controller, Bash only) | Lockfile hashes are tool-determined, not authorable — no subagent can return a valid `go.sum`/`uv.lock` |
| review gate | Whole-diff review | `requesting-code-review` | "Is it correct" proof — quality + governance rubric (constitution hard gate + matched `.github/instructions/*`; no security add-on — guard cleared trust boundaries) |
| RESOLVE / probe | Ask the installed tools what they are | `dispatching-parallel-agents` (optional) | Read-only, bulk-noise-in / one-table-out — the one part of RESOLVE that is pure fact-gathering, so it is the one part that delegates |
| RESOLVE / pin | Decide versions before generating | — (serial, one confirm) | Every version decided once, appended to the bundle and re-pinned — so a version surprise is not re-discovered N times as a post-hoc deviation |
| convergence gate | Batch evidence | `verification-before-completion` | "Does it work" proof — real build/lint/bring-up output, not assertion |
| bracket | Draft-PR finish | **overrides** `finishing-a-development-branch` | Worker stops at a draft PR; merge is owned by repo/CI |

**Review and verification are orthogonal and both mandatory.** `requesting-code-review` answers *is
the diff correct and well-formed*; `verification-before-completion` answers *does the scaffold
actually build and come up*. Neither substitutes for the other — a scaffold can build cleanly yet be
wrong, or read well yet never come up. Scaffold mode drops TDD and the two-stage loop, but it
**never** drops either of these two.

**Review comes FIRST, then verification** — the same order as the universal bracket, and for the
same reason: the convergence gate requires every evidence kind to be captured against **one final
tree**, so any edit after it (including a review-driven fix) invalidates the whole capture and forces
a re-run. Verifying before reviewing guarantees you pay that cost on every review finding. Freeze,
then capture.

### RESOLVE — decide every version ONCE, before any subagent runs

A scaffold's job is to materialize a toolchain, and a toolchain has versions. Decide them here,
in one serial step with one human confirm, or you will decide them N times downstream — each time
as a *deviation* discovered after generation, costing a review round and a re-run of the whole
evidence set. On a real Phase-1 run this was eleven deviations; almost all were avoidable here.

They come in three kinds, and only the first is a judgement call:

1. **Task text vs. governance.** A task says "ruff + black"; the repo's `python.instructions.md`
   mandates Ruff only. Governance is more specific and wins — resolve it at the **governance gate**
   (that is what it is for) and record the resolution as a bundle line, so every maker brief carries
   the decision instead of re-deriving it.
2. **Task text vs. reality.** A task names `.eslintrc.cjs`; the current generator emits flat config,
   or oxlint. A task pins Go 1.23; `go get` on the declared deps raises the directive to 1.25. A
   config schema changed major version (`golangci-lint` v2 split `linters:`/`formatters:`, `gosimple`
   folded into `staticcheck`). **Nothing in any document can tell you these — only the installed tool
   can.** So ask it, before generating: `<tool> --version`, `<tool> config verify`, `npm view <pkg>
   version`, `go list -m -versions`. One cheap probe per tool beats one review round per surprise.
3. **Registry drift.** `npm create vite@latest`, `uv add <pkg>`, unpinned `@latest` anything: the tree
   gets whatever the registry served that minute, so the same task run twice produces two different
   trees and neither is reproducible. The governance bundle almost certainly already bans this for
   build artifacts (`no :latest` / "pin versions"); **the generator is a build artifact too.**

Produce a short table and append it to `runs/<RUN_ID>.governance.md` under `## Resolved toolchain`,
then **re-pin the bundle** (`track-note.sh governance <path>`). No new machinery: it now travels into
every maker brief exactly like the rest of the bundle, `G1` re-hashes it, and a compacted session
re-reads it from disk.

| Surface | Generator + version (**decided here, run below or not at all**) | Runtime / language | Key deps (exact) | Probed from |
|---|---|---|---|---|
| frontend | `npm create vite@7.1.2` | node 22.11.0 | react 19.2.8, vite 8.2.1 | `npm view … version` |
| backend-go | — | go 1.25.0 (`go get` raises 1.23) | golangci-lint 2.5.0 | `go version`, `golangci-lint --version` |

**RESOLVE decides versions; it does not author files.** The generator column records *which* pinned
tool would produce a surface, established by a cheap `--version` / `npm view` probe. Reading a pinned
generator out of that column and executing the whole bootstrap from it — `go mod init` … `go get` …
`uv add` … `npm create vite` — is the slide that ends the run before GENERATE ever fans out, and it
happened on a real run: the model read `Generator (pinned): npm create vite@7.1.2`, ran it, kept
going, and made the batch's live authorial decisions (which lint rules, which judge dependencies)
itself. Nothing in this document licenses that. Exactly one thing may be *run* here, under the rules
in the next section.

**Confirm this table with the human before GENERATE**, and say what each pin costs — "the task says
Go 1.23; the declared deps force 1.25, and forcing it back breaks `go list -m all`" is a decision
someone should make knowingly, once, rather than read about in a PR three hours later. Where the
repo has a `skill-deps.json`, a tool that belongs under version-lock goes there so `track-deps.sh`
enforces it at preflight on every later run.

**What this does NOT do:** it cannot pre-empt a genuine discovery (a peer-dependency conflict that
only appears at install). Those remain deviations — and that is fine. The goal is that the
deviations you report are the *novel* ones, not the ones a `--version` call would have told you.

#### PROBE is delegable; PIN is not

RESOLVE is two jobs wearing one name, and they have opposite delegation rules — the same trap the
[GENERATE boundary](#generate--parallel-generation-is-safe-because-nothing-writes) sets one step
later, so decide it deliberately rather than by momentum.

- **PROBE — fact-gathering. Delegate it.** `go version`, `golangci-lint --version`, `npm view <pkg>
  version`, `uv python list --all-versions`, `<tool> config verify`. This is read-only, needs no
  judgement, and its *inputs are bulk noise the controller should never hold* — a `uv python list`
  or an `nvm install` dumps hundreds of lines to establish one number. One subagent per surface
  (go / python / node), dispatched in parallel via `dispatching-parallel-agents`, is the same shape
  as the GENERATE fan-out and safe for the same reason: nothing writes. On the observed run this was
  the single largest avoidable context sink outside governance.
- **PIN — judgement. Keep it in your own session.** Which version wins when `plan.md` pins
  `requires-python = ">=3.13.15"` and that patch does not exist; whether a task's `.eslintrc.cjs` or
  the generator's actual `.oxlintrc.json` is authoritative; what each pin costs. These are the
  `## Conflicts` lines that travel into every brief, and they are non-delegable for exactly the
  reason the governance gate is: a subagent's reasoning dies at the process boundary, and what
  survives is a number with no argument attached.

**A probe subagent's brief must declare `GOVERNANCE: n/a — read-only toolchain probe, returns
versions only, writes nothing`** — it carries no maker constraints, and the declaration is what keeps
`G6` from reading it as a filename-passing brief. That declaration is also load-bearing in the other
direction: `track-guard.sh` and `track-audit.sh`'s `C2` **do not count an `n/a` dispatch as the
GENERATE fan-out**, so probing does not buy you out of the gate one dispatch early.

**Return raw output, not a summary.** Each probe comes back as the command and its verbatim answer
(`$ go version` → `go version go1.25.4 darwin/arm64`), not "Go is 1.25". A summarized version number
is an assertion, and pins built on assertions are the deviations RESOLVE exists to prevent. The
compact table is *yours* to build from those lines, in the bundle, once.

**A probe never installs.** `nvm install 22`, `uv python install 3.13.13`, a `GOTOOLCHAIN=auto` fetch:
these mutate the developer's machine outside the repo, and they belong **after** the human confirm,
run by the controller — a scaffold run should not silently put a new Node on someone's laptop. A
probe that finds a required version missing reports it as a *required action* ("node 22.x not
installed; `nvm install 22` would add it") and stops. Two further reasons this is not pedantry:
shell activation does not cross a process boundary at all (`nvm use 22` inside a subagent is inert
for you — the binary persists, the activation does not), so **whether every later command in this run
needs a `nvm use 22` prefix is itself a probe finding to report**; and an install performed before
the confirm has front-run the one gate that was supposed to authorize it.

### The line: JUDGEMENT is delegated, TOOL-DETERMINED output is not

Every real scaffold hits the same tension, and leaving it unstated is what produced the worst
observed failure of this mode. A read-only subagent returns *text*. But `go.sum`, `uv.lock` and
`package-lock.json` carry cryptographic hashes no model can author, and `tasks.md` itself usually
says to commit them **"as generated, never hand-edited."** So some content genuinely cannot come from
a maker — and a controller that notices this, with no rule to point at, generalizes from "lockfiles
must come from the real tool" to "so I'll just do all of it myself," and dresses the result up as
compliant. Both halves of that are separable. Separate them:

| | Who produces it | With what | Examples |
|---|---|---|---|
| **Took a decision** | a **maker subagent**, always | returns text; controller applies with Write/Edit | the dependency *list*, ruff rule selection, `.golangci.yml`, compose services, CI jobs, `Makefile` targets, every override layered on a template |
| **A pinned tool decides** | the **controller**, always | **Bash only**, never typed by hand | `go.sum`, `uv.lock`, `package-lock.json`, a generator's own untouched template files |

Two consequences, and they are the whole rule:

- **If you are about to use Write/Edit on a deliverable and no subagent has returned a body yet, you
  are violating the mode.** That is now mechanical: `track-guard.sh` denies the write while the run
  record shows scaffold mode with zero dispatches, and `track-audit.sh`'s `C2` fails a scaffold run
  that produced a diff with none. Bash is untouched — the guard is drawing exactly the line in this
  table, not blocking work.
- **"Only a tool can produce it" is never a reason to also decide it.** `uv.lock` must come from
  `uv lock`; *which packages it locks* is a decision, and that decision belongs in a maker's brief.
  A controller that picks the dependency set and then points at the lockfile as justification has
  used a real constraint to launder an unrelated deviation.

#### BASELINE (optional, inside RESOLVE) — a generator's tree is INPUT, never the deliverable

Where a pinned generator owns a surface (`npm create vite@<pinned>`, `go mod init`, `uv init`),
run it **here, in Bash**, before the fan-out, and treat what it lays down as *material the makers
build on*: record the resulting file inventory and the defaults that surprised you (the template
ships `.oxlintrc.json`, not ESLint; `go mod init` wrote a `go` directive one minor above the pin)
into the bundle's `## Resolved toolchain` section. Every cluster brief then carries the real
baseline instead of the maker guessing at it.

It is **input, not output.** The baseline is not the scaffold, and laying it down is not GENERATE
having happened. Everything on top of it — every pin override, every config the batch actually names
— still comes back from a maker.

#### MATERIALIZE (after APPLY) — resolvers run over maker-authored manifests

Once the applied tree is converged, the controller runs the pinned resolvers over it, in Bash, once:
`go mod tidy`, `uv lock` / `uv sync`, `npm install`. This is a mechanical pass over text the makers
authored — it turns a declared dependency set into real hashes and nothing else. Keep the output out
of context (`npm install > /tmp/npm.log 2>&1 || tail -50 /tmp/npm.log`); the lockfiles are the
artifact, not the log. If a resolver reports a conflict, that is a genuine deviation: fix it by
re-briefing the owning cluster's maker, not by hand-editing the manifest it authored.

### GENERATE — parallel generation is safe because nothing writes

> **Reset your mode at this boundary.** The step immediately before this one — the governance gate —
> is explicitly **non-delegable**: it must happen in your own session, and you will have just spent
> many turns correctly doing everything yourself. GENERATE is the exact opposite and the switch is
> not automatic. On a real run it wasn't made: the controller carried "I do this myself" straight
> through RESOLVE into raw Bash execution and **never invoked the dispatch tool at GENERATE at all**,
> while its own TODO list said *"fan out 5 disjoint-file cluster subagents."* Writing the correct
> plan down is not the same as using it as a checkpoint. Before the first deliverable write, assert
> out loud: **N cluster briefs dispatched, N sets of file bodies returned.** If N is zero, you are
> not at APPLY.

The fan-out subagents are **read-only**: each receives its cluster's task text + the relevant
design-doc context and **returns the file body (or bodies) as text**. They do not touch the git index,
do not run tests, do not commit. That is why in-session parallelism is safe here and *not* in story
mode's serial green phase — there is no shared mutable worktree during generation, so none of the
single-index / whole-tree-fingerprint hazards apply. (See the SKILL Gotcha on in-session fan-out.)

**Each subagent's brief must also carry the governance bundle** (see [`governance.md`](governance.md)). Along with the cluster's task text and design-doc context, embed: (a) the
relevant **constitution** principles (`.specify/memory/constitution.md`, if present) — e.g. the
kernel-cannot-import-product rule for a Go cluster; (b) the `.github/instructions/*` that match the
files the cluster will produce — `go` for a Go cluster, `reactjs`/`state-management` for a frontend
cluster, `python` for a Python cluster; and (c) `security-and-owasp.instructions.md` for any cluster
touching a deploy/secrets/network surface (`docker-compose.yml`, a proxy config, a `.env` template).
(d) **For frontend clusters** (any cluster producing `.tsx`/`.ts`/`.jsx`/`.css` files): also embed the
relevant design artefacts collected during governance discovery — the matching
`.stitch/designs/<page>.html` mock and/or the `design-system/` page spec — if those files exist.
Pass silently if absent. Without these, the subagent generates UI from inference rather than the
approved design, requiring a separate alignment pass.
Tell the maker in-brief that these are **binding**: the config it returns must *already* satisfy them
— pinned image tags (no `:latest`), no committed default credentials (env placeholders with a dev
fallback), security headers on public-facing proxies, strict type/lint settings, coverage floors that
match the constitution. Governance discovered only at the review gate is a bug you paid a round-trip for
— it is the exact failure mode that ships hardcoded `POSTGRES_PASSWORD` in a bootstrap PR.

**Use the bundle's `## Cluster → binding sections` map to pick (a)–(d) per cluster.** Because a
scaffold batch fans out to several clusters at once, the governance bundle should already carry that
map (see [`governance.md`](governance.md) — "Pre-slice governance to the clusters that will consume
it"), so each cluster's brief embeds exactly the sections its row names and nothing else. The map is
the routing table; embed the *content* of the sections it points at, never the filename or the bare
row.

**The fan-out unit is an independent domain (a disjoint-file cluster) — NOT one-per-file, and NOT
one-per-task.** This is the same rule `dispatching-parallel-agents` states: *one agent per independent
problem domain*, not per file. Two facts force this:

- **A file may be written by more than one task.** In a real Setup batch, one manifest often satisfies
  two `[P]` tasks — e.g. a Python `pyproject.toml` holds both the *dependencies* task and the
  *ruff/black lint config* task. "One agent per file" is undefined here (two tasks, one file) and
  "one agent per task" is a race (two agents writing the same path). Both tasks belong to **one**
  agent that owns that file, so the file stays internally consistent.
- **A task may span several files.** A test-harness task can create Go, Python, and Playwright fixtures
  at once; the frontend cluster owns `package.json` + `eslint`/`prettier` + `tsconfig` + `vite.config`
  together. Splitting these across agents fragments a coherent config.

So group the batch into **disjoint-file clusters** (natural seams: per-runtime, per-tool-surface,
per-deploy-area), give each cluster to one subagent, and guarantee **no two agents share a target
file**. `[P]` tells you tasks *can* run concurrently; the clustering tells you *how to slice the
agents* without two of them racing the same path. If you cannot cleanly partition the files, the tasks
are not disjoint and must not fan out.

### APPLY — the controller is the only writer

The controller applies every returned body in one pass. Single writer ⇒ no `.git/index.lock` race,
deterministic tree. This is the moment the N parallel proposals collapse into **one** tree state.

"The only writer" also means the controller is **only** a writer, never the generator. If you find
yourself composing file contents from your own reasoning and saving them directly — skipping the GENERATE gate's
read-only subagents because the files are "trivial config" or a subagent-per-file feels heavyweight —
you have collapsed generate and apply into one role and **dropped the fan-out**. The delegation is the
discipline, not an optimization to trade away: generation is delegated to the subagents, application
is the controller's sole job. A converged tree that the controller authored itself is a scaffold-mode
violation even though it "looks the same."

**And do not narrate the deviation into the bundle.** The observed run wrote `(real, controller-run)`
into its own `## Resolved toolchain` table, as if that were sanctioned terminology — inventing a
framing that made a skipped fan-out read like a designed part of the process. There are exactly two
sanctioned controller-run steps, BASELINE and MATERIALIZE, both Bash-only and both named above; if
what you are about to record is neither, the honest record is a deviation, and the honest move is to
stop and dispatch. A genuine ambiguity here is a reason to **ask**, never a licence to pick a third
option and document it as policy.

### APPLY (scope rule) — generate ONLY the task-declared surface, no speculative structure

Both the generating subagents and the applying controller are bounded by the **files and directories the
batched tasks explicitly name** — nothing more. A scaffold task that says *"create
`backend-go/{cmd/api,kernel,internal,migrations,tests}`"* declares **those** directories; it does **not**
license pre-creating the entire downstream architecture (every future `internal/<domain>/{dto,errors,
infra,model,service}`, every kernel port, every `cmd/<x>`) that later stages' tasks will introduce.
Materializing that speculative tree — typically as a blast of one `.gitkeep` per anticipated leaf —
is a scope breach: it drags dozens of empty directories for **unreached tasks** into a bootstrap PR,
front-runs design decisions that belong to those later tasks, and buries the real scaffold in noise.

Two rules keep the batch contained:

- **Subagents (generate):** return only the files each batched task's text names. Do not invent directories
  for tasks outside the batch, and do not "round up" a named parent (`internal/`) to its imagined
  children. If a task genuinely needs an *empty* directory to exist (Git cannot track an empty dir),
  represent it with **exactly one** `.gitkeep` **in that task-named directory only** — never a recursive
  spray across a tree the task did not enumerate.
- **Controller (apply):** before applying, diff the returned path set against the batch's declared
  surface. **Reject or trim any path outside it** — an out-of-scope path is a generation error, not a
  head start. A `.gitkeep` count that dwarfs the number of directories the tasks actually name is the
  tell-tale sign the fan-out over-reached; trim back to the declared surface before committing.

Rule of thumb: the scaffold PR should contain the batch's real files plus the *minimum* set of empty
directories those tasks name — not a materialized map of the whole future codebase.

### CONVERGENCE GATE — batch evidence via `verification-before-completion` (do NOT skip)

Scaffold mode drops per-task TDD and per-task review, but it **keeps one `verification-before-completion`
capture**. Evidence here is not a TDD artifact — it is the "does this actually work" proof, and it is
cheap. Without it you can open a PR where a manifest won't resolve, a compose file won't parse, or the
stack won't come up, and **nobody noticed** because the only check was an LLM reading its own output.
This gate is orthogonal to the review gate — see [the map above](#which-superpowers-skill-runs-at-which-gate):
verification proves the scaffold *works*, review proves it is *correct*, and neither is optional.

The scaffold's Definition of Done is the plan's own **bootstrap checkpoint** — typically some form of
*"all runtimes build; the infra stack comes up."* Realize it as one command set against the converged
tree, then paste real output:

```
build all runtimes  +  lint  +  bring the stack up (health check)  →  paste output  →  then PR
```

Mechanically this reuses the existing evidence gate exactly once over the whole batch — the
whole-tree fingerprint is *happy* here because there is a single converged tree, one evidence pack,
one commit. (Contrast story mode, where per-increment captures must each converge on the final tree
at freeze & verify-all.)

**Never edit the deliverable to make the gate green.** A scaffold creates directories that are
still empty by design, so a batch verify routinely meets tools that cannot run yet: `go vet ./...`
and `golangci-lint run ./...` fail on a module with zero `.go` files; a test target finds no tests.
The gate is reporting the truth — the surface is empty — and the wrong repair is to change the
*product* (a skip-guard bolted into the Makefile, a condition whose only reader is this gate) so a
red command turns green. That ships logic nobody asked for, written for the gate rather than the
user, and it is how a target ends up silently exiting 0 for work it did not do.

Do this instead, in order of preference:

1. **Verify what exists.** For an empty surface, the honest check is that the *configuration* is
   valid, not that a suite passed: `golangci-lint config verify`, `docker compose config`,
   `go mod verify`, `npm run build`. Those run fine on an empty module and prove the scaffold works.
2. **Record the gap as evidence, not as code.** Capture the kind as `n/a — no sources yet` with the
   command and its actual output. An evidence pack that says "this cannot run until Phase 2 lands
   code" is *more* informative than one showing a fabricated pass.
3. **Only then, if a guard genuinely belongs in the deliverable** — because the repo's CI already
   uses the same detect-and-skip pattern, say — write it so it **skips loudly** (prints what it
   skipped and why) or **fails loudly** (a missing tool is an error with an install hint), never so
   it returns 0 in silence. And record it as a deviation with its reason, because you have just made
   a design decision on the user's behalf.

**Keep build noise out of the context** — the local case of the standing rule in
[`context-budget.md`](context-budget.md), which covers the rest of the run. `npm install`, `uv sync`,
`docker compose up` and friends
emit thousands of lines that prove nothing and are re-read on every subsequent turn. Redirect them
and read the verdict: `npm install > /tmp/npm.log 2>&1 || tail -50 /tmp/npm.log`. Keep full output
for the commands that *are* the evidence (build, lint, test, health check) — but a `tail -50` of a
real run still satisfies the evidence gate and stays well clear of `E2`'s 40-character floor on
passing captures. This is a token decision, never an evidence one: if you cannot see the verdict,
you have not verified it.

### REVIEW GATE — one review, not two-stage

A single `requesting-code-review` pass over the entire scaffold diff replaces SDD's per-task
stage-1 (spec) + stage-2 (quality) loop. The rubric is **quality + governance** (project
constitution as a hard gate; matched `.github/instructions/*` applied to the diff). The security
add-on that story mode requires does **not** apply — the guard already established there is no
trust-boundary surface in the batch. (If a task *did* touch a trust boundary, the guard would have
refused the batch.)

## What scaffold mode drops vs. keeps

| Aspect | Story core | Scaffold core |
|---|---|---|
| Execution | Serial: RED batch → incremental green | **Parallel generate (one agent per disjoint-file cluster)** → serial apply/land |
| TDD (test-first) | Required — story-scoped RED batch | **Dropped** — nothing behavioral to test |
| Review | RED review + per-increment spec/quality (+ security) | **One** whole-diff `requesting-code-review` (quality + governance; no security add-on) |
| Evidence | Whole story suite, converged at freeze & verify-all | **One** batch build/lint/bring-up capture — **kept** |
| Commit | One per increment | One (or few) for the batch |
| Preflight / isolation / run-log / hooks / draft-PR | — | **Identical (reused)** |

## When to use / when to refuse

**Use** for: project skeletons, dependency manifests, lint/format configs, compose/proxy/`Makefile`
files, CI wiring, test-harness bootstrap — the pure-config slices of a "Setup" stage and nothing
else.

**Refuse** (route to **story mode**) the moment a batch contains: any test obligation, any migration
with RLS/policy logic, any auth/secrets/DB/network handling, any access-control or correctness
success-criterion. Foundational and user-story stages are almost entirely behavioral — treat them as
**story mode** by default.

## Composition

Scaffold mode is still **one branch, one worktree**. It is *not* a substitute for
`sso-executing-parallel-tracks` (worktree-per-track) — its parallelism is confined to the read-only
generation phase and its landing is serial. A parallel orchestrator may still dispatch one
scaffold-mode run as a track's bootstrap step, then fan out behavioral tracks via story mode.
