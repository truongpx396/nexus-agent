# Context Budget — bound every tool result before it lands

**The standing rule for every step of this pipeline, in every mode:** decide what a tool call should
return *before* you make it, and shape the call so that is all it returns. A result you did not need
is not free and does not go away — it is re-read on every subsequent turn for the rest of the run.

This is a **correctness** control here, not a thrift measure. Two reasons, and the first is the one
that ships bugs:

1. **Context pressure is what triggers compaction, and compaction is this pipeline's documented
   silent failure.** Bulk pasted file content is the first thing a compaction drops — which means the
   governance bundle goes first. Afterwards the run keeps dispatching subagents while *believing* it
   is still carrying the constraints. That is the exact degradation
   [`governance.md`](governance.md) exists to prevent, [`track-compact.sh`](hooks.md) records, and
   `I4` fails. Every avoidable token you hold brings that moment forward.
2. **A token in context is paid many times over, not once.** On a real client scaffold run the
   transcript recorded **38,860,060** cache-read tokens against **1,492,020** cache-write — every
   token placed into that context was re-read about **26 times**. That multiplier is what turns "I'll
   just paste the whole file" into a real number: a 400-line file you needed 6 lines of does not cost
   400 lines, it costs roughly 400 × the turns that follow it.

## The rule, per tool

| Tool | Default that wastes | Do this instead |
|---|---|---|
| **Read** | reading a whole file to check one thing | `Grep` for the thing first; `Read` with `offset`/`limit` when you know the region. Read whole files when you genuinely need the whole file — a file you are about to rewrite, a 50-line config. |
| **Grep** | `output_mode: content` on a broad pattern | Start with `files_with_matches`, narrow, *then* pull content. Use `-n`/`-C` deliberately, not reflexively. |
| **Glob / ls** | listing a tree to find one path | Name the pattern you actually want. A directory listing you scroll past is pure carry cost. |
| **Bash — installs, builds, syncs** | `npm install`, `uv sync`, `go mod download`, `docker compose up` straight into context | Redirect and read the verdict: `npm install > /tmp/npm.log 2>&1 \|\| tail -50 /tmp/npm.log`. Thousands of lines that prove nothing is the single largest avoidable sink in a scaffold run. |
| **Bash — queries** | `cat file`, `git log`, `git diff` unbounded | Ask the narrow question: `git diff --stat`, `git log --oneline -10`, `grep -c`. `--name-only` before the full diff. |
| **Bash — probes** | `<tool> --version` mixed into a compound command whose output you keep | One probe, one line kept. See scaffold mode's [PROBE](scaffold-mode.md#probe-is-delegable-pin-is-not), which delegates exactly this class. |
| **Subagent dispatch** | an open-ended brief | State the **return contract** — see below. |

Two habits that cut across all of them:

- **Never re-read what you already hold.** The one deliberate exception is the post-compaction
  governance re-read, which is mandatory precisely *because* you no longer hold it.
- **Read once, distil immediately, let the raw text go.** This is the discipline
  [`governance.md`](governance.md#budget-the-read--distil-dont-hoard) already applies to the
  instruction files and the SpecKit slice; it is the same move everywhere else. What you carry
  forward is the extracted constraint, not the document it came from.

## Delegation is the biggest lever — and it only pays if the return is bounded

Dispatching a subagent is the one move that keeps bulk input **entirely** out of your context: the
subagent reads the noise, you receive the conclusion. That is why `dispatching-parallel-agents` fan-out
is scaffold mode's generate step and why RESOLVE's probe is delegable at all.

But the saving is in the **return**, not the dispatch. A maker that returns 2,000 lines of narration
around its file bodies has cost you more than writing the files inline would have. So **every brief
states what comes back**, in one line, as part of the brief.

### The contract bounds the PACKAGING, never the CONTENT

Read this before writing one, because a careless return contract causes a **correctness** bug, not a
style one. The controller applies a returned body **verbatim**, so anything the maker leaves out is
missing from the file on disk — and a truncated file that parses looks exactly like a complete one in
a diff. "Be brief" pointed at a maker is the single most reliable way to produce
`// ... rest of file unchanged ...`.

So the contract cuts **wrapping** — narration, rationale, restated task text, a summary of what the
agent did — and never the artifact:

- **Every file body is complete and literal.** No elision markers (`... existing code ...`,
  `# rest unchanged`, `<!-- snip -->`), no "same as above", no summarizing a region instead of
  writing it. If a file is genuinely long, that is its length; the fix is a narrower cluster, never a
  shorter body. This one is enforced — `track-guard.sh` denies a `Write`/`Edit` whose content carries
  an elision marker, because the controller cannot spot one by eye in a 400-line body it did not write.
- **A blocker or deviation always comes back.** Say so in the brief, or "no commentary" silences the
  one thing you most need to hear: a pin that does not exist, a constraint the maker could not satisfy,
  a conflict between two governance lines. Give it a bounded slot rather than banning it.

```
RETURN, in this order and nothing else:
  1. NOTES — max 5 bullets: blockers, deviations, or constraints you could NOT satisfy.
     Write "NOTES: none" if there are none. Never silently drop one to stay brief.
  2. The file bodies, each a fenced block preceded by its repo-relative path.
     COMPLETE and VERBATIM — never abbreviate, elide, or write "... unchanged ...".
No preamble, no rationale, no summary of what you did.
```
```
RETURN: one markdown table — surface | command | verbatim first line of output — then
        "NOTES:" with anything that needs a decision (e.g. a version that does not exist).
        Nothing else. GOVERNANCE: n/a — read-only toolchain probe, writes nothing.
```

A brief with no return contract gets whatever the subagent felt like writing, and you pay for it at
the same ~26× multiplier as anything else. This is not the same rule as `G6` (which asks whether the
bundle's constraints went *out* in the brief) — this is about what comes *back*, which no hook checks
beyond the elision guard.

## The one thing you never trade away: evidence

**Truncating the output that *is* the proof is not a token decision.** Keep the full output of the
commands the evidence gate records — build, lint, test, health check. `E2` flags passing captures
under 40 characters precisely because a truncated pass-looking string satisfies the gate while proving
nothing, and `track-evidence.sh` marks a capture that verified nothing as `vacuous`.

The distinction is sharp and worth stating plainly:

- `npm install` is **setup**. Its 3,000 lines prove nothing. Redirect it.
- `npm run build` is **evidence**. Its output is the artifact. Keep it.

A `tail -50` of a real test run still clears `E2`'s floor comfortably, so there is room to be
economical — but if you cannot see the verdict in what you kept, you have not verified it, and no
saving justifies that.

## What the hooks do and do not do here

[`track-tokens.sh`](hooks.md) records a cost-weighted estimate at every `Stop` and halts the run
`budget-exceeded` past `TRACK_MAX_TOKEN_ESTIMATE`. Two things follow:

- **It is a detector, not a budget.** It fires at `Stop`, so by the time it speaks the tokens are
  spent. It catches a runaway; it cannot make a run efficient. That part is this document.
- **Read `token_usage`, not just the total.** The breakdown (`input` / `output` / `cache_write` /
  `cache_read`) is written into the run record every turn, and `cache_read` dwarfing everything else
  is the signature of a context that grew too large too early — the shape this reference exists to
  prevent. `token_ceiling` is recorded beside it so a high estimate with no `budget-exceeded` status
  is explainable from the artifact rather than a mystery.

## Checklist

- [ ] Every install/build/sync command redirected, with only its verdict read back
- [ ] Reads scoped — `Grep` before `Read`, `offset`/`limit` when the region is known
- [ ] Nothing re-read that is already in context (post-compaction bundle re-read excepted)
- [ ] Every subagent brief carries a one-line `RETURN:` contract
- [ ] Bulk-input work delegated where a subagent can return the conclusion instead
- [ ] Full output kept for every command that *is* evidence — never truncated to save tokens
