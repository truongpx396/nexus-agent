#!/usr/bin/env bash
# track-skill.sh — PreToolUse (Skill tool): mechanically record skill activations.
#
# WHY THIS EXISTS
#   `skills[]` used to be reachable only via `track-note.sh skill <name>` — a manual,
#   self-reported call the model has to remember to make at the top of every step. On a
#   real run that drove `using-git-worktrees`, `dispatching-parallel-agents`,
#   `subagent-driven-development`, and `test-driven-development` in sequence, `skills[]`
#   recorded exactly ONE entry: whichever call the model happened to remember. The other
#   activations were real — the run's own diff and trace prove they ran — but nothing
#   durable said so. A PreToolUse hook on the Skill tool itself sees every activation
#   whether or not the model narrates it, the same fix track-brief.sh applied to the
#   dispatch hop.
#
# FIELD NAME: this surface's Skill tool call carries the invoked skill's name as
# `tool_input.skill` (confirmed directly from this tool's own definition — not a guess
# the way `agent_description`/`agentName` were for track-trace.sh). A couple of
# plausible alternate spellings are read as a fallback for a differently-shaped surface,
# matching this bundle's standing convention of never hard-coding to one surface's field
# names — but the primary key is a known fact here, not an assumption.
#
# PROVENANCE: `self_reported:false` on every entry this hook writes — the one thing that
# distinguishes it from `track-note.sh skill`'s entries, which stay `self_reported:true`.
# A reader must never have to guess which kind an entry is; track-report.sh's own
# self-reported fencing depends on being able to tell them apart.
#
# Opt-in via env (no-op unless RUN_ID is set):
#   RUN_ID    stable run-id for this worker
#   RUNS_DIR  where run records live (default: runs)
set -eufo pipefail

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
if [ -n "$__gcd" ]; then
  case "$__gcd" in /*) ;; *) __gcd="$PWD/$__gcd" ;; esac
  __main_root="$(cd "$__gcd/.." 2>/dev/null && pwd || true)"
  if [ -n "$__main_root" ] && [ -d "$__main_root/.github/hooks" ]; then __env_dir="$__main_root/.github/hooks"; fi
fi
unset __gcd
if [ -f "$__env_dir/track-env.sh" ]; then . "$__env_dir/track-env.sh"; fi
if [ -f "$__env_dir/track-env.base.sh" ]; then . "$__env_dir/track-env.base.sh"; fi
unset __env_dir

[ -n "${RUN_ID:-}" ] || exit 0

input="$(cat)"
# Copilot fires every hook on every tool call and cannot scope by matcher, so the shape
# test (not just tool_name) is what keeps this cheap on that surface: exit before touching
# the record unless the payload actually looks like a skill invocation.
tool="$(jq -r '.tool_name // empty' <<<"$input")"
case "$tool" in
  Skill | skill | RunSkill | InvokeSkill | UseSkill) ;;
  *) exit 0 ;;
esac

skill_name="$(jq -r '.tool_input.skill // .tool_input.name // .tool_input.skill_name // .tool_input.skillName // empty' <<<"$input")"
[ -n "$skill_name" ] || exit 0
skill_args="$(jq -r '.tool_input.args // empty' <<<"$input")"

RUNS_DIR="${RUNS_DIR:-runs}"
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
# Canonical skeleton — identical across every track-*.sh writer so whichever hook fires
# first stamps the same shape (v = run-record schema version).
[ -f "$rec" ] || printf '{"run_id":"%s","v":1,"trace":[],"evidence":[],"tool_calls":0}\n' "$RUN_ID" >"$rec"

ts="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
tmp="$(mktemp)"
jq --arg t "$ts" --arg s "$skill_name" --arg a "$skill_args" \
  '.skills = ((.skills // []) + [{t:$t, skill:$s, self_reported:false}
     + (if $a != "" then {args:($a[0:200])} else {} end)])
   | .started_ts = (.started_ts // $t) | .last_ts = $t' \
  "$rec" >"$tmp" && mv "$tmp" "$rec"
exit 0
