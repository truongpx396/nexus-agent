#!/usr/bin/env bash
# track-guard.sh — PreToolUse guard for the sso-executing-parallel-tracks skill.
#
# Makes two of the skill's gates MECHANICAL instead of prompt-trusted:
#   1. Deny-by-default file ownership (per worktree) + frozen entrypoints.
#   2. Worker push/merge lockout — workers stop at `gh pr create --draft`.
#
# Wiring: copy this file + the bundled track-hooks.json into the repo's
# .github/hooks/ directory (the JSON points VS Code / Copilot CLI / cloud agent
# at this script).
# Requires: jq. Keep runtime < 5s — hooks block the agent synchronously.
#
# Per-worktree scope (export BEFORE launching each worker):
#   TRACK_ALLOWED_PREFIXES  colon-separated path prefixes this track may edit, relative to
#                           the git WORKTREE ROOT (never to the hook's CWD), e.g.
#                           "internal/ingest:migrations/0007_:test/ingest".
#                           A path outside every worktree stays absolute, so allowing a
#                           scratch dir means listing it absolutely, e.g. "/tmp/scratch/".
#   TRACK_FROZEN_PATHS      colon-separated exact files no track may edit, e.g.
#                           "cmd/main.go:internal/app/app.go"
#   TRACK_IMMUTABLE_PREFIXES  (optional) colon-separated prefixes whose
#                           already-committed files are append-only, e.g.
#                           "migrations/:backend-go/migrations/". A NEW file under
#                           the prefix is fine; editing one with git history is denied.
#
# Always-on (no env needed): any file whose first 3 lines carry a
# "GENERATED — DO NOT EDIT" banner is denied — re-run its generator instead.
#
# Also always-on: a write whose CONTENT carries an elision marker
# ("... existing code ...", "# rest of file unchanged", "<!-- snip -->") is denied.
# Set TRACK_ALLOW_ELISION=1 to permit it (e.g. authoring docs *about* elision).
#
# Opt-in destructive-infra guard (off unless set):
#   TRACK_GUARD_DESTRUCTIVE  set to any value to also deny irreversible data/infra
#                            shell commands (DROP/TRUNCATE, unbounded DELETE, Redis
#                            FLUSHALL/FLUSHDB, NATS stream/consumer teardown,
#                            rm -rf on an absolute/home path). Tune per stack.
#
# Scaffold fan-out gate (ON by default; only ever active on a scaffold-mode run):
#   TRACK_SCAFFOLD_FANOUT_GUARD  set to 0 to disable. While a run's record says
#                            phase.mode=="scaffold" and NO subagent has been dispatched yet,
#                            a Write/Edit to a deliverable path is denied — the controller
#                            would be authoring what GENERATE must delegate. See the block
#                            above the `case` for why this fails open in every unknown case.
#
# Opt-in fast-forward push (off unless set):
#   TRACK_ALLOW_FF_PUSH      set to any value to permit a plain `git push` (e.g. a
#                            PR-rework flow updating an existing PR branch). --force,
#                            --no-verify, gh pr merge, git merge, and reset --hard
#                            stay denied, so only a fast-forward push is allowed.
#
# NOTE: Copilot/VS Code ignores hook "matchers", so under that surface this script
# fires on EVERY tool call and branches on tool_name itself. Claude Code DOES scope
# by matcher (see the .claude/settings.json wiring), but the branching stays so a
# single script serves both surfaces. Tool names / input keys differ across
# surfaces — VS Code: create_file / replace_string_in_file + run_in_terminal,
# camelCase tool_input.filePath; Claude Code: Write / Edit / MultiEdit + Bash,
# snake_case file_path / command. All are handled below.
set -eufo pipefail   # -f: no globbing (path prefixes are literal, never patterns)

# Bootstrap: load hook presets sitting beside this script, if present:
#   1. track-env.sh       per-worktree LOCAL overrides (gitignored, optional)
#   2. track-env.base.sh  repo-wide COMMITTED defaults (travels into every worktree)
# Local is sourced first so a worktree value wins over the repo base; every line
# uses ${VAR:-default}, so an already-exported value (e.g. an executing-parallel-
# tracks per-track override) still wins over both. No-op when a file is absent.
__env_dir="${BASH_SOURCE[0]%/*}"
# Prefer the MAIN checkout's .github/hooks (canonical) when installed, so a hook
# firing from a linked worktree sources the SAME per-run env + RUN_ID block the
# main-checkout preflight wrote — not an absent worktree-local copy (which would
# leave the guard with empty scope and deny every worktree write). git-common-dir
# resolves to the main repo's .git from any worktree; its parent is the main root.
__gcd="$(git rev-parse --git-common-dir 2>/dev/null || true)"
TRACK_MAIN_ROOT=""
if [ -n "$__gcd" ]; then
  case "$__gcd" in /*) ;; *) __gcd="$PWD/$__gcd" ;; esac
  TRACK_MAIN_ROOT="$(cd "$__gcd/.." 2>/dev/null && pwd || true)"
  if [ -n "$TRACK_MAIN_ROOT" ] && [ -d "$TRACK_MAIN_ROOT/.github/hooks" ]; then __env_dir="$TRACK_MAIN_ROOT/.github/hooks"; fi
fi
unset __gcd
if [ -f "$__env_dir/track-env.sh" ]; then . "$__env_dir/track-env.sh"; fi
if [ -f "$__env_dir/track-env.base.sh" ]; then . "$__env_dir/track-env.base.sh"; fi
unset __env_dir

# --- per-worktree scope override (the parallel-wave layer) --------------------------
# The bootstrap above resolves ONE env, from the main checkout. That is right for a solo
# run — the session works in a sibling worktree while hooks fire from the main checkout,
# and a worktree-local lookup would find nothing and deny everything. But it collapses the
# layer a WAVE needs: N workers, each owning a disjoint slice of the tree, each requiring a
# DIFFERENT TRACK_ALLOWED_PREFIXES.
#
# Nothing in the process can tell those N apart. `dispatching-parallel-agents` fans out
# in-session subagents, so all of them share one process environment (a subagent cannot set
# env for the hooks that fire on its own tool calls) and one CWD. The only signal that
# distinguishes worker 2 from worker 3 is the TOOL CALL'S OWN TARGET PATH — which resolves
# to a worktree, which can carry its own gitignored track-env.sh. That is what this reads:
# the scope of the tree the write actually lands in, not the scope of the session.
#
# Read in a SUBSHELL with the three vars unset, for two reasons: the file's own
# `${VAR:-default}` / `[ -n ... ] ||` idioms are no-ops against an already-set value (so a
# plain `.` after the bootstrap would change nothing), and sourcing inside the path loop
# would otherwise leak one path's override onto the next. Empty means "not declared here" —
# fall back to the session value rather than fail closed on a file that merely exists.
__wt_cache_root=""; __wt_cache_val=""
_wt_scope() { # _wt_scope <worktree-root> — echoes "allowed<TAB>frozen<TAB>immutable"
  [ -n "${1:-}" ] || return 0
  [ -n "$TRACK_MAIN_ROOT" ] && [ "$1" = "$TRACK_MAIN_ROOT" ] && return 0   # already sourced
  if [ "$1" = "$__wt_cache_root" ]; then printf '%s' "$__wt_cache_val"; return 0; fi
  __wt_cache_root="$1"; __wt_cache_val=""
  if [ -f "$1/.github/hooks/track-env.sh" ]; then
    __wt_cache_val="$(
      unset TRACK_ALLOWED_PREFIXES TRACK_FROZEN_PATHS TRACK_IMMUTABLE_PREFIXES
      . "$1/.github/hooks/track-env.sh" 2>/dev/null || exit 0
      printf '%s\t%s\t%s' "${TRACK_ALLOWED_PREFIXES:-}" "${TRACK_FROZEN_PATHS:-}" "${TRACK_IMMUTABLE_PREFIXES:-}"
    )" || __wt_cache_val=""
  fi
  printf '%s' "$__wt_cache_val"
}

input="$(cat)"
tool="$(jq -r '.tool_name // empty' <<<"$input")"

deny() {
  jq -nc --arg r "$1" '{
    hookSpecificOutput: {
      hookEventName: "PreToolUse",
      permissionDecision: "deny",
      permissionDecisionReason: $r
    }
  }'
  exit 0
}

# Normalize a tool-supplied path to a path relative to the git worktree ROOT it
# belongs to. TRACK_ALLOWED_PREFIXES is written relative to the worktree root, so the
# comparison is only meaningful against a root-relative path — and the hook's $PWD is
# NOT a reliable stand-in for that root. Two ways it drifts, both observed in real runs:
#
#   * a sibling WORKTREE holds the isolated work while the agent (and $PWD) stay rooted
#     in the main checkout, and
#   * a plain `cd` into a subdirectory in an earlier Bash call — after which a $PWD-strip
#     yields "contracts/agent-graph.md" for a file the scope names as
#     "specs/001-x/contracts/agent-graph.md".
#
# Both end the same way: the relativized path never matches a prefix, every scoped write
# is denied (fail-closed), and the pressure is toward ungoverned terminal-heredoc writes.
# So resolve against the worktree root UNCONDITIONALLY — relative inputs included, since
# a bare "contracts/x.md" typed from a subdirectory is exactly the case that used to slip
# through. `git rev-parse --show-prefix` is git's own answer to "where am I inside this
# worktree?", which also sidesteps the string-strip mismatch when a checkout is reached
# through a symlink (/tmp on macOS) and git reports the physical path instead. create_file
# targets — and their parent dirs — may not exist yet, so resolve from the deepest
# EXISTING ancestor and re-attach the not-yet-created tail.
#
# Emits TWO lines: the discovered worktree root (may be empty) and the root-relative path.
# The root has to come back through STDOUT rather than a global, because the caller reads
# this function in a command substitution — a variable it sets dies with that subshell.
# That is why the immutable-prefix check silently fell back to `$PWD` and stopped matching
# whenever the hook's CWD was not the worktree root: exactly the sibling-worktree case the
# root-tracking was added for, so the check quietly passed everything it was meant to stop.
GIT_WT_ROOT=""
_git_relpath() {
  p_in="$1"
  case "$p_in" in
    /*) ;;                                   # absolute → resolve below
    *)  p_in="$PWD/$p_in" ;;                 # relative → make absolute, then resolve alike
  esac
  d="${p_in%/*}"; [ -n "$d" ] || d="/"       # dir part + the tail we must re-attach
  tail="${p_in##*/}"
  while [ ! -d "$d" ] && [ "$d" != "/" ] && [ -n "$d" ]; do
    tail="${d##*/}/$tail"; d="${d%/*}"; [ -n "$d" ] || d="/"
  done
  if pfx="$(git -C "$d" rev-parse --show-prefix 2>/dev/null)"; then
    printf '%s\n%s\n' "$(git -C "$d" rev-parse --show-toplevel 2>/dev/null || true)" "$pfx$tail"
  else
    printf '%s\n%s\n' "" "${p_in#"$PWD"/}"   # outside any worktree → legacy behavior
  fi
}

# --- scaffold mode: the controller APPLIES, it never AUTHORS -------------------------
# Scaffold mode's one structural rule is that generation is delegated: GENERATE fans out a
# read-only subagent per disjoint-file cluster, each RETURNS its file bodies as text, and the
# controller's only job is to write them down. Nothing observed whether that happened, because
# a converged tree the controller authored looks byte-identical to one it applied — and on a
# real run a model that had already read the rule, and could quote it back verbatim, skipped
# the fan-out anyway because the files were "trivial config" and a subagent per cluster felt
# heavyweight. A rule that only the rule-breaker can check is not a gate.
#
# One half of it IS checkable in the moment: whether ANY subagent has run yet. In scaffold
# mode no deliverable write is legitimate before the first dispatch, so that is the test.
#
# What stays allowed, deliberately, because it is NOT authorship:
#   * writes into the run's own RUNS_DIR — the governance bundle and the PR body are the
#     controller's own work product by design (handled by the caller, which skips this check
#     for those paths);
#   * every Bash command. Running a PINNED generator or resolver (`go mod init`, `uv lock`,
#     `npm create vite@8.2.1`, `go mod tidy`, `npm install`) produces tool-determined output,
#     not a judgement call, and it is the only way a real lockfile hash ever gets made. That
#     is exactly the line scaffold-mode.md draws: content that took a decision comes from a
#     maker, content a pinned tool decides comes from the tool.
#
# And it stops after the first dispatch: this catches the STRUCTURAL skip (no fan-out at all),
# never per-file provenance (whether body N came from maker N), which no hook can see and which
# track-audit.sh keeps on its MANUAL list.
#
# FAILS OPEN in every case where the answer is not positively known — no RUN_ID, no run record,
# a mode other than scaffold, or a track-trace.sh that is not wired. That last one matters: with
# no trace hook, trace[] is empty on a perfectly compliant run, and a guard that denied every
# write on that basis would be worse than no guard at all.
# --- elision markers: a truncated body that reads as a complete one -------------------
# The controller APPLIES a maker's returned body verbatim, so anything the maker left out is
# simply missing from the file on disk — and a file that was silently abbreviated still
# parses, still diffs cleanly, and looks complete to a reviewer who was not told to expect a
# gap. Nobody can eyeball this: the controller is writing 400 lines it did not author.
#
# The risk got sharper when this skill started telling briefs to keep returns tight
# (references/context-budget.md). "Be brief" aimed at a maker is the single most reliable way
# to produce `// ... rest of file unchanged ...`, and that instruction now sits directly
# upstream of a verbatim-write step. The doc rule says bodies come back complete; this is the
# half that does not depend on everyone having read it.
#
# DELIBERATELY NARROW — FIVE conditions, because every one of them alone has honest uses and
# a false positive here blocks a legitimate write with no in-bounds alternative. A line is an
# elision marker only when it (a) is SHORT, (b) carries no quote or backtick, (c) opens as a
# comment or a bracketed/leading-dots placeholder, (d) contains an ellipsis, and (e) names the
# elision in words.
#
# (a) and (b) are what separate a marker from a line ABOUT markers, and both were found the
# hard way: this guard's own header comment and context-budget.md both discuss elision by
# quoting it, and an earlier form of this check denied writing either file. The real
# discriminator is that a genuine marker IS the whole line — a mention of one is embedded in a
# sentence, in quotes or backticks, and runs long.
#
# Note single `-` is NOT a comment opener here: it is the markdown/YAML list bullet, and
# treating it as one flagged every prose bullet that mentioned elision. `--` (SQL, Lua) stays.
# Honest uses each condition protects: a bare `...` is Python's Ellipsis and YAML's document
# end; "the rest of the file is unchanged" is a fine English sentence; `{...x}` is a spread;
# "wait for it... then retry" is ordinary punctuation in a comment.
_elision_hit() {  # _elision_hit <content> — echoes the offending line, empty if clean
  [ -n "${1:-}" ] || return 0
  [ -z "${TRACK_ALLOW_ELISION:-}" ] || return 0
  printf '%s\n' "$1" \
    | grep -aE '^.{0,72}$' \
    | grep -av '["'"'"'`]' \
    | grep -aE '^[[:space:]]*(//|/\*|\*|#+|--|;+|%|<!--|\[|\()?[[:space:]]*(\.\.\.|…)|^[[:space:]]*(//|/\*|\*|#+|--|;+|%|<!--)' \
    | grep -aE '(\.\.\.|…)' \
    | grep -aiE '(unchanged|existing code|rest of|remainder|remains? the same|omitted|elided|truncated|snip|as (above|before)|previous (content|code)|no changes? here)' \
    | head -1 || true
}

__sfg_done=0; __sfg_deny=0
_scaffold_fanout_violation() {   # exit 0 = deny this write, 1 = nothing to say
  if [ "$__sfg_done" -eq 0 ]; then
    __sfg_done=1
    [ "${TRACK_SCAFFOLD_FANOUT_GUARD:-1}" != "0" ] || return 1
    [ -n "${RUN_ID:-}" ] || return 1
    _sfg_root="${TRACK_MAIN_ROOT:-$PWD}"
    _sfg_runs="${RUNS_DIR:-runs}"
    case "$_sfg_runs" in /*) ;; *) _sfg_runs="${_sfg_root%/}/${_sfg_runs%/}" ;; esac
    _sfg_rec="$_sfg_runs/$RUN_ID.json"
    [ -f "$_sfg_rec" ] || return 1
    jq -e '(.phase.mode // "") == "scaffold"' "$_sfg_rec" >/dev/null 2>&1 || return 1
    # "Has a GENERATING dispatch happened?" — not "has any subagent run?". RESOLVE may
    # legitimately delegate its toolchain PROBE to a read-only subagent (that is the whole
    # point of the probe: `nvm install`/`npm view`/`uv python list` output is bulk noise the
    # controller should never hold), and such a brief declares `GOVERNANCE: n/a` because it
    # carries no maker constraints. Counting it would hand the run a free pass out of this
    # gate before GENERATE — the exact skip being guarded, one dispatch later.
    #
    # track-brief.sh already records the discriminator (`declared_na`), so prefer briefs[]
    # when it has anything to say and fall back to the weaker trace[] signal when the brief
    # hook is unwired or did not recognise the dispatch tool. Same selector track-audit.sh
    # uses: the kind tag first, the raw event name for records written before that tag.
    jq -e '((.briefs // []) as $b
            | if ($b | length) > 0
              then ([$b[] | select((.declared_na // false) != true)] | length) > 0
              else ([.trace[]? | select(((.kind // "") == "subagent")
                      or (((.event // "") | ascii_downcase) | test("subagent")))] | length) > 0
              end) | not' \
      "$_sfg_rec" >/dev/null 2>&1 || return 1
    # An empty trace[] is only evidence of anything if something was watching.
    _sfg_wired=0
    for _sfg_f in "${_sfg_root%/}/.claude/settings.json" \
                  "${_sfg_root%/}/.github/hooks/track-hooks.json" \
                  "${_sfg_root%/}/.vscode/hooks.json"; do
      if [ -f "$_sfg_f" ] && grep -q 'track-trace' "$_sfg_f" 2>/dev/null; then _sfg_wired=1; break; fi
    done
    [ "$_sfg_wired" -eq 1 ] || return 1
    __sfg_deny=1
  fi
  [ "$__sfg_deny" -eq 1 ]
}

case "$tool" in
  create_file | replace_string_in_file | multi_replace_string_in_file | edit_notebook_file | Write | Edit | MultiEdit | NotebookEdit)
    # Collect every target path this edit touches, across surface variants.
    # NotebookEdit (Claude Code) carries its target as tool_input.notebook_path.
    paths="$(jq -r '
      [ .tool_input.filePath?,
        .tool_input.file_path?,
        .tool_input.notebook_path?,
        (.tool_input.replacements[]?.filePath),
        (.tool_input.edits[]?.file_path) ]
      | map(select(. != null and . != "")) | .[]' <<<"$input")"
    [ -z "$paths" ] && exit 0

    # Content check runs once per call, before the per-path loop: an elision is a property
    # of what is being written, not of where it lands. Every surface's new-text field is
    # collected, so a MultiEdit hunk is covered as well as a whole-file Write.
    _new_text="$(jq -r '
      [ .tool_input.content?,
        .tool_input.new_string?,
        .tool_input.newString?,
        (.tool_input.edits[]?.new_string),
        (.tool_input.replacements[]?.newString) ]
      | map(select(type == "string" and . != "")) | join("\n")' <<<"$input" 2>/dev/null || true)"
    _elided="$(_elision_hit "$_new_text")"
    if [ -n "$_elided" ]; then
      deny "this write contains an ELISION MARKER — '$(printf '%s' "$_elided" | cut -c1-80)'. A body applied verbatim with a placeholder in it writes a TRUNCATED file that still parses and still diffs cleanly, so nothing downstream will catch it. If this came back from a maker subagent, the return contract was not honoured: re-request the file COMPLETE and VERBATIM rather than patching around the gap by hand (see references/context-budget.md). If the marker is genuinely part of the content — documentation about elision, a test fixture — set TRACK_ALLOW_ELISION=1 for this write."
    fi
    unset _new_text _elided

    while IFS= read -r p; do
      [ -z "$p" ] && continue
      # Root + relative path, both read back from stdout (see _git_relpath on why the root
      # cannot be a global). GIT_WT_ROOT then aims the banner + immutable-history checks at
      # the tree the path actually lives in, not at whatever the hook's CWD happens to be.
      { IFS= read -r GIT_WT_ROOT; IFS= read -r rel; } <<<"$(_git_relpath "$p")"

      # Resolve the scope that governs THIS path's tree (see _wt_scope). A worktree that
      # declares nothing inherits the session's — the common case, and a solo run's only case.
      p_allowed="${TRACK_ALLOWED_PREFIXES:-}"
      p_frozen="${TRACK_FROZEN_PATHS:-}"
      p_immutable="${TRACK_IMMUTABLE_PREFIXES:-}"
      p_scoped_by=""
      wt_scope="$(_wt_scope "$GIT_WT_ROOT")"
      if [ -n "$wt_scope" ]; then
        wt_a="${wt_scope%%	*}"; wt_rest="${wt_scope#*	}"
        wt_f="${wt_rest%%	*}"; wt_i="${wt_rest#*	}"
        [ -n "$wt_a" ] && { p_allowed="$wt_a"; p_scoped_by="$GIT_WT_ROOT"; }
        [ -n "$wt_f" ] && p_frozen="$wt_f"
        [ -n "$wt_i" ] && p_immutable="$wt_i"
      fi

      # Frozen entrypoints: never editable by any track (tracks self-register).
      saved_ifs="$IFS"; IFS=:
      for f in ${p_frozen:-}; do
        [ "$rel" = "$f" ] && { IFS="$saved_ifs";
          deny "frozen entrypoint '$rel' — self-register via your track's own file instead of editing the shared entrypoint"; }
      done
      IFS="$saved_ifs"

      # Deny-by-default: the path MUST match an allowed prefix.
      ok=0
      saved_ifs="$IFS"; IFS=:
      for a in ${p_allowed:-}; do
        case "$rel" in "$a"*) ok=1 ;; esac
      done
      IFS="$saved_ifs"

      # The run's OWN bookkeeping directory is always writable, regardless of scope. It
      # holds the governance bundle, the PR body and the run record — artifacts this skill
      # REQUIRES the model to write, which are gitignored and never part of the reviewed
      # diff. Scoping them as if they were deliverables is a category error, and the
      # observed run shows what it costs: `runs/` was absent from the approved prefixes, so
      # the model composed the governance bundle into `backend-go/.gov.tmp2.md` — an
      # in-scope DELIVERABLE path — and shell-`cp`'d it across. The guard pushed a
      # bookkeeping file into the very tree it exists to protect.
      # Resolved as an ABSOLUTE path, never against the hook's CWD. A relative RUNS_DIR
      # ("runs", the shipped default) means <repo-root>/runs — so a `runs/` directory that
      # happens to sit under some subdirectory the hook was invoked from is a DIFFERENT
      # directory and must stay denied. Both roots are checked because the bundle is
      # written to the main checkout while the work lives in a linked worktree.
      # `p_is_runs` also tells the scaffold fan-out check below to leave this path alone:
      # bookkeeping is controller-authored by design, deliverables are not. It is computed
      # unconditionally (not only when the scope check already failed) because a scope that
      # legitimately includes `runs/` would otherwise leave the flag unset and hand the
      # governance bundle to a check that has nothing to say about it.
      p_is_runs=0
      _runs="${RUNS_DIR:-runs}"
      case "$p" in /*) _abs="$p" ;; *) _abs="$PWD/$p" ;; esac
      case "$_runs" in
        /*) case "$_abs" in "${_runs%/}"/*) p_is_runs=1 ;; esac ;;
        *)  for _base in "$TRACK_MAIN_ROOT" "${GIT_WT_ROOT:-}"; do
              [ -n "$_base" ] || continue
              case "$_abs" in "${_base%/}/${_runs%/}"/*) p_is_runs=1 ;; esac
            done ;;
      esac
      unset _runs _abs _base
      if [ "$p_is_runs" -eq 1 ]; then ok=1; fi
      # A path outside EVERY worktree gets its own message. It is not a scope dispute — no
      # track owns it and no prefix can match it, because the scope is repo-relative and
      # `_git_relpath` leaves such a path absolute. The generic "merge conflict at
      # integration" wording named no in-bounds move for it, and the observed failure mode
      # is precisely what that produces: an agent told to stage work in a scratch dir gets
      # every Write denied, then reaches for `cat > … <<EOF` in Bash to route around the
      # guard. Say where the work belongs instead.
      if [ "$ok" -ne 1 ] && [ -z "$GIT_WT_ROOT" ]; then
        deny "'$rel' is outside every git worktree, so no track owns it — this track's scope is repo-relative (${p_allowed:-<empty>}). Write into the worktree under an owned prefix instead of a scratch/temp directory: work written outside the repo never reaches the diff, the evidence gate, or the PR. If a scratch dir is genuinely needed, add it to TRACK_ALLOWED_PREFIXES as an ABSOLUTE path."
      fi
      [ "$ok" -eq 1 ] ||
        deny "'$rel' is outside this track's ownership scope (set TRACK_ALLOWED_PREFIXES); editing it would become a merge conflict at integration${p_scoped_by:+ — scope for this path came from $p_scoped_by/.github/hooks/track-env.sh (that worktree's own track), not the session's}"

      # Generated files are never hand-edited — re-run the generator (always-on).
      # Test the ORIGINAL path ($p), which resolves regardless of $PWD vs worktree.
      if [ -f "$p" ] && head -3 "$p" 2>/dev/null | grep -q "GENERATED — DO NOT EDIT"; then
        deny "'$rel' is generated (carries a 'GENERATED — DO NOT EDIT' banner) — re-run its generator instead of editing it by hand"
      fi

      # Immutable prefixes: an already-committed file (e.g. an applied migration)
      # is append-only. A brand-new file under the prefix is allowed. Query the
      # worktree the path lives in (GIT_WT_ROOT), not $PWD, so a sibling-worktree
      # branch's history is checked — falling back to $PWD when root is unknown.
      saved_ifs="$IFS"; IFS=:
      for m in ${p_immutable:-}; do
        case "$rel" in
          "$m"*)
            if git -C "${GIT_WT_ROOT:-$PWD}" log --oneline -1 -- "$rel" 2>/dev/null | grep -q .; then
              IFS="$saved_ifs"
              deny "'$rel' is an already-committed artifact under an immutable prefix ($m) — create a NEW file instead of editing it"
            fi ;;
        esac
      done
      IFS="$saved_ifs"

      # Scaffold mode: a deliverable written before the first subagent ran is the controller
      # authoring what GENERATE must delegate (see _scaffold_fanout_violation above).
      if [ "$p_is_runs" -eq 0 ] && _scaffold_fanout_violation; then
        deny "scaffold mode: '$rel' would be AUTHORED by the controller — this run's record shows no GENERATING subagent yet, so no maker has returned a body for you to apply. GENERATE fans out one read-only subagent per disjoint-file cluster (dispatching-parallel-agents); each RETURNS its file bodies as text and the controller only writes them down. Dispatch the fan-out, then apply what it returns. Two things do NOT clear this and are not meant to: a RESOLVE toolchain-probe dispatch (its brief declares 'GOVERNANCE: n/a', so it is not counted), and running a PINNED generator or resolver in Bash (go mod init, uv lock, npm create vite@<pinned>, npm install) — the latter stays allowed because it is tool-determined output rather than authorship. That is the line: content that took a decision comes from a maker, content a pinned tool decides comes from the tool. Escape hatch, if this is genuinely not a fan-out step: TRACK_SCAFFOLD_FANOUT_GUARD=0."
      fi
    done <<<"$paths"
    ;;

  run_in_terminal | bash | shell | Bash)
    cmd="$(jq -r '.tool_input.command // .tool_input.bash // empty' <<<"$input")"
    # History rewrites, merges, and gate bypass are ALWAYS denied — even when
    # fast-forward push is opted in below (this catches `git push --force`).
    case "$cmd" in
      *"gh pr merge"* | *"git merge "* | *"git reset --hard"*)
        deny "blocked by autonomy boundary: merging/rewriting history is the merge gate's job (human or merge queue), not the worker's." ;;
    esac
    # `--force` / `--no-verify` are the flags that matter HERE, but neither spelling is
    # git's alone: a raw substring match denies every unrelated tool that happens to take
    # one (`specify integration install claude --force`, `npm ci --force`, `uv pip install
    # --force-reinstall`) and explains itself with a message about rewriting git history
    # that makes no sense for the command being run. That is a false positive with no
    # in-bounds alternative, which is the shape of rule that gets worked around rather
    # than obeyed. Scope the check to the segments that actually invoke git/gh — split on
    # shell separators first, so `foo --force && git push --force` still trips on its
    # SECOND segment and nothing is smuggled through in a compound command.
    while IFS= read -r seg; do
      case "$seg" in
        *"git "* | *"gh "*) ;;
        *) continue ;;
      esac
      case "$seg" in
        *"--force"* | *"--no-verify"*)
          deny "blocked by autonomy boundary: '--force'/'--no-verify' on a git/gh command ('$seg') — merging/rewriting history is the merge gate's job (human or merge queue), not the worker's. Non-git tools that take a --force flag are unaffected." ;;
      esac
    done <<<"$(printf '%s' "$cmd" | tr ';&|\n' '\n\n\n\n')"
    # `git push` lockout — workers normally stop at `gh pr create --draft`. Two
    # carve-outs, and nothing else gets through:
    #
    #   1. TRACK_ALLOW_FF_PUSH — explicit opt-in for a PR-rework flow that updates an
    #      already-published branch. The always-deny block above still bars --force.
    #
    #   2. The FIRST publish of the worker's own branch. `gh pr create` cannot open a PR
    #      for a branch the remote has never seen; non-interactively it fails outright
    #      rather than offering to push. Denying this made the skill's OWN documented
    #      terminal step unreachable, leaving a worker no in-bounds move — on a real run
    #      that pressure produced exactly the predictable outcome: the worker self-granted
    #      TRACK_ALLOW_FF_PUSH, a flag documented for a different purpose, to get unstuck.
    #      A rule with no compliant path does not produce compliance, it produces
    #      workarounds, so the compliant path is now explicit and narrow.
    #
    # The carve-out is deliberately the narrowest thing that reaches `gh pr create`: it
    # publishes ONE branch ONCE. A second push of the same branch is an update and still
    # requires the opt-in, so the rework flag keeps its documented meaning.
    is_first_publish() { # is_first_publish <cmd> — 0 only for a branch-publishing push
      _c="$1"
      # Bulk/destructive push modes are never a publish (--force is denied above).
      case "$_c" in
        *--delete*|*--mirror*|*--all*|*--tags*|*--prune*|*" -d "*) return 1 ;;
      esac
      _wt="${GIT_WT_ROOT:-$PWD}"
      # Tokens after `git push`, flags and blanks dropped: [<remote>] [<refspec>].
      _toks="$(printf '%s' "$_c" | sed -n 's/.*git push//p' | tr ' \t' '\n\n' \
               | grep -v '^-' | grep -v '^$' || true)"
      # The first token is the remote only if git actually knows it as one — otherwise a
      # bare `git push mybranch` would have its BRANCH eaten as a remote name.
      _rem=""
      if [ -n "$_toks" ]; then
        _t1="$(printf '%s\n' "$_toks" | head -1)"
        if git -C "$_wt" remote 2>/dev/null | grep -Fqx "$_t1"; then
          _rem="$_t1"; _toks="$(printf '%s\n' "$_toks" | sed 1d)"
        fi
      fi
      _spec="$(printf '%s\n' "$_toks" | grep -v '^$' | tail -1 || true)"

      # WHICH branch does this push publish? The REFSPEC's answer is authoritative, and is
      # the only one that survives the hook firing from a different checkout than the work.
      # Reading HEAD at the hook's CWD does not: under worktree isolation the guard's CWD is
      # routinely the MAIN checkout, where HEAD is `main` — so `git push -u origin feat-x`
      # was read as an attempt to publish the base branch and denied, making the skill's own
      # documented handoff step (`gh pr create`) unreachable and pressuring the worker into
      # self-granting TRACK_ALLOW_FF_PUSH. Fall back to HEAD only when no refspec names one.
      case "$_spec" in
        "" | HEAD) _src=""; _dst="" ;;
        *:*)       _src="${_spec%%:*}"; _dst="${_spec#*:}" ;;
        *)         _src="$_spec"; _dst="$_spec" ;;
      esac
      case "$_src" in
        "" | HEAD) _src="$(git -C "$_wt" rev-parse --abbrev-ref HEAD 2>/dev/null || echo "")" ;;
      esac
      [ -n "$_dst" ] || _dst="$_src"
      _cur="$_src"
      { [ -n "$_cur" ] && [ "$_cur" != "HEAD" ]; } || return 1
      # No redirection: `HEAD:main` / `feat-x:main` publishes somewhere else, not this branch.
      [ "$_dst" = "$_cur" ] || return 1
      # It must be a real local branch here (worktrees of one repo share refs, so this
      # resolves the sibling worktree's branch from the main checkout too).
      git -C "$_wt" rev-parse --verify --quiet "refs/heads/$_cur" >/dev/null 2>&1 || return 1
      # Never publish the base/default branch — that is the merge gate's ref, not ours.
      _def="${TRACK_DEFAULT_BRANCH:-}"
      [ -n "$_def" ] || { _b="${TRACK_BASE_REF:-}"; _def="${_b##*/}"; }
      [ -n "$_def" ] || _def="main"
      [ "$_cur" != "$_def" ] || return 1
      # A remote named on the command line wins; else the branch's own config; else origin.
      if [ -z "$_rem" ]; then
        _rem="$(git -C "$_wt" config --get "branch.$_cur.remote" 2>/dev/null || echo origin)"
      fi
      [ -n "$_rem" ] || _rem=origin
      # Already on the remote → this is an update, not a first publish. Needs the opt-in.
      ! git -C "$_wt" rev-parse --verify --quiet "refs/remotes/$_rem/$_dst" >/dev/null 2>&1 || return 1
      return 0
    }
    case "$cmd" in
      *"git push"*)
        if [ -n "${TRACK_ALLOW_FF_PUSH:-}" ]; then
          :   # explicit opt-in (PR-rework); --force et al. still denied above
        elif is_first_publish "$cmd"; then
          :   # first publish of this worker's own branch — the path to `gh pr create`
        else
          deny "blocked by autonomy boundary: workers stop at 'gh pr create --draft'. Pushing is the merge gate's job. (Publishing your own branch for the first time is allowed so 'gh pr create' can reach the remote; this push is an update, a different branch, or a bulk/destructive mode. Set TRACK_ALLOW_FF_PUSH=1 for a PR-rework flow that updates an already-published branch.)"
        fi ;;
    esac

    # OPTIONAL destructive-infra guard — irreversible data/infra ops. Off unless
    # TRACK_GUARD_DESTRUCTIVE is set; case-insensitive; tune patterns per stack.
    if [ -n "${TRACK_GUARD_DESTRUCTIVE:-}" ]; then
      # Scan the command's CODE, not the data it merely carries. A heredoc BODY is data:
      # the run that motivated this had a routine
      #   cat > pr-body.md <<'EOF' … "output was truncated to 50 lines" … EOF
      # denied as an "irreversible schema op". That is both wrong and uncompliable — the
      # only way to satisfy the rule was to stop writing the PR body — which is exactly the
      # shape of rule that gets worked around rather than obeyed. So drop heredoc bodies
      # before matching, keeping the command line that opens them.
      scan=""; _delim=""
      while IFS= read -r _l; do
        if [ -n "$_delim" ]; then                  # inside a body: skip to the terminator
          _t="${_l#"${_l%%[![:space:]]*}"}"        # left-trim (covers <<- style indenting)
          [ "$_t" = "$_delim" ] && _delim=""
          continue
        fi
        scan="$scan$_l
"
        case "$_l" in
          *"<<"*)
            _d="${_l##*<<}"; _d="${_d#-}"
            _d="${_d#"${_d%%[![:space:]]*}"}"      # left-trim
            _d="${_d%%[[:space:]]*}"; _d="${_d%%;*}"
            _d="$(printf '%s' "$_d" | tr -d "\"'")"
            case "$_d" in
              "" | *"<"*) ;;                       # `<<<` herestring or malformed: no body
              *) _delim="$_d" ;;
            esac ;;
        esac
      done <<<"$cmd"
      unset _l _t _d _delim

      shopt -s nocasematch
      case "$scan" in
        # `truncate` is NOT matched bare: it is also coreutils (`truncate -s 0 f`) and, far
        # more often, an ordinary English word ("truncated", "truncate the log"). Require
        # the SQL spelling, or a SQL client on the same command line.
        *"drop table"* | *"drop database"* | *"drop schema"* | *"truncate table"*)
          deny "blocked: irreversible schema op in '$cmd'. Express it as a reversible migration, not an ad-hoc DROP/TRUNCATE." ;;
        *truncate*)
          case "$scan" in
            *psql* | *mysql* | *mariadb* | *sqlite3* | *cockroach* | *clickhouse-client* | *mongosh*)
              deny "blocked: irreversible schema op in '$cmd'. Express it as a reversible migration, not an ad-hoc DROP/TRUNCATE." ;;
          esac ;;
      esac
      case "$scan" in
        *flushall* | *flushdb*)
          deny "blocked: Redis FLUSHALL/FLUSHDB wipes shared state. Scope deletions to your own keys instead." ;;
        *"nats stream rm"* | *"nats stream delete"* | *"nats stream purge"* | *"nats consumer rm"* | *"nats consumer delete"*)
          deny "blocked: NATS stream/consumer teardown touches shared infra. Leave topology changes to the platform owner." ;;
        *"rm -rf /"* | *"rm -fr /"* | *"rm -rf ~"* | *"rm -fr ~"*)
          deny "blocked: 'rm -rf' on an absolute or home path. Delete only within the repo/worktree." ;;
      esac
      # Unbounded DELETE (no WHERE) wipes a whole table.
      case "$scan" in
        *"delete from"*)
          case "$scan" in
            *where*) : ;;
            *) deny "blocked: 'DELETE FROM' with no WHERE clause wipes the whole table. Add a WHERE filter." ;;
          esac ;;
      esac
      shopt -u nocasematch
      unset scan
    fi
    ;;
esac

exit 0
