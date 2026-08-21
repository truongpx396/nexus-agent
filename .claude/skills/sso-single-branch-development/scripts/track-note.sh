#!/usr/bin/env bash
# track-note.sh — SELF-REPORTED run annotations. NOT a hook, NOT mechanically observed.
#
# The meter/trace/evidence hooks only record what a PostToolUse/Subagent hook can
# actually see (tool-call count, subagent spawns, test output). Several things the model
# knows but no hook can observe are (1) which skill it is currently executing, (2) how
# many implement→review loops it has run, (3) WHERE IN THE PIPELINE the run currently
# is, (4) that the run has reached a non-success terminal state, and (5) where the
# persisted governance bundle lives. This CLI lets the skill *assert* those into the run
# record — so they are the model's own claim, not a verified fact.
#
# To keep that honest, everything written here is provenance-tagged:
#   - skills[] entries carry  self_reported:true
#   - the loop counter lives in  iterations  and a mirror  iterations_self_reported:true
#     flag is set the first time it is touched, so a reader can never mistake either
#     array/scalar for hook-observed truth.
#   - phase / status / governance_bundle each carry  self_reported:true  inside the object.
#
# WHY phase/status/governance EXIST (the compaction problem)
#   A long run gets its context compacted mid-flight. Compaction is NOT a new session, so
#   SessionStart (and therefore track-reconcile.sh) does not fire, and the model silently
#   loses: which execution core it chose, whether the RED suite is frozen, which increment
#   it was on, and the governance excerpts it must embed in every subagent brief.
#   Evidence freshness cannot recover any of that — it only says which test kinds are
#   current. These three subcommands put that position in a FILE, so a compacted (or
#   crashed) session re-anchors from durable state instead of re-reading the worktree,
#   which the skill's own resume invariant forbids.
#
# Wire it from the SKILL prompt (not track-hooks.json): call `note skill …` at the top
# of each core step, `note loop …` once per RED→GREEN→review cycle, `note phase …` at
# EVERY pipeline-step boundary (mandatory — it is the resume anchor), `note governance …`
# once the bundle is persisted, and `note status …` on any non-success terminal state.
#
# Usage (no-op unless RUN_ID is set):
#   track-note.sh skill <name> [step]        append {t, skill, step, self_reported:true} to skills[]
#   track-note.sh loop  [phase]              iterations += 1  (+ optional phase label on the mark)
#   track-note.sh phase <mode> <step>        SET phase={mode,step,t} (overwritten) + append phase_log[]
#   track-note.sh govpath [--staged]         PRINT the anchored path the governance bundle must be
#                                            written to, creating the records dir first. Call it
#                                            BEFORE writing the bundle, and write to exactly what it
#                                            prints — see the subcommand for the two failures it removes.
#                                            --staged prints a path inside the CURRENT worktree
#                                            instead, for a surface that confines its file-writing
#                                            tools to the worktree (a native/sandboxed worktree tool)
#                                            and so cannot author the anchored path at all. Pin the
#                                            staged path and `governance` promotes it across.
#   track-note.sh evidence-na <kind> <why>   DECLARE that a required evidence kind verifies nothing on
#                                            this tree (an empty Go module, a suite that does not exist
#                                            yet). Clears the gate's vacuity block for that kind, on
#                                            record and with a reason — silence never does
#   track-note.sh governance <file>          SET governance_bundle={path,sha,t} — the persisted bundle
#                                            AND append the same to governance_stamps[] (the pin
#                                            HISTORY, so a legitimate mid-core re-pin is on record).
#                                            A staged copy of THIS run's bundle is promoted into the
#                                            anchored records dir first, and the pin names the
#                                            promoted file — the bundle keeps exactly one home.
#   track-note.sh status <state> [blocker] [next_step]
#                                            SET status (+ blocker/next_step). state must be one of
#                                            success | blocked | no-progress | budget-exceeded
#   track-note.sh workaround <what> <why>    APPEND {t, what, why, self_reported:true} to
#                                            workarounds[] — call whenever a rule this skill
#                                            enforces had to be routed around, even via the
#                                            sanctioned escape hatch. Pairs with track-guard.sh's
#                                            hook-observed denials[]: one records WHAT was
#                                            refused, this records WHY and what the run did
#                                            about it. track-report.sh renders both in the PR
#                                            body so a reviewer can see where the skill itself
#                                            created friction, not just what the model claims.
#
# Opt-in via env:
#   RUN_ID    stable run-id for this worker  (REQUIRED — no-op when unset)
#   RUNS_DIR  where run records live (default: runs)
set -eufo pipefail

# Bootstrap: load hook presets sitting beside this script, if present (same contract as
# the hooks: local worktree overrides win over repo base, and an exported value wins
# over both via ${VAR:-default}). No-op when a file is absent.
__env_dir="${BASH_SOURCE[0]%/*}"
# Prefer the MAIN checkout's .github/hooks (canonical) when installed, so a hook
# firing from a linked worktree sources the SAME per-run env + RUN_ID block the
# main-checkout preflight wrote — not an absent worktree-local copy (which would
# leave the guard with empty scope and deny every worktree write). git-common-dir
# resolves to the main repo's .git from any worktree; its parent is the main root.
__gcd="$(git rev-parse --git-common-dir 2>/dev/null || true)"
if [ -n "$__gcd" ]; then
  case "$__gcd" in /*) ;; *) __gcd="$PWD/$__gcd" ;; esac
  __main_root="$(cd "$__gcd/.." 2>/dev/null && pwd || true)"
  if [ -n "$__main_root" ] && [ -d "$__main_root/.github/hooks" ]; then __env_dir="$__main_root/.github/hooks"; fi
  unset __main_root
fi
unset __gcd
if [ -f "$__env_dir/track-env.sh" ]; then . "$__env_dir/track-env.sh"; fi
if [ -f "$__env_dir/track-env.base.sh" ]; then . "$__env_dir/track-env.base.sh"; fi
unset __env_dir

# Unlike the hooks, this is a CLI the skill calls deliberately — and the two things it
# writes (phase, governance) are the run's resume anchors. A silent no-op here is the
# worst possible failure: the caller believes it stamped the pipeline position, the
# record shows the step was never taken, and nobody finds out until the audit reads the
# gap as a skipped step. Costs nothing to say so; stays exit 0 so an inline call in a
# compound command still behaves as documented.
if [ -z "${RUN_ID:-}" ]; then
  printf '%s\n' \
    "track-note: RUN_ID is not set — this call recorded NOTHING." \
    "  '${1:-<subcommand>}' was a no-op: every subcommand writes into \$RUNS_DIR/\$RUN_ID.json," \
    "  so any phase/governance/status stamp you believe you just made is absent from the record." \
    "  Fix: run track-preflight.sh --persist (it writes the managed RUN_ID block into the" \
    "  installed .github/hooks/track-env.sh that this script sources), or export RUN_ID for" \
    "  this call. If the block IS installed, the run has been retired — check for a" \
    "  success/blocked status or a completed breadcrumb, and whether its branch is still" \
    "  checked out in some worktree of this repo." >&2
  exit 0
fi

sub="${1:-}"
RUNS_DIR="${RUNS_DIR:-runs}"
# Kept before the anchoring below, because `govpath --staged` needs the records dir's
# RELATIVE name to rebuild it inside the CURRENT worktree.
RUNS_DIR_RAW="$RUNS_DIR"
# Anchor a RELATIVE RUNS_DIR to the main working tree so the run record is
# single-homed across the main checkout and any linked worktree — a bare "runs"
# resolves against the process CWD, splitting the record when preflight mints it
# in the main checkout but later hooks fire from a sibling worktree. An absolute
# RUNS_DIR (explicit override, e.g. the test harness) is respected verbatim.
case "$RUNS_DIR" in
  /*) ;;
  *)
    __rgcd="$(git rev-parse --git-common-dir 2>/dev/null || true)"
    if [ -n "$__rgcd" ]; then
      case "$__rgcd" in /*) ;; *) __rgcd="$PWD/$__rgcd" ;; esac
      __rroot="$(cd "$__rgcd/.." 2>/dev/null && pwd || true)"
      if [ -n "$__rroot" ]; then RUNS_DIR="$__rroot/$RUNS_DIR"; fi
      unset __rroot
    fi
    unset __rgcd
    ;;
esac
rec="$RUNS_DIR/$RUN_ID.json"
mkdir -p "$RUNS_DIR"
# Canonical skeleton — identical across track-evidence/-meter/-trace/-note so whichever
# writer fires first stamps the same shape (v = run-record schema version). skills[] /
# iterations are added by the mutation below, never by the skeleton, to preserve that
# byte-for-byte invariant.
[ -f "$rec" ] || printf '{"run_id":"%s","v":1,"trace":[],"evidence":[],"tool_calls":0}\n' "$RUN_ID" >"$rec"

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tmp="$(mktemp)"

case "$sub" in
  skill)
    name="${2:-}"
    [ -n "$name" ] || { printf '%s\n' "track-note: 'skill' needs a name." >&2; exit 2; }
    step="${3:-}"
    # Append the activation + refresh the heartbeat (started_ts once, last_ts every event).
    jq --arg t "$ts" --arg s "$name" --arg st "$step" \
      '.skills = ((.skills // []) + [{t:$t, skill:$s, step:$st, self_reported:true}])
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  loop)
    phase="${2:-}"
    # Increment the loop counter, tag its provenance once, and (if a phase was given)
    # drop a timestamped mark so the loop timeline is reconstructable, not just a total.
    jq --arg t "$ts" --arg p "$phase" \
      '.iterations = ((.iterations // 0) + 1)
       | .iterations_self_reported = true
       | (if $p != "" then .iteration_log = ((.iteration_log // []) + [{t:$t, phase:$p}]) else . end)
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  phase)
    # DURABLE PIPELINE POSITION — the compaction/crash re-anchor. `phase` is a single
    # object that is OVERWRITTEN each call (the answer to "where am I now?"), while
    # phase_log[] keeps the append-only history (the answer to "how did I get here?").
    # Both are needed: reconcile reads the scalar, an auditor reads the log.
    mode="${2:-}"
    step="${3:-}"
    [ -n "$mode" ] && [ -n "$step" ] \
      || { printf '%s\n' "track-note: 'phase' needs <mode> <step> (e.g. story red-review)." >&2; rm -f "$tmp"; exit 2; }
    jq --arg t "$ts" --arg m "$mode" --arg s "$step" \
      '.phase = {mode:$m, step:$s, t:$t, self_reported:true}
       | .phase_log = ((.phase_log // []) + [{t:$t, mode:$m, step:$s}])
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  evidence-na)
    # DECLARE that a required evidence kind cannot verify anything on this tree, and why.
    #
    # The vacuity flag alone cannot settle the gate. A scaffold that creates `backend-go/`
    # with no .go files makes `go build ./...` print "matched no packages" and exit 0: that
    # is the honest state of the world, so failing it would push the run into writing code
    # to satisfy a gate — the "never edit the deliverable to make the gate green" trap. But
    # passing it silently is what a real client PR did, printing `go-build ✅ pass` for a
    # capture that compiled nothing.
    #
    # So it takes the same shape as every other no-op in this bundle (`ABSENT` lines in the
    # governance bundle, `GOVERNANCE: n/a` in a brief): an EXPLICIT declaration clears it,
    # silence does not. One command, on record, with a reason a reviewer can weigh.
    kind="${2:-}"
    why="${3:-}"
    [ -n "$kind" ] && [ -n "$why" ] \
      || { printf '%s\n' "track-note: 'evidence-na' needs <kind> \"<why nothing can be verified yet>\" (e.g. go-build \"Phase 1 scaffold: no .go sources exist yet\")." >&2; rm -f "$tmp"; exit 2; }
    jq --arg t "$ts" --arg k "$kind" --arg w "$why" \
      '.evidence_na = ((.evidence_na // []) + [{t:$t, kind:$k, why:$w, self_reported:true}])
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    printf 'declared: %s verifies nothing on this tree — %s\n' "$kind" "$why"
    ;;
  govpath)
    # WHERE the governance bundle goes — one command, one answer, directory already made.
    # Two observed failures, both from the caller re-deriving this path by hand:
    #
    #   1. The Write hard-failed with nothing written. `runs/` is gitignored, so it exists
    #      only where something created it — which is the MAIN checkout (preflight mints the
    #      breadcrumb there) and never a freshly-added linked worktree. The agent surface's
    #      Write tool does not reliably create missing parent directories, so the skill's
    #      own mandatory step died on a missing dir and the repair was a hand-rolled `mkdir`
    #      the skill never mentions. `mkdir -p "$RUNS_DIR"` above has already run by here.
    #   2. The path drifted on the SECOND write. Deriving it once is easy; deriving it again
    #      later in the run reliably produces a bare `runs/<id>.governance.md`, which from a
    #      worktree resolves to that worktree's private copy — invisible to the main checkout
    #      where the record lives. The usual repair is to keep both and let them diverge.
    #
    # Printing it (rather than having this script write the file) keeps authorship where it
    # belongs: distilling the bundle is the model's judgement, not a script's.
    #
    # `--staged` — THE SANDBOXED-WORKTREE ROUTE. The anchored path lives in the MAIN
    # checkout, and an agent surface that isolates by native worktree tool confines its
    # file-writing tools to the worktree: the anchored Write is refused before any hook
    # sees it. That left an EMPTY intersection with the guard, which denies a bundle
    # written anywhere but the anchored dir — no legal path existed, and an observed run
    # burned its budget discovering four different refusals one at a time (anchored path,
    # scratch dir, dotfile at the worktree root, a chained Bash lookup+cp).
    # So: stage inside the worktree under a name that cannot be mistaken for the bundle
    # itself, then let `governance` PROMOTE it across the boundary — the scripts here are
    # not sandboxed, which is exactly why the record and the evidence captures already
    # work from a linked worktree. The one-home invariant is untouched: what gets pinned
    # is always the anchored copy, and the staged file is removed at promotion.
    if [ "${2:-}" = "--staged" ]; then
      case "$RUNS_DIR_RAW" in
        /*) _stage_dir="$RUNS_DIR" ;;
        *)
          _stage_wt="$(git rev-parse --show-toplevel 2>/dev/null || true)"
          [ -n "$_stage_wt" ] \
            || { printf '%s\n' "track-note: 'govpath --staged' must run inside a git worktree." >&2; rm -f "$tmp"; exit 2; }
          _stage_dir="${_stage_wt%/}/${RUNS_DIR_RAW%/}"
          unset _stage_wt
          ;;
      esac
      mkdir -p "$_stage_dir"
      printf '%s\n' "$_stage_dir/$RUN_ID.governance.staged.md"
      unset _stage_dir
      rm -f "$tmp"
      exit 0
    fi
    printf '%s\n' "$RUNS_DIR/$RUN_ID.governance.md"
    rm -f "$tmp"
    ;;
  governance)
    # Pin the PERSISTED governance bundle so a compacted session can re-read the binding
    # constraints from disk instead of from a context window that no longer holds them.
    # The sha lets a reader tell whether the bundle changed after briefs were built.
    file="${2:-}"
    [ -n "$file" ] || { printf '%s\n' "track-note: 'governance' needs a file path." >&2; rm -f "$tmp"; exit 2; }
    # Resolve to an ABSOLUTE path before recording it. A relative one is read back by
    # whoever asks next — track-audit (G1), track-reconcile, track-compact — each from
    # ITS own CWD, and those disagree the moment the work sits in a linked worktree:
    # `runs/` is gitignored, so a worktree has its own private copy, and a bundle
    # written there while the record lives in the main checkout's runs/ resolves to
    # nothing from the main checkout ("recorded but MISSING from disk"). Prefer the
    # file the caller actually points at; fall back to the same basename under the
    # anchored RUNS_DIR so a path typed from the wrong CWD still finds its bundle.
    if [ ! -f "$file" ] && [ -f "$RUNS_DIR/$(basename "$file")" ]; then
      file="$RUNS_DIR/$(basename "$file")"
    fi
    [ -f "$file" ] || { printf '%s\n' "track-note: governance bundle '${2}' does not exist (looked in \$PWD and $RUNS_DIR) — persist it first." >&2; rm -f "$tmp"; exit 2; }
    # `pwd -P`, not `pwd`: every path this is compared against (RUNS_DIR below, and what
    # `git rev-parse` hands the hooks) is PHYSICAL, while a caller's path is whatever they
    # typed. On any checkout reached through a symlink — /tmp and /var/folders on macOS, a
    # symlinked worktree root anywhere — the logical form makes the file's OWN records dir
    # look foreign: the "outside this run's records dir" warning fires on a correctly placed
    # bundle, and the promotion below tries to copy the file onto itself.
    # Canonicalized AFTER the fallback, never inside one branch of it: the fallback hands
    # back `$RUNS_DIR/<basename>`, and RUNS_DIR is whatever the environment set — which is
    # exactly the logical form that compares unequal to its own physical directory.
    _gov_dir="$(cd "$(dirname "$file")" 2>/dev/null && pwd -P || true)"
    file="${_gov_dir:+$_gov_dir/}$(basename "$file")"
    unset _gov_dir
    # The bundle belongs beside the record it is pinned into. Living elsewhere is not an
    # error — the absolute path above keeps it findable — but it is how a run ends up
    # with two divergent bundles, so say it out loud while there is still one.
    _runs_abs="$(cd "$RUNS_DIR" 2>/dev/null && pwd -P || printf '%s' "$RUNS_DIR")"
    # PROMOTE a staged bundle instead of warning about it. The pin is the moment the run
    # commits to one bundle, so it is also the only safe moment to move it: doing the same
    # thing by hand is the `cp` loop that forked one client run's bundle into two copies
    # pinned at three shas. Deliberately narrow — only this run's own bundle basenames are
    # ever relocated, so pinning some other file still warns and stays where it is.
    _canon="$_runs_abs/$RUN_ID.governance.md"
    _promote=0
    case "${file##*/}" in
      "$RUN_ID.governance.staged.md"|"$RUN_ID.governance.md")
        [ "$file" = "$_canon" ] || _promote=1 ;;
    esac
    if [ "$_promote" -eq 1 ]; then
      # Fail-soft: a promotion that cannot happen must not cost the run its pin. The
      # bundle is still recorded where it lies, and the warning below then applies.
      if mkdir -p "$_runs_abs" 2>/dev/null && cp "$file" "$_canon" 2>/dev/null; then
        rm -f "$file" 2>/dev/null || true
        printf '%s\n' \
          "track-note: promoted the staged bundle into this run's records dir." \
          "  from: $file" \
          "  to:   $_canon" \
          "  The pin names the promoted copy — re-read THAT path after a compaction." >&2
        file="$_canon"
      else
        printf '%s\n' "track-note: could not promote '$file' into $_runs_abs — pinning it where it lies." >&2
      fi
    fi
    unset _canon _promote
    case "$file" in
      "$_runs_abs"/*) ;;
      *) printf '%s\n' \
           "track-note: WARNING — governance bundle is outside this run's records dir." \
           "  bundle: $file" \
           "  runs:   $_runs_abs  (where $RUN_ID.json lives)" \
           "  The pin is absolute so it stays readable, but a bundle under a linked worktree's" \
           "  gitignored runs/ is invisible to the main checkout and easily written twice." \
           "  Fix: write it to \$(track-note.sh govpath) and re-pin that path — or, on a" \
           "  surface that will not let you write outside this worktree, to" \
           "  \$(track-note.sh govpath --staged), which this call promotes across for you." >&2 ;;
    esac
    unset _runs_abs
    sha="$( { if command -v shasum >/dev/null 2>&1; then shasum "$file"; else sha1sum "$file"; fi; } | cut -d' ' -f1)"
    # `governance_bundle` is the CURRENT pin (overwritten); `governance_stamps[]` is the
    # history (append-only). Both exist because a run legitimately re-pins mid-core: when a
    # later cluster drags in an instruction file the first pass did not match, the correct
    # move is re-distil → re-pin, and with only the overwritten field on record that looked
    # identical to "governance was stamped AFTER the first dispatch" — G3 failed the run for
    # doing the right thing. The history lets G3 ask the accurate question instead: was every
    # dispatch preceded by SOME pin?
    jq --arg t "$ts" --arg p "$file" --arg sha "$sha" \
      '.governance_bundle = {path:$p, sha:$sha, t:$t, self_reported:true}
       | .governance_stamps = ((.governance_stamps // [])
           + [{t:$t, path:$p, sha:$sha, self_reported:true}])
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  status)
    # TERMINAL STATE. Blocked/exhausted runs are not successes — naming them in the record
    # is what stops a worker dressing one up as done. Constrained to the same four states
    # sso-executing-parallel-tracks routes on, so a solo run and a fleet worker report alike.
    # NOTE: track-meter.sh (no-progress) and track-tokens.sh (budget-exceeded) also write
    # `status` mechanically; this is the model-asserted path for the states no hook sees.
    state="${2:-}"
    case "$state" in
      success|blocked|no-progress|budget-exceeded) ;;
      *) printf '%s\n' "track-note: 'status' needs one of: success | blocked | no-progress | budget-exceeded (got '${state:-<none>}')." >&2; rm -f "$tmp"; exit 2 ;;
    esac
    blocker="${3:-}"
    next_step="${4:-}"
    jq --arg t "$ts" --arg s "$state" --arg b "$blocker" --arg n "$next_step" \
      '.status = $s
       | .status_self_reported = true
       | (if $b != "" then .blocker = $b else . end)
       | (if $n != "" then .next_step = $n else . end)
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  workaround)
    # THE MODEL-AUTHORED HALF of the friction record. track-guard.sh's denials[] (hook-
    # observed) captures WHAT was refused; it cannot capture the WHY behind the refusal or
    # what the run did instead — that only the model watching itself hit the wall knows. A
    # reviewer reading a PR body with neither has no way to tell "the skill's own mechanism
    # forced a detour" from "everything went cleanly" — which is exactly the gap this whole
    # bundle keeps re-learning the hard way: a guard fires, the model finds SOME way through
    # (sanctioned or not), and nothing downstream of the run ever finds out. Call this
    # whenever a rule this skill enforces (a guard denial, an append-only prefix, the
    # autonomy boundary at `gh pr create`, anything) had to be routed around, EVEN when the
    # route taken was the sanctioned one — a repeatedly-needed escape hatch is itself a
    # signal the default is wrong for this repo.
    what="${2:-}"
    why="${3:-}"
    [ -n "$what" ] && [ -n "$why" ] \
      || { printf '%s\n' "track-note: 'workaround' needs <what happened> \"<why / root cause>\" (e.g. 'hand-cp'd the governance bundle across the worktree boundary' 'track-guard.sh denied a direct Write to the anchored path; no --staged option existed yet')." >&2; rm -f "$tmp"; exit 2; }
    jq --arg t "$ts" --arg w "$what" --arg y "$why" \
      '.workarounds = ((.workarounds // []) + [{t:$t, what:$w, why:$y, self_reported:true}])
       | .started_ts = (.started_ts // $t) | .last_ts = $t' \
      "$rec" >"$tmp" && mv "$tmp" "$rec"
    ;;
  *)
    rm -f "$tmp"
    printf '%s\n' "track-note: unknown subcommand '${sub:-<none>}' (want: skill | loop | phase | evidence-na | govpath | governance | status | workaround)." >&2
    exit 2
    ;;
esac
exit 0
