#!/usr/bin/env bash
# track-preflight.sh — Start gate: mint (or recover) a stable RUN_ID, verify the run can
# actually proceed, and disambiguate START vs RESUME from a durable breadcrumb. Solves the
# "humans can't reproduce a RUN_ID from memory" footgun: the id is generated once and
# persisted to runs/<RUN_ID>.dispatch, so a later resume reads it back instead of guessing.
#
# Two phases (so the skill can show a summary, get confirmation, THEN persist):
#   inspect (default)  — detect resume-vs-fresh, check prerequisites, print a summary +
#                        emit JSON to stdout. READ-ONLY: writes nothing. Exit non-zero only
#                        on a HARD prerequisite failure (missing gh/git/toolchain) — a
#                        missing dep is not a preference, it blocks in every mode.
#   --persist          — persist runs/<RUN_ID>.dispatch (the breadcrumb) after the caller
#                        has confirmed. Idempotent: re-persisting the same id is a no-op.
#                        (--commit stays as a deprecated alias for --persist.)
#   --complete         — stamp completed_utc + duration_secs (now − created_utc) onto the
#                        breadcrumb at draft-PR handoff. Write-once; the honest home for
#                        "total run time" (a per-event hook never sees PR handoff).
#
# Confirmation waiver (--yes / AUTO_CONFIRM=1):
#   The SKILL requires a HUMAN to approve the summary below before anything is created.
#   That is impossible for a worker fanned out by sso-executing-parallel-tracks: there is no
#   human on the other end of a dispatched subagent, so an un-waivable confirm makes the
#   worker either hang forever or silently self-waive — and N workers each guessing is
#   worse than either. `--yes` (or AUTO_CONFIRM=1) is the EXPLICIT, RECORDED waiver: the
#   orchestrator already took the human confirmation once, at its own wave-plan gate.
#   It waives ONLY the interactive proceed-confirm. It does NOT waive prerequisites —
#   a missing bin / unauthed gh still hard-fails, because a missing dep is not a
#   preference. The waiver is stamped into the summary, the JSON (auto_confirm:true),
#   and the persisted breadcrumb, so an audit can always tell an approved run from a
#   waived one.
#
# Inputs (env or args):
#   TRACK_ID     short track slug (e.g. setup, us1). REQUIRED.
#   TASKS        human task range for the summary/breadcrumb (e.g. "T001-T009"). Optional.
#   RUN_ID       override the minted id (rare). If a breadcrumb for this TRACK_ID already
#                exists, its id WINS (resume) unless RUN_ID is set explicitly.
#   RUNS_DIR     default "runs".
#   TRACK_BRANCH target branch name to work to. Optional — empty derives it from the track
#                slug. Validated with `git check-ref-format` so a bad name fails here.
#   TRACK_BASE_REF / default_branch  base for the new branch (summary only; default main).
#   PREFLIGHT_REQUIRE_TOOLCHAIN  comma list of extra bins to require (e.g. "go,uv,node").
#   PREFLIGHT_REQUIRE_GH         "1" (default) to require an authenticated gh; "0" to skip
#                                (e.g. a setup run that won't open a PR until later).
#
# Resume detection: the NEWEST runs/*.dispatch whose track==TRACK_ID. Its run_id is the
# resume key; the caller then hands that RUN_ID to track-reconcile.sh.
#
# Requires: jq, git. gh only when PREFLIGHT_REQUIRE_GH=1. Keep runtime < 5s.
set -eufo pipefail

# An EXPLICIT RUN_ID is one a CALLER exported (an orchestrator's per-worker id, or a
# human pinning a record). Capture it BEFORE the bootstrap below sources track-env.sh,
# which may carry a managed activation block left by an EARLIER run in this checkout.
# Only the caller's value may override the id this run picks; a file-supplied one is an
# activation hint for the recorder hooks and nothing more. Without this split, starting
# a NEW track in a checkout that still holds a previous run's block silently reuses that
# run's id — the fresh start writes into a finished run's record.
RUN_ID_EXPLICIT="${RUN_ID:-}"

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
  unset __main_root
fi
unset __gcd
if [ -f "$__env_dir/track-env.sh" ]; then . "$__env_dir/track-env.sh"; fi
if [ -f "$__env_dir/track-env.base.sh" ]; then . "$__env_dir/track-env.base.sh"; fi
unset __env_dir

# Canonical hooks dir for the RUN_ID managed block — the MAIN checkout's
# .github/hooks when installed (so the block lands where every hook's bootstrap
# READS it from), else the dir beside this script. Preflight may run from a
# linked worktree on resume, so the write target must not depend on the CWD.
_canon_hooks_dir() {
  local d g r
  d="$(cd "${BASH_SOURCE[0]%/*}" && pwd)"
  g="$(git rev-parse --git-common-dir 2>/dev/null || true)"
  if [ -n "$g" ]; then
    case "$g" in /*) ;; *) g="$PWD/$g" ;; esac
    r="$(cd "$g/.." 2>/dev/null && pwd || true)"
    if [ -n "$r" ] && [ -d "$r/.github/hooks" ]; then d="$r/.github/hooks"; fi
  fi
  printf '%s' "$d"
}

# --- the managed block is a REGISTRY, not a slot ------------------------------------
# One checkout routinely hosts more than one live run: start a feature, leave it, open a
# second editor window on the same repo and start another. Both sessions' hooks resolve to
# this one file. While it held a single run's id and scope, the second `--persist` simply
# overwrote the first — and the first run did not fail loudly, it silently began recording
# into the second run's record while its guard enforced the second run's scope and denied
# the paths its own human had approved. So the block holds a ROW PER RUN and resolves which
# one applies at source time.
_BLK_BEGIN="# >>> track-preflight (managed - do not edit) >>>"
_BLK_END="# <<< track-preflight (managed - do not edit) <<<"
# Pre-registry single-slot markers. Stripped on sight: such a block predates row resolution,
# so leaving it in place would let a stale unconditional adoption outrank the registry.
_BLK_BEGIN_LEGACY="# >>> track-preflight RUN_ID (managed - do not edit) >>>"
_BLK_END_LEGACY="# <<< track-preflight RUN_ID (managed - do not edit) <<<"

# _blk_strip <file> — drop both block formats, leaving operator lines untouched.
_blk_strip() {
  [ -f "$1" ] || return 0
  awk -v b="$_BLK_BEGIN" -v e="$_BLK_END" -v lb="$_BLK_BEGIN_LEGACY" -v le="$_BLK_END_LEGACY" '
    $0==b || $0==lb {skip=1; next}
    skip && ($0==e || $0==le) {skip=0; next}
    !skip {print}
  ' "$1" > "$1.tmp" && mv "$1.tmp" "$1"
}

# _blk_rows <file> — echo the registry rows (id|branch) currently on file, one per line.
_blk_rows() {
  [ -f "$1" ] || return 0
  awk "/^__sbd_rows='\$/{inr=1; next} inr && /^'\$/{inr=0; next} inr && NF {print}" "$1"
}

# _row_dead <id> <branch> — 0 when this row can be dropped. Pruned at write time so the
# registry tracks reality instead of growing forever. A branch that does not exist YET is
# alive (Step 1 runs before Step 3 cuts it); a branch that exists but is checked out in no
# worktree is a run somebody abandoned or finished.
_row_dead() {
  local id="$1" br="$2"
  jq -e '(.completed_utc // "") != ""' "$RUNS_DIR/$id.dispatch" >/dev/null 2>&1 && return 0
  jq -e '(.status // "") | . == "success" or . == "blocked"' "$RUNS_DIR/$id.json" >/dev/null 2>&1 && return 0
  if [ -n "$br" ] && git rev-parse --verify --quiet "refs/heads/$br" >/dev/null 2>&1 \
     && ! git worktree list --porcelain 2>/dev/null | grep -Fqx "branch refs/heads/$br"; then return 0; fi
  return 1
}

# _blk_write <env-file> <rows> — emit the registry block. Shared by --persist and
# --complete so the resolution logic has exactly one author: --complete rewrites the block
# with the finishing run's row dropped, and a second copy of this emitter would be a second
# thing to keep in sync.
_blk_write() {
  local env_file="$1" rows="$2"
  {
    printf '%s\n' "$_BLK_BEGIN"
    cat <<'SBD_BLK_HEAD'
# Registry of this checkout's live runs, and the rule for deciding which one applies to the
# session sourcing this file. One row per run: <run-id>|<branch>. A caller's exported RUN_ID
# always outranks it; a handed-off or abandoned run is never re-adopted.
SBD_BLK_HEAD
    printf "__sbd_runs='%s'\n" "$RUNS_DIR"
    printf "__sbd_rows='\n%s'\n" "$rows"
    cat <<'SBD_BLK_TAIL'
__sbd_pick=''; __sbd_pick_br=''; __sbd_n=0; __sbd_one=''; __sbd_one_br=''
# WHICH run is this session? The reliable answer is the branch checked out where the hook
# runs, so prefer that. Falling back to "the only run on record" keeps the common single-run
# case working when the session sits in the main checkout while the work is in a sibling
# worktree — the skill's own default isolation. With two runs and no branch match there is
# no honest answer, so adopt NOTHING rather than guess: a wrong guess does not fail, it
# quietly records one run's work into another run's record and enforces the wrong scope.
__sbd_head="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || echo '')"
while IFS='|' read -r __r_id __r_br; do
  [ -n "${__r_id:-}" ] || continue
  __sbd_n=$(( __sbd_n + 1 ))
  [ -n "$__sbd_one" ] || { __sbd_one="$__r_id"; __sbd_one_br="${__r_br:-}"; }
  if [ -n "${__r_br:-}" ] && [ "$__r_br" = "$__sbd_head" ]; then __sbd_pick="$__r_id"; __sbd_pick_br="$__r_br"; fi
done <<<"$__sbd_rows"
# The "only run on record" fallback applies ONLY from the MAIN checkout. Inside a linked
# worktree the branch is an exact signal, so a HEAD that matches no row means no run owns
# this session — most sharply once a run completes: its worktree usually still exists, and
# without this restriction the lone surviving row would be adopted there, recording the
# finished feature's tree into an unrelated live run.
if [ -z "$__sbd_pick" ] && [ "$__sbd_n" = 1 ] \
   && [ "$(git rev-parse --git-dir 2>/dev/null || echo a)" = "$(git rev-parse --git-common-dir 2>/dev/null || echo b)" ]; then
  __sbd_pick="$__sbd_one"; __sbd_pick_br="$__sbd_one_br"
fi
# Fast path: a caller already running some OTHER run needs none of the probes below. This
# file is sourced on every tool call, so it must not spend git/jq on a settled question.
if [ -n "$__sbd_pick" ] && { [ -z "${RUN_ID:-}" ] || [ "${RUN_ID:-}" = "$__sbd_pick" ]; }; then
  __sbd_live=1
  # Deliberately terminal: the model called `track-note.sh status success|blocked`, which it
  # does once the run is over. NOT budget-exceeded / no-progress — those are CEILING trips
  # stamped mid-session, and the work that follows one is precisely the report-out: the
  # status stamp, the evidence capture, the handoff. De-adopting there switched off every
  # recorder AND the Stop-time evidence gate for the rest of the session, so a tripped run
  # went dark and un-gated while still writing to the tree. A ceiling stops the run; it must
  # never stop the run being RECORDED.
  if jq -e '(.status // "") | . == "success" or . == "blocked"' "$__sbd_runs/$__sbd_pick.json" >/dev/null 2>&1; then __sbd_live=0; fi
  if [ "$__sbd_live" = 1 ] \
     && jq -e '(.completed_utc // "") != ""' "$__sbd_runs/$__sbd_pick.dispatch" >/dev/null 2>&1; then __sbd_live=0; fi
  # A branch that exists but is checked out nowhere is an abandoned or finished run.
  # Skipped while it does not exist yet (Step 1 precedes Step 3) so a starting run meters itself.
  if [ "$__sbd_live" = 1 ] && [ -n "$__sbd_pick_br" ] \
     && git rev-parse --verify --quiet "refs/heads/$__sbd_pick_br" >/dev/null 2>&1 \
     && ! git worktree list --porcelain 2>/dev/null | grep -Fqx "branch refs/heads/$__sbd_pick_br"; then __sbd_live=0; fi
  if [ "$__sbd_live" = 1 ]; then
    [ -n "${RUN_ID:-}" ] || export RUN_ID="$__sbd_pick"
    # The confirmed scope comes from the run's own breadcrumb — the record of what a human
    # approved — so the registry never has to restate it and a resume cannot lose it. One
    # jq, joined, because this runs on every tool call.
    if [ "${RUN_ID:-}" = "$__sbd_pick" ] && [ -f "$__sbd_runs/$__sbd_pick.dispatch" ]; then
      __sbd_cfg="$(jq -r '[((.allowed_prefixes//[])|join(":")), ((.frozen_paths//[])|join(":")),
                           ((.require_toolchain//[])|join(",")), ((.required_evidence//[])|join(","))]
                          | join("|")' "$__sbd_runs/$__sbd_pick.dispatch" 2>/dev/null || echo '|||')"
      IFS='|' read -r __c_a __c_f __c_t __c_e <<<"$__sbd_cfg"
      # Written as `if` rather than `&&` chains on purpose: this file is sourced by every
      # hook under `set -eufo pipefail`, where a short-circuiting AND-OR list is a footgun
      # nobody wants to re-audit. An empty breadcrumb field means "not declared", so it
      # falls through to track-env.base.sh rather than pinning an empty value.
      if [ -z "${TRACK_ALLOWED_PREFIXES:-}" ] && [ -n "${__c_a:-}" ]; then export TRACK_ALLOWED_PREFIXES="$__c_a"; fi
      if [ -z "${TRACK_FROZEN_PATHS:-}" ] && [ -n "${__c_f:-}" ]; then export TRACK_FROZEN_PATHS="$__c_f"; fi
      if [ -z "${PREFLIGHT_REQUIRE_TOOLCHAIN:-}" ] && [ -n "${__c_t:-}" ]; then export PREFLIGHT_REQUIRE_TOOLCHAIN="$__c_t"; fi
      if [ -z "${TRACK_REQUIRED_EVIDENCE:-}" ] && [ -n "${__c_e:-}" ]; then export TRACK_REQUIRED_EVIDENCE="$__c_e"; fi
      unset __sbd_cfg __c_a __c_f __c_t __c_e
    fi
  fi
  unset __sbd_live
fi
unset __sbd_runs __sbd_rows __sbd_pick __sbd_pick_br __sbd_n __sbd_one __sbd_one_br __sbd_head __r_id __r_br
SBD_BLK_TAIL
    printf '%s\n' "$_BLK_END"
  } >> "$env_file"
}

mode="inspect"
auto_confirm="${AUTO_CONFIRM:-0}"
for a in "$@"; do
  case "$a" in
    --persist) mode="persist" ;;
    --commit) mode="persist" ;;   # deprecated alias for --persist
    --inspect) mode="inspect" ;;
    --complete) mode="complete" ;;
    --yes|-y) auto_confirm=1 ;;   # waive the interactive proceed-confirm (orchestrator runs)
  esac
done
[ "$auto_confirm" = "1" ] || auto_confirm=0

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
track="${TRACK_ID:-}"
tasks="${TASKS:-}"
base="${TRACK_BASE_REF:-${default_branch:-main}}"
branch_override="${TRACK_BRANCH:-}"          # arbitrary target branch name; empty = derive from the track slug
require_gh="${PREFLIGHT_REQUIRE_GH:-1}"
allowed_prefixes="${TRACK_ALLOWED_PREFIXES:-}"   # writable scope the guard enforces; derived from the task file set upstream
frozen_paths="${TRACK_FROZEN_PATHS:-}"           # exact entrypoints no task may edit
require_toolchain="${PREFLIGHT_REQUIRE_TOOLCHAIN:-}"  # bins this task needs on PATH; derive from the task's languages so a missing tool fails HERE, not mid-run
required_evidence="${TRACK_REQUIRED_EVIDENCE:-}"      # evidence floor required on EVERY diff; empty = rules-only (weaker gate)

err() { printf '%s\n' "preflight: $1" >&2; }
die() { err "$1"; exit 1; }

[ -n "$track" ] || die "TRACK_ID is required (the track slug, e.g. setup / us1)."
command -v jq  >/dev/null 2>&1 || die "jq not found."
command -v git >/dev/null 2>&1 || die "git not found."
git rev-parse --is-inside-work-tree >/dev/null 2>&1 || die "not inside a git work tree."

mkdir -p "$RUNS_DIR" 2>/dev/null || true
[ -w "$RUNS_DIR" ] || die "$RUNS_DIR is not writable."

# --- resume detection: newest breadcrumb for this track ----------------------------
# NOTE: `set -f` (noglob) is active, so shell globbing of *.dispatch is disabled — use
# `find` (which does its own matching) rather than an `ls runs/*.dispatch` shell glob.
existing_id=""
existing_file=""
# Build a mtime-sorted (newest first) list, then scan with a here-string so the matched
# filename survives in this shell (a pipe-to-while would set it inside a lost subshell).
sorted=""
while IFS= read -r f; do
  [ -n "$f" ] && [ -f "$f" ] || continue
  mt="$(stat -f %m "$f" 2>/dev/null || stat -c %Y "$f" 2>/dev/null || echo 0)"
  sorted="$sorted$mt	$f
"
done <<<"$(find "$RUNS_DIR" -maxdepth 1 -type f -name '*.dispatch' 2>/dev/null || true)"
sorted="$(printf '%s' "$sorted" | sort -rn)"
while IFS="$(printf '\t')" read -r _ f; do
  [ -n "${f:-}" ] || continue
  t="$(jq -r '.track // empty' "$f" 2>/dev/null || true)"
  if [ "$t" = "$track" ]; then
    existing_file="$f"; existing_id="$(jq -r '.run_id // empty' "$f" 2>/dev/null)"; break
  fi
done <<<"$sorted"

# --- pick RUN_ID: explicit override > existing breadcrumb (resume) > mint fresh -----
resume=false
if [ -n "$RUN_ID_EXPLICIT" ]; then
  run_id="$RUN_ID_EXPLICIT"
  [ -n "$existing_id" ] && [ "$existing_id" = "$run_id" ] && resume=true
elif [ -n "$existing_id" ]; then
  run_id="$existing_id"; resume=true
else
  run_id="$(date -u +%Y-%m-%dT%H-%M)_${track}"
fi
rec_dispatch="$RUNS_DIR/$run_id.dispatch"

# --- prerequisite checks (hard) ----------------------------------------------------
missing=""
if [ "$require_gh" = "1" ]; then
  if command -v gh >/dev/null 2>&1; then
    gh auth status >/dev/null 2>&1 || missing="$missing gh(not-authed)"
  else
    missing="$missing gh(absent)"
  fi
fi
if [ -n "${PREFLIGHT_REQUIRE_TOOLCHAIN:-}" ]; then
  saved_ifs="$IFS"; IFS=,
  for bin in $PREFLIGHT_REQUIRE_TOOLCHAIN; do
    [ -n "$bin" ] || continue
    command -v "$bin" >/dev/null 2>&1 || missing="$missing $bin(absent)"
  done
  IFS="$saved_ifs"
fi

# --- dependency version-lock (delegated to track-deps.sh; TTL-cached) --------------
# Verify the repo's pinned tool versions (skill-deps.json) once per run. track-deps.sh
# no-ops when no manifest is present (the lock is opt-in) and reuses a runs/-local TTL
# cache so a heavy version probe does not re-run on every preflight. A REQUIRED dep that
# is missing — or, under TRACK_DEPS_STRICT, out of range — folds into `missing` so it
# blocks the start gate exactly like an absent toolchain bin.
deps_configured=false; deps_ok=true; deps_viol=""; deps_warn=""
deps_script="${BASH_SOURCE[0]%/*}/track-deps.sh"
if [ -f "$deps_script" ]; then
  deps_rc=0
  deps_out="$(bash "$deps_script" --json 2>/dev/null)" || deps_rc=$?
  if [ -n "${deps_out:-}" ]; then
    # NOTE: jq's `//` treats BOTH null and `false` as empty, so `.ok // true` would
    # wrongly yield true for a genuine ok:false. Read the raw value and normalize.
    deps_configured="$(printf '%s' "$deps_out" | jq -r 'if .configured == true then "true" else "false" end' 2>/dev/null || echo false)"
    deps_ok="$(printf '%s' "$deps_out" | jq -r 'if .ok == false then "false" else "true" end' 2>/dev/null || echo true)"
    deps_viol="$(printf '%s' "$deps_out" | jq -r '(.violations // []) | join(",")' 2>/dev/null || true)"
    deps_warn="$(printf '%s' "$deps_out" | jq -r '(.warnings // []) | join(",")' 2>/dev/null || true)"
  fi
  if [ "$deps_configured" = true ] && [ "$deps_ok" != true ]; then
    missing="$missing deps(${deps_viol:-lock})"
  fi
fi
missing="$(printf '%s' "$missing" | sed 's/^ *//')"

# --- evidence-kind consistency (soft config check) ---------------------------------
# The gate requires kinds (TRACK_EVIDENCE_RULES glob:kind, TRACK_REQUIRED_EVIDENCE) that
# capture must be able to TAG (TRACK_EVIDENCE_KINDS label:pattern, plus implicit "test"
# when TRACK_TEST_CMD_PATTERN is set). A required kind with no matching capture label can
# NEVER be captured, so the gate would block forever — a silent config typo. Surface it as
# a warning (non-fatal: kinds may legitimately be supplied outside this script's view).
config_warn=""
labels=""
if [ -n "${TRACK_EVIDENCE_KINDS:-}" ]; then
  saved_ifs="$IFS"; IFS=';'
  for pair in $TRACK_EVIDENCE_KINDS; do
    label="${pair%%:*}"
    [ -n "$label" ] && [ "$label" != "$pair" ] && labels="$labels $label"
  done
  IFS="$saved_ifs"
fi
[ -n "${TRACK_TEST_CMD_PATTERN:-}" ] && labels="$labels test"
# Only validate when SOME capture label exists — otherwise the evidence system is simply
# not configured here and there is nothing to cross-check.
if [ -n "${labels// /}" ]; then
  req_kinds=""
  if [ -n "${TRACK_REQUIRED_EVIDENCE:-}" ]; then
    saved_ifs="$IFS"; IFS=,
    for k in $TRACK_REQUIRED_EVIDENCE; do [ -n "$k" ] && req_kinds="$req_kinds $k"; done
    IFS="$saved_ifs"
  fi
  if [ -n "${TRACK_EVIDENCE_RULES:-}" ]; then
    saved_ifs="$IFS"; IFS=';'
    for rule in $TRACK_EVIDENCE_RULES; do
      kind="${rule#*:}"
      [ -n "$kind" ] && [ "$kind" != "$rule" ] && req_kinds="$req_kinds $kind"
    done
    IFS="$saved_ifs"
  fi
  for rk in $(printf '%s\n' $req_kinds | sed '/^$/d' | sort -u); do
    found=0
    for lb in $labels; do [ "$rk" = "$lb" ] && found=1 && break; done
    [ "$found" -eq 1 ] || config_warn="$config_warn ${rk}(no-capture-label)"
  done
fi
config_warn="$(printf '%s' "$config_warn" | sed 's/^ *//')"

# Target branch: an explicit TRACK_BRANCH wins; otherwise derive from the track slug.
# Validate an explicit name with git's own ref rules so a bad name fails HERE (at the
# start gate), not mid-run when the worktree step tries to create it.
branch="${branch_override:-$track}"
if [ -n "$branch_override" ]; then
  git check-ref-format --branch "$branch_override" >/dev/null 2>&1 \
    || die "TRACK_BRANCH '$branch_override' is not a valid git branch name."
fi
prereq_ok=true; [ -n "$missing" ] && prereq_ok=false

# --- persist phase: persist the breadcrumb, then exit ------------------------------
if [ "$mode" = "persist" ]; then
  [ "$prereq_ok" = true ] || die "refusing to persist breadcrumb — unmet prerequisites:$missing"
  if [ -f "$rec_dispatch" ]; then
    printf '%s\n' "preflight: breadcrumb already present ($rec_dispatch) — no-op." >&2
  else
    jq -n \
      --arg run_id "$run_id" --arg track "$track" --arg tasks "$tasks" \
      --arg branch "$branch" --arg base "$base" \
      --arg created "$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
      --arg allowed "$allowed_prefixes" --arg frozen "$frozen_paths" \
      --arg toolchain "$require_toolchain" --arg required_evidence "$required_evidence" \
      --argjson auto_confirm "$([ "$auto_confirm" = 1 ] && echo true || echo false)" \
      '{run_id:$run_id, track:$track, tasks:$tasks, branch:$branch, base_ref:$base, created_utc:$created,
        auto_confirm:$auto_confirm,
        confirmed_by:(if $auto_confirm then "orchestrator-waiver" else "human" end),
        allowed_prefixes:($allowed | if . == "" then [] else split(":") end),
        frozen_paths:($frozen | if . == "" then [] else split(":") end),
        scope_set:($allowed != ""),
        require_toolchain:($toolchain | if . == "" then [] else split(",") end),
        toolchain_set:($toolchain != ""),
        required_evidence:($required_evidence | if . == "" then [] else split(",") end),
        evidence_floor_set:($required_evidence != "")}' \
      > "$rec_dispatch"
  fi
  # --- activate the run for THIS checkout ------------------------------------------
  # Two distinct things have to survive from this gate to the hooks, and NEITHER can
  # travel in the process environment: hooks are spawned by the agent surface, not by
  # the shell this script runs in, so an `export` here (or in any later tool call)
  # never reaches them. The only channel is the file every hook sources.
  #
  #   1. RUN_ID — the per-call recorders (meter/trace/evidence/note/brief/compact) and
  #      the Stop-time evidence gate all no-op without it. In a solo run no orchestrator
  #      exports it, so the record would stay empty.
  #   2. The CONFIRMED task-derived config — the writable scope a human just approved on
  #      the summary above. Without it `track-guard.sh` fails closed and denies every
  #      edit to the very paths that were approved, and the approval survives only as a
  #      line in the breadcrumb that nothing enforces. The operator's own scope lines in
  #      track-env.base.sh are never touched; this block only supplies values the
  #      environment has not already set.
  #
  # Values come from the BREADCRUMB, not from this process's env, because on a RESUME
  # the breadcrumb is the only record of what was approved and the resuming session has
  # no reason to have re-exported any of it. Guarded by the track-env.base.sh marker so
  # this only ever fires inside a real INSTALLED hooks dir — never in the skill's
  # scripts/ source mirror that unit tests run in-place.
  #
  # The block SELF-RETIRES (see the conditions it writes). `--complete` also removes it,
  # but completion is reached at draft-PR handoff ONLY: a run that ends any other way —
  # `blocked`, crash, human abandon — never gets there and used to leave an
  # unconditional `export RUN_ID=…` behind forever. That residue governs every LATER
  # session in the checkout, on any branch, which is how a finished run's evidence
  # demands land on an unrelated task's diff. Binding adoption to "run is live AND its
  # branch is checked out here" retires the id on every exit path instead of just the
  # happy one.
  _env_dir="$(_canon_hooks_dir)"
  if [ -f "$_env_dir/track-env.base.sh" ]; then
    env_file="$_env_dir/track-env.sh"
    # Merge THIS run into the registry: keep every other row that is still alive, drop any
    # stale copy of our own, append ours. A branch carrying a single quote would break the
    # quoted row block, so it is refused rather than silently corrupting the file.
    _rows_keep=""
    if [ -f "$env_file" ]; then
      while IFS='|' read -r _r_id _r_br; do
        [ -n "${_r_id:-}" ] || continue
        [ "$_r_id" = "$run_id" ] && continue
        _row_dead "$_r_id" "${_r_br:-}" && continue
        _rows_keep="$_rows_keep$_r_id|${_r_br:-}
"
      done <<<"$(_blk_rows "$env_file")"
    fi
    case "$run_id$branch" in
      *"'"*) err "run id or branch contains a single quote — not registering it in track-env.sh" ;;
      *)     _rows_keep="$_rows_keep$run_id|$branch
" ;;
    esac
    # Tell the operator when this checkout now hosts more than one live run: adoption stops
    # being inferable from a session sitting in the main checkout, and the fix is a choice
    # they make (open the editor on the worktree), not one this script can make for them.
    _n_rows="$(printf '%s' "$_rows_keep" | grep -c . || true)"
    if [ "${_n_rows:-0}" -gt 1 ]; then
      err "note: $_n_rows live runs now share this checkout. Hooks adopt a run only from a session whose HEAD is that run's branch — work from each run's own worktree, or the recorders stay off."
    fi
    _blk_strip "$env_file"
    _blk_write "$env_file" "$_rows_keep"
    unset _rows_keep _n_rows
  fi
  printf '%s\n' "$run_id"
  exit 0
fi

# --- complete phase: stamp the terminal breadcrumb, then exit ----------------------
# Called ONCE at draft-PR handoff — the one deliberate step that knows the run is done.
# Writes completed_utc + duration_secs (now − created_utc) into the existing breadcrumb.
# Write-once: re-completing a stamped run is a no-op, so a resume can't overwrite the
# original finish time. This is the honest home for "total run time" — a single stamp at
# a real boundary, not a per-event hook (a PostToolUse hook never sees PR handoff).
if [ "$mode" = "complete" ]; then
  [ -f "$rec_dispatch" ] || die "cannot complete — no breadcrumb at $rec_dispatch (run --persist first)."
  if [ "$(jq -r '.completed_utc // empty' "$rec_dispatch" 2>/dev/null)" != "" ]; then
    printf '%s\n' "preflight: breadcrumb already completed ($rec_dispatch) — no-op." >&2
    printf '%s\n' "$run_id"
    exit 0
  fi
  now_utc="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  created="$(jq -r '.created_utc // empty' "$rec_dispatch" 2>/dev/null)"
  # Portable ISO-8601-UTC → epoch (BSD/macOS `date -j -f`; GNU `date -d`). Either failing
  # leaves duration null rather than aborting the handoff over a clock-parse quirk.
  to_epoch() { date -j -u -f "%Y-%m-%dT%H:%M:%SZ" "$1" +%s 2>/dev/null || date -u -d "$1" +%s 2>/dev/null || echo ""; }
  dur="null"
  if [ -n "$created" ]; then
    c_epoch="$(to_epoch "$created")"; n_epoch="$(to_epoch "$now_utc")"
    if [ -n "$c_epoch" ] && [ -n "$n_epoch" ] && [ "$n_epoch" -ge "$c_epoch" ]; then
      dur="$(( n_epoch - c_epoch ))"
    fi
  fi
  tmp="$(mktemp)"
  jq --arg done "$now_utc" --argjson dur "$dur" \
    '.completed_utc = $done | .duration_secs = $dur' "$rec_dispatch" >"$tmp" && mv "$tmp" "$rec_dispatch"
  # Retire THIS run from the registry so it stops steering the recorder hooks — and only
  # this run: a sibling run started from the same checkout is still live, and dropping the
  # whole block would silently switch its recording off at the moment an unrelated feature
  # happened to finish. When our row was the last one, the block goes entirely.
  _env_dir="$(_canon_hooks_dir)"
  if [ -f "$_env_dir/track-env.base.sh" ]; then
    env_file="$_env_dir/track-env.sh"
    if [ -f "$env_file" ]; then
      _rows_keep=""
      while IFS='|' read -r _r_id _r_br; do
        [ -n "${_r_id:-}" ] || continue
        [ "$_r_id" = "$run_id" ] && continue
        _row_dead "$_r_id" "${_r_br:-}" && continue
        _rows_keep="$_rows_keep$_r_id|${_r_br:-}
"
      done <<<"$(_blk_rows "$env_file")"
      _blk_strip "$env_file"
      [ -n "$_rows_keep" ] && _blk_write "$env_file" "$_rows_keep"
      unset _rows_keep
    fi
  fi
  printf '%s\n' "$run_id"
  exit 0
fi

{
  echo "PREFLIGHT — sso-single-branch-development"
  echo "  Mode:         $([ "$resume" = true ] && echo 'RESUME (breadcrumb found)' || echo 'START (fresh)')"
  echo "  Track:        $track"
  echo "  Tasks:        ${tasks:-<unspecified>}"
  echo "  RUN_ID:       $run_id $([ "$resume" = true ] && echo '(recovered)' || echo '(generated)')"
  [ -n "$existing_file" ] && echo "  Breadcrumb:   $existing_file"
  echo "  Branch:       $branch  $([ -n "$branch_override" ] && echo '(TRACK_BRANCH — custom)' || echo '(derived from track slug)')"
  echo "  Base ref:     $base"
  # Anchored to the MAIN working tree, so it is the same directory from every linked
  # worktree. Printed because the run record AND the governance bundle both belong
  # here: a bundle written to a worktree-relative runs/ lands in that worktree's own
  # gitignored copy, splitting it from the record that points at it.
  echo "  Runs dir:     $RUNS_DIR  (run record + governance bundle live HERE — use this path, not a bare 'runs/')"
  if [ -n "$allowed_prefixes" ]; then
    echo "  Scope:        $allowed_prefixes  (guard denies edits outside this)"
  else
    echo "  Scope:        ⚠ TRACK_ALLOWED_PREFIXES UNSET — guard fails closed (denies ALL edits). Derive + set the writable scope from the task file set before dispatch."
  fi
  [ -n "$frozen_paths" ] && echo "  Frozen:       $frozen_paths  (no task may edit)"
  if [ -n "$require_toolchain" ]; then
    echo "  Toolchain:    $require_toolchain  (required on PATH — a missing bin blocks here)"
  else
    echo "  Toolchain:    (none required) — derive from the task's languages so a missing tool fails here, not mid-run"
  fi
  if [ -n "$required_evidence" ]; then
    echo "  Evid. floor:  $required_evidence  (required on every diff regardless of rules)"
  else
    echo "  Evid. floor:  ⚠ TRACK_REQUIRED_EVIDENCE UNSET — gate is rules-only (no floor). Derive the mandatory kinds from the task's languages if any must run on every diff."
  fi
  if [ "$deps_configured" = true ]; then
    if [ "$deps_ok" = true ]; then
      echo "  Deps lock:    OK (skill-deps.json verified${deps_warn:+ · warnings: $deps_warn})"
    else
      echo "  Deps lock:    ⚠ VIOLATION: $deps_viol  (pinned versions in skill-deps.json; TRACK_DEPS_STRICT=$([ "${TRACK_DEPS_STRICT:-0}" = 1 ] && echo on || echo off))"
    fi
  fi
  if [ "$prereq_ok" = true ]; then
    echo "  Prereqs:      OK (git ✓ · runs/ ✓ writable$([ "$require_gh" = 1 ] && echo ' · gh ✓ authed')${PREFLIGHT_REQUIRE_TOOLCHAIN:+ · $PREFLIGHT_REQUIRE_TOOLCHAIN ✓})"
    if [ "$auto_confirm" = 1 ]; then
      echo "  Confirm:      WAIVED (--yes / AUTO_CONFIRM) — orchestrator run; the human gate was taken upstream at the wave plan"
      echo "  → Proceed     no interactive confirm; re-run with --persist to persist the breadcrumb"
    else
      echo "  Confirm:      REQUIRED — a human must approve this summary before anything is created"
      echo "  → Proceed?    confirm to dispatch (then re-run with --persist to persist the breadcrumb)"
    fi
  else
    echo "  Prereqs:      BLOCKED — missing:$missing"
    echo "  → Fix the missing prerequisite before dispatching."
  fi
  [ -n "$config_warn" ] && echo "  Config:       ⚠ evidence kinds required but not capturable:$config_warn (check TRACK_EVIDENCE_KINDS labels vs TRACK_EVIDENCE_RULES/TRACK_REQUIRED_EVIDENCE)"
} >&2

jq -nc \
  --arg run_id "$run_id" --arg track "$track" --arg tasks "$tasks" \
  --arg branch "$branch" --arg base "$base" --arg runs_dir "$RUNS_DIR" \
  --argjson resume "$resume" --argjson prereq_ok "$prereq_ok" \
  --arg missing "$missing" --arg breadcrumb "$existing_file" \
  --arg config_warn "$config_warn" \
  --arg allowed "$allowed_prefixes" --arg frozen "$frozen_paths" \
  --arg toolchain "$require_toolchain" --arg required_evidence "$required_evidence" \
  --argjson deps_configured "$([ "$deps_configured" = true ] && echo true || echo false)" \
  --argjson deps_ok "$([ "$deps_ok" = true ] && echo true || echo false)" \
  --arg deps_viol "$deps_viol" --arg deps_warn "$deps_warn" \
  --argjson auto_confirm "$([ "$auto_confirm" = 1 ] && echo true || echo false)" \
  '{run_id:$run_id, track:$track, tasks:$tasks, branch:$branch, base_ref:$base,
    runs_dir:$runs_dir,
    mode:(if $resume then "resume" else "start" end),
    prereq_ok:$prereq_ok,
    deps_configured:$deps_configured,
    deps_ok:$deps_ok,
    deps_violations:($deps_viol | if . == "" then [] else split(",") end),
    deps_warnings:($deps_warn | if . == "" then [] else split(",") end),
    auto_confirm:$auto_confirm,
    confirm_required:($auto_confirm | not),
    allowed_prefixes:($allowed | if . == "" then [] else split(":") end),
    frozen_paths:($frozen | if . == "" then [] else split(":") end),
    scope_set:($allowed != ""),
    require_toolchain:($toolchain | if . == "" then [] else split(",") end),
    toolchain_set:($toolchain != ""),
    required_evidence:($required_evidence | if . == "" then [] else split(",") end),
    evidence_floor_set:($required_evidence != ""),
    missing:($missing | if . == "" then [] else split(" ") end),
    config_warnings:($config_warn | if . == "" then [] else split(" ") end),
    breadcrumb:($breadcrumb | if . == "" then null else . end)}'

[ "$prereq_ok" = true ] || exit 3
exit 0
