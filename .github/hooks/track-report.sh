#!/usr/bin/env bash
# track-report.sh — render the DETERMINISTIC half of a stage/PR completion report.
#
# NOT a hook. Run by the skill at Step 8 (draft-PR handoff). It emits the "Auto" block
# of the PR body ENTIRELY from machine state that already exists — never from the
# model's recollection — so the factual half of the report cannot drift from reality:
#
#   - Files changed + change size   ← git diff --name-status / --stat vs TRACK_BASE_REF
#   - Evidence (kind, cmd, fingerprint, pass/fail) ← runs/<RUN_ID>.json .evidence[]
#   - Tool calls / subagent order   ← runs/<RUN_ID>.json .tool_calls / .trace[]
#   - Self-reported skills / loops  ← runs/<RUN_ID>.json .skills[] / .iterations
#                                     (rendered UNDER a "self-reported" heading so a
#                                      reader can never mistake a model claim for a
#                                      hook-observed fact — same discipline as the record)
#   - Duration / timestamps         ← runs/<RUN_ID>.dispatch breadcrumb + heartbeat
#
# The NARRATIVE half (constitution/OWASP compliance, "After merge", caveats) is a model
# ASSERTION and is NOT produced here — it lives in templates/pr-body.md, which the skill
# fills in and appends. Keeping the two halves in separate producers is the whole point:
# machine-rendered facts vs. clearly-labelled model claims.
#
# Usage (writes the Auto block to stdout):
#   track-report.sh                 # uses $RUN_ID (or recovers it from a runs/*.dispatch)
#   track-report.sh <RUN_ID>        # explicit run id
#   track-report.sh --json          # emit the same facts as a JSON object (for tooling)
#
# Opt-in via env (same contract as the rest of the bundle):
#   RUN_ID          run id (else recovered from the newest runs/*.dispatch breadcrumb)
#   RUNS_DIR        where run records live (default: runs)
#   TRACK_BASE_REF  diff base for the files/size block (default: origin/main, then HEAD)
#
# Requires: jq, git. Read-only: never mutates the record, the tree, or git state.
set -eufo pipefail

# Bootstrap the same presets every bundle script sources (exported > worktree > base).
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
# --- the auto block's self-attestation ---------------------------------------
# The block between the BEGIN/END markers is machine-rendered and says so. Nothing
# enforced it. On a real client PR the markers were intact and everything between them had
# been RE-TYPED: the summary singularised ("1 warning · 0 failures" for
# "1 warning(s) · 0 failure(s)"), the findings table cut from four columns to three
# (dropping "How to clear it"), the "not a clean bill of health" caveat paraphrased, the
# <details> list of un-checked invariants deleted, and the one WARN rewritten into an
# exculpatory paragraph asserting compliance no artifact showed. A reader has no way to tell
# that from a render — the markers are the only signal, and they survived.
#
# So the END marker now carries a sha of the block content. Recomputing it is mechanical, so
# `--verify-body` can settle it locally at `gh pr create` (track-guard.sh) and in CI
# (agent-pr-audit.yml) instead of both re-deriving "what a genuine render looks like" from
# its shape. This does not make forgery impossible — a determined caller can re-hash — but it
# ends the SILENT case, which is the one that actually happened.
block_sha() { # stdin → sha1 of the block content
  if command -v shasum >/dev/null 2>&1; then shasum; else sha1sum; fi | cut -d' ' -f1
}
BEGIN_MARK='<!-- BEGIN track-report auto block — machine-rendered, do not hand-edit -->'

# --verify-body <file> — exit 0 verified · 3 tampered · 4 no block / no sha / unreadable.
# Deliberately BEFORE run-id recovery: verification reads only the file it is given, so it
# works from a worktree, in CI, and for a run whose record is long gone.
if [ "${1:-}" = "--verify-body" ]; then
  body_file="${2:-}"
  [ -n "$body_file" ] && [ -f "$body_file" ] \
    || { printf 'track-report: --verify-body needs a readable file\n' >&2; exit 4; }
  if ! grep -qF "$BEGIN_MARK" "$body_file"; then
    printf 'no auto block in %s\n' "$body_file"; exit 4
  fi
  end_line="$(grep -n '^<!-- END track-report auto block' "$body_file" | head -1 || true)"
  [ -n "$end_line" ] || { printf 'auto block has a BEGIN marker but no END marker\n' >&2; exit 3; }
  claimed="$(printf '%s' "${end_line#*:}" | sed -n 's/.*sha=\([0-9a-f]\{40\}\).*/\1/p')"
  [ -n "$claimed" ] || { printf 'auto block carries no sha= in its END marker — rendered by an older track-report.sh, or hand-authored\n' >&2; exit 4; }
  begin_n="$(grep -nF "$BEGIN_MARK" "$body_file" | head -1 | cut -d: -f1)"
  end_n="${end_line%%:*}"
  actual="$(sed -n "${begin_n},$((end_n - 1))p" "$body_file" | block_sha)"
  if [ "$claimed" = "$actual" ]; then printf 'auto block verified (sha=%s)\n' "$actual"; exit 0; fi
  printf 'auto block does NOT match its own sha (claims %s, content hashes to %s)\n' "$claimed" "$actual" >&2
  exit 3
fi

emit_json=0
run_id="${RUN_ID:-}"
for arg in "$@"; do
  case "$arg" in
    --json) emit_json=1 ;;
    -*)     printf 'track-report: unknown flag %s\n' "$arg" >&2; exit 2 ;;
    *)      run_id="$arg" ;;
  esac
done

# Recover RUN_ID from the newest breadcrumb when none was supplied — a solo run that
# never exported it can still be reported at handoff.
if [ -z "$run_id" ]; then
  # RUN_IDs are `<UTC-timestamp>_<track>`, so the lexically-greatest .dispatch is the
  # newest — a deterministic pick that also survives `set -f` (no glob) via find, and
  # doesn't depend on mtime. `|| true` keeps an empty runs/ from tripping pipefail.
  newest="$(find "$RUNS_DIR" -maxdepth 1 -name '*.dispatch' -type f 2>/dev/null | sort | tail -1 || true)"
  if [ -n "$newest" ]; then
    run_id="$(jq -r '.run_id // empty' "$newest" 2>/dev/null || true)"
    [ -n "$run_id" ] || run_id="$(basename "$newest" .dispatch)"
  fi
fi
[ -n "$run_id" ] || { printf 'track-report: no RUN_ID and no runs/*.dispatch to recover from.\n' >&2; exit 2; }

rec="$RUNS_DIR/$run_id.json"
dispatch="$RUNS_DIR/$run_id.dispatch"

# --- gather facts (missing sources degrade to empty, never abort) --------------------
base="${TRACK_BASE_REF:-origin/main}"
git rev-parse --verify --quiet "$base" >/dev/null 2>&1 || base="HEAD"

files_ns="$(git diff --name-status "$base"...HEAD 2>/dev/null || true)"
stat_line="$(git diff --shortstat "$base"...HEAD 2>/dev/null | sed 's/^ *//' || true)"
[ -n "$stat_line" ] || stat_line="no committed changes vs ${base}"
files_count="$(printf '%s\n' "$files_ns" | grep -c . 2>/dev/null || true)"
[ -n "$files_count" ] || files_count=0

# record-derived (all optional — absent record = empty sections, not an error)
if [ -f "$rec" ]; then
  tool_calls="$(jq -r '.tool_calls // 0' "$rec" 2>/dev/null || echo 0)"
  started_ts="$(jq -r '.started_ts // empty' "$rec" 2>/dev/null || true)"
  last_ts="$(jq -r '.last_ts // empty' "$rec" 2>/dev/null || true)"
  iterations="$(jq -r '.iterations // 0' "$rec" 2>/dev/null || echo 0)"
  ev_count="$(jq -r '.evidence | length' "$rec" 2>/dev/null || echo 0)"
  review_seen="$(jq -r '[.skills[]? | select((.skill // "") | ascii_downcase | test("requesting-code-review|code-review"))] | length' "$rec" 2>/dev/null || echo 0)"
else
  tool_calls=0; started_ts=""; last_ts=""; iterations=0; ev_count=0; review_seen=0
fi

if [ -f "$dispatch" ]; then
  d_branch="$(jq -r '.branch // empty' "$dispatch" 2>/dev/null || true)"
  d_tasks="$(jq -r '.tasks // empty' "$dispatch" 2>/dev/null || true)"
  d_created="$(jq -r '.created_utc // empty' "$dispatch" 2>/dev/null || true)"
  d_completed="$(jq -r '.completed_utc // empty' "$dispatch" 2>/dev/null || true)"
  d_duration="$(jq -r '.duration_secs // empty' "$dispatch" 2>/dev/null || true)"
else
  d_branch=""; d_tasks=""; d_created=""; d_completed=""; d_duration=""
fi

# --- compliance warnings: silent-omission tripwires a reviewer MUST see --------------
# A hook cannot observe review quality or judge a pasted capture, but it CAN flag the two
# gaps that otherwise slip through unnoticed: an empty evidence pack and a missing code-
# review activation. Absence is reported LOUDLY here so a skipped Step-5 review or an
# un-captured verification surfaces in the PR body itself, not in a later audit.
warnings=()
[ "${ev_count:-0}" -gt 0 ] || warnings+=("No evidence rows recorded — the evidence gate ran on an empty pack. Paste the real verification output in the Asserted zone and confirm every required kind passed, or wire a matching TRACK_EVIDENCE_RULES entry so it is captured mechanically.")
[ "${review_seen:-0}" -gt 0 ] || warnings+=("No \`requesting-code-review\` activation on record — Step 5 review may have been skipped (or not logged via \`track-note.sh skill requesting-code-review\`). Confirm the maker/checker review happened before merge.")

if [ "${#warnings[@]}" -gt 0 ]; then
  warn_json="$(printf '%s\n' "${warnings[@]}" | jq -R . | jq -s .)"
else
  warn_json='[]'
fi

# --- JSON mode: same facts, machine-consumable --------------------------------------
if [ "$emit_json" -eq 1 ]; then
  jq -nc \
    --arg run_id "$run_id" --arg base "$base" \
    --arg stat "$stat_line" --arg files "$files_ns" \
    --argjson tool_calls "${tool_calls:-0}" --argjson iterations "${iterations:-0}" \
    --arg started_ts "$started_ts" --arg last_ts "$last_ts" \
    --arg branch "$d_branch" --arg tasks "$d_tasks" \
    --arg created "$d_created" --arg completed "$d_completed" --arg duration "$d_duration" \
    --argjson warnings "$warn_json" \
    --slurpfile r "${rec:-/dev/null}" \
    '{run_id:$run_id, base:$base, branch:$branch, tasks:$tasks,
      change_shortstat:$stat,
      files:($files | split("\n") | map(select(length>0))),
      tool_calls:$tool_calls, iterations:$iterations,
      started_ts:$started_ts, last_ts:$last_ts,
      created_utc:$created, completed_utc:$completed, duration_secs:$duration,
      warnings:$warnings,
      evidence:(($r[0].evidence) // []),
      trace:(($r[0].trace) // []),
      skills:(($r[0].skills) // [])}' 2>/dev/null \
  || printf '{"run_id":"%s","error":"record unreadable"}\n' "$run_id"
  exit 0
fi

# --- markdown mode: the "Auto" block ------------------------------------------------
md_row_files() {
  # git name-status → a markdown table (status letter → word)
  printf '%s\n' "$files_ns" | while IFS=$'\t' read -r st path rest; do
    [ -n "${st:-}" ] || continue
    case "$st" in
      A*) w="added" ;; M*) w="modified" ;; D*) w="deleted" ;;
      R*) w="renamed"; path="$rest" ;; C*) w="copied"; path="$rest" ;;
      *)  w="$st" ;;
    esac
    printf '| `%s` | %s |\n' "$path" "$w"
  done
}

md_group_by_area() {
  # git name-status → a compact table grouped by top-level path segment, so a
  # 100-file scaffold reads as a handful of area rows instead of a file-by-file wall.
  # Area = first path component (`backend-go/`), or the whole name if it has no slash
  # (`Makefile`). Emitted in first-seen order with a per-area add/modify/delete tally.
  printf '%s\n' "$files_ns" | awk -F'\t' '
    NF>=2 {
      st=$1; p=$2;
      if (st ~ /^R/ || st ~ /^C/) p=$3;              # renamed/copied → destination
      slash=index(p, "/");
      area=(slash>0) ? substr(p,1,slash) : p;
      if (!(area in seen)) { seen[area]=1; order[++n]=area; }
      cnt[area]++;
      c=substr(st,1,1);
      if (c=="A") add[area]++; else if (c=="M") mod[area]++;
      else if (c=="D") del[area]++; else oth[area]++;
    }
    END {
      print "| Area | Files | Changes |";
      print "|---|---|---|";
      for (i=1;i<=n;i++) {
        a=order[i]; chg="";
        if (add[a]) chg=chg (chg?", ":"") add[a] " added";
        if (mod[a]) chg=chg (chg?", ":"") mod[a] " modified";
        if (del[a]) chg=chg (chg?", ":"") del[a] " deleted";
        if (oth[a]) chg=chg (chg?", ":"") oth[a] " other";
        printf "| `%s` | %d | %s |\n", a, cnt[a], chg;
      }
    }
  '
}

# Buffered to a FILE rather than a `$( … )` capture: the block's own renderers contain jq
# programs with parenthesised strings, and bash 3.2 (the system bash on macOS, where these
# hooks run) mis-scans for the closing paren of a command substitution that encloses them.
_ab="$(mktemp)"
trap 'rm -f "$_ab"' EXIT
{
printf '%s\n' "$BEGIN_MARK"
printf '### Run `%s`\n\n' "$run_id"
[ -n "$d_branch" ] && printf -- '- **Branch:** `%s`\n' "$d_branch"
[ -n "$d_tasks" ]  && printf -- '- **Tasks:** %s\n' "$d_tasks"
printf -- '- **Base:** `%s`\n' "$base"
if [ -n "$d_duration" ] && [ "$d_duration" != "null" ]; then
  printf -- '- **Duration:** %ss' "$d_duration"
  [ -n "$d_completed" ] && printf ' (completed %s)' "$d_completed"
  printf '\n'
elif [ -n "$started_ts" ]; then
  printf -- '- **Activity window:** %s → %s (heartbeat)\n' "$started_ts" "${last_ts:-$started_ts}"
fi
printf '\n#### Files changed\n\n'
if [ -n "$files_ns" ]; then
  # Many files → group by area + tuck the full list into a <details> so the PR body
  # stays scannable; few files → a plain per-file table is clearer. Threshold tunable.
  if [ "${files_count:-0}" -gt "${TRACK_REPORT_FILE_TABLE_MAX:-12}" ]; then
    md_group_by_area
    printf '\n<details><summary>All %s files</summary>\n\n' "$files_count"
    printf '| File | Change |\n|---|---|\n'
    md_row_files
    printf '\n</details>\n'
  else
    printf '| File | Change |\n|---|---|\n'
    md_row_files
  fi
else
  printf '_No committed changes vs `%s`._\n' "$base"
fi
printf '\n_%s_\n' "$stat_line"

# Evidence — pass/fail derived from the recorded response text, fingerprint shown.
printf '\n#### Evidence\n\n'
if [ -f "$rec" ] && [ "$(jq -r '.evidence | length' "$rec" 2>/dev/null || echo 0)" -gt 0 ]; then
  # Read the verdict track-evidence.sh recorded at capture; only fall back to
  # grepping for records written before verdicts existed. This script used to carry
  # its own default fail-pattern, which differed from the gate's — so the same
  # capture could be ❌ here and acceptable there, with neither side flagging it.
  fail_pat="${TRACK_FAIL_PATTERN:-FAIL|--- FAIL|Error:|panic:|Traceback|AssertionError|✗|npm ERR!}"

  # LATEST PER KIND FIRST; everything else is superseded and collapsed.
  #
  # This used to dump `evidence[]` whole — every capture ever taken, in one flat table, with
  # no marking of which rows anything actually read. On a real client run that was 31 rows
  # for 7 kinds across 5 fingerprints, six of them ❌, and the six failures were all stale
  # intermediate states that a later capture of the same kind had already fixed. Nothing in
  # the table said so, so the run's author wrote a paragraph underneath explaining which
  # failures didn't count — hand-authored prose doing the job of a renderer, inside the block
  # that is supposed to be the un-authored half of the PR (and which the sha now freezes).
  #
  # The gate and `E1` only ever read ONE capture per kind: the latest. So that is the table,
  # and the rest goes into a <details> labelled for what it is. Nothing is hidden — a
  # superseded failure is still one click away, and still says ❌ — but a reviewer can see at
  # a glance what the current tree proves without being told in prose which rows to ignore.
  ev_rows() { # ev_rows latest|earlier
    jq -r --arg fp "$fail_pat" --arg want "$1" '
      # A captured command can be a whole multi-line shell block (cd / setup / the
      # actual test), not a one-liner. `.cmd` is the RAW command verbatim — good for
      # the JSON record, fatal for a GFM table cell: a literal newline ends the row,
      # so everything after the first line loses its leading "|" and renders as a
      # bare text blob with no visible Command/Result/Fingerprint columns. Collapse
      # to one line for display only; the full command still lives in the record.
      def display_cmd:
        gsub("\r\n|\r|\n"; "; ")
        | gsub("[ \t]+"; " ")
        | sub("^[ \t;]+"; "") | sub("[ \t;]+$"; "")
        | if (length > 240) then .[0:240] + " …" else . end;
      (.evidence // []) as $ev
      | ($ev | last | .fingerprint // "") as $final
      | ($ev | to_entries | group_by(.value.kind) | map(last | .key)) as $keep
      | $ev | to_entries[]
      # Bind the index first: `index(.key)` would evaluate `.key` against $keep (the array
      # index() is reading), not against this entry.
      | .key as $i
      | (($keep | index($i)) != null) as $is_latest
      | select(if $want == "latest" then $is_latest else ($is_latest | not) end)
      | .value
      | (if (.verdict // "") != "" then (.verdict == "fail")
         else ((.response // "") | test($fp)) end) as $failed
      # A VACUOUS capture is a pass that checked nothing — an empty build, or a command that
      # only installed/located the tool. Rendering it ✅ beside a real suite is how a client
      # PR certified `go-build ✅ pass` for a build that compiled zero packages. Same row,
      # different symbol: the gate and the audit treat these as unproven, and the PR must not
      # read stronger than they do.
      | (if $failed then "❌ FAIL"
         elif (.vacuous // false) then "⚠️ passed, verified nothing"
         else "✅ pass" end) as $result
      # A latest capture at an OLD fingerprint is the staleness E1 exists to catch: it proves
      # a tree that no longer exists. Say so in the row rather than leaving a reader to
      # compare twelve hex characters by eye.
      | (if ($want == "latest") and $final != "" and (.fingerprint // "") != $final
         then " ⏱ stale" else "" end) as $stale
      | "| \(.kind // "?") | `\((.cmd // "?") | display_cmd | gsub("\\|";"\\|"))` | \($result) | `\((.fingerprint // "?")[0:12])`\($stale) |"
    ' "$rec" 2>/dev/null; }

  n_earlier="$(jq -r '(.evidence // []) as $ev
                      | (($ev | to_entries | group_by(.value.kind) | map(last.key)) | length) as $k
                      | ($ev | length) - $k' "$rec" 2>/dev/null || echo 0)"
  printf '| Kind | Command | Result | Fingerprint |\n|---|---|---|---|\n'
  ev_rows latest || printf '| _(evidence unreadable)_ | | | |\n'
  printf '\n_Latest capture per kind — the rows the Stop gate and `E1` actually read._\n'
  if [ "${n_earlier:-0}" -gt 0 ]; then
    printf '\n<details><summary>%s earlier capture(s) — superseded by a later run of the same kind</summary>\n\n' "$n_earlier"
    printf '| Kind | Command | Result | Fingerprint |\n|---|---|---|---|\n'
    ev_rows earlier
    printf '\n_Kept because the record is append-only. A ❌ here was an intermediate state, not the state this PR ships — the row above it, for the same kind, is what the gate read. A ❌ that was deliberate (a negative test whose command MUST fail) is not something any artifact can distinguish; say so in your own section below the auto block._\n'
    printf '</details>\n'
  fi
else
  printf '_No evidence rows recorded (evidence hooks not enabled, or none captured)._\n'
fi

# Compliance warnings — silent-omission tripwires (empty evidence / no review on record).
printf '\n#### Compliance warnings\n\n'
if [ "${#warnings[@]}" -gt 0 ]; then
  for _w in "${warnings[@]}"; do printf -- '- ⚠️ %s\n' "$_w"; done
else
  printf -- '- ✅ A code-review activation and at least one evidence row are on record.\n'
fi

# Discipline audit — the pipeline invariants, re-derived from artifacts by track-audit.sh
# and reproduced here so a REVIEWER can see them without running anything. A gate whose
# result only ever appeared in the author's terminal is a gate the reviewer has to take on
# trust, which is the thing this whole bundle refuses to do.
# Degrades quietly: an older install without track-audit.sh simply omits the section, and
# --warn-only keeps a failing audit from breaking report rendering (this script stays
# read-only and non-blocking; the audit does its own blocking at the Stop gate and Step 8).
#
# DEFINED HERE, RENDERED LAST (see the call below the run stats). It is the verdict on the
# run, not a detail of the mechanical dump, so it reads at `###` — a peer of the auto
# block's `### Run` and of the model's `### Asserted` zone rather than a subsection of
# either. It stays INSIDE the auto-block markers because it is machine-derived, and it sits
# at the end of that block so it is the last un-authored thing a reviewer sees before the
# model's narrative begins: its whole function is to calibrate how much of that narrative
# to believe, which only works if it is read first.
render_discipline_audit() {
_audit="$(cd "${BASH_SOURCE[0]%/*}" && pwd)/track-audit.sh"
if [ -f "$_audit" ]; then
  _aj="$(RUN_ID="$run_id" RUNS_DIR="$RUNS_DIR" bash "$_audit" --json --warn-only 2>/dev/null || true)"
  if [ -n "$_aj" ] && printf '%s' "$_aj" | jq -e '.summary' >/dev/null 2>&1; then
    printf '\n---\n\n### Discipline audit — mechanical invariants (derived from artifacts, not claimed)\n\n'
    printf -- '- %s\n' "$(printf '%s' "$_aj" | jq -r '
      "**\(.summary.pass) passed · \(.summary.warn) warning(s) · \(.summary.fail) failure(s)**"
      + (if .summary.fail > 0 then "  ❌ **blocking — this PR should not have been opened**"
         elif .summary.warn > 0 then "  ⚠️ review the warnings below"
         else "  ✅ every *mechanically-checkable* invariant holds" end)')"
    # Scope caveat — ALWAYS visible, never tucked inside the <details> below. A reader who
    # sees only a green count reads it as "the run followed the pipeline"; what it actually
    # means is "nothing derivable from an artifact contradicts that". The gap between those
    # two sentences is where every silent failure this bundle exists to catch actually
    # lives, so it is stated inline rather than left one click away.
    printf -- '- ⚠️ **Not a clean bill of health.** %s further invariant(s) were checked by **no machine** — including whether subagent briefs carried governance *content* rather than filenames, and whether the bundle was re-read from disk after a compaction. A clean audit is necessary, not sufficient.\n' \
      "$(printf '%s' "$_aj" | jq -r '.manual | length')"
    if printf '%s' "$_aj" | jq -e '[.checks[] | select(.verdict != "PASS")] | length > 0' >/dev/null 2>&1; then
      printf '\n| | Check | Finding | How to clear it |\n|---|---|---|---|\n'
      printf '%s' "$_aj" | jq -r '
        .checks[] | select(.verdict != "PASS") |
        "| \(if .verdict == "FAIL" then "❌" else "⚠️" end) | `\(.id)` | \(.message | gsub("\\|";"\\|")) | \(.remediation | gsub("\\|";"\\|")) |"'
    fi
    # The honesty rule carries into the PR: say what was NOT checked, so a green audit is
    # never mistaken for a full verification.
    printf '\n<details><summary>⚠️ The %s invariants NOT checked mechanically — a human must confirm these</summary>\n\n' \
      "$(printf '%s' "$_aj" | jq -r '.manual | length')"
    printf '%s' "$_aj" | jq -r '.manual[] | "- **\(.id)** — \(.check)"'
    # Second-order caveat: even some PASSing rows above rest on stamps the model wrote
    # itself. Naming which ones keeps a reviewer from reading the whole table as one
    # uniform grade of proof.
    printf '\n_Also note the checks above are not all equally strong. `I1`/`I2`/`T2` (git state), `G2`/`G4`/`G5` (bundle vs. the real diff), `M1` (hook-written `trace[]`), `T1`/`E1`/`E2` (hook-written `evidence[]`), `I4` (hook-written `compactions[]`/`governance_reads[]`/`briefs[]`) and `G6` (hook-written `briefs[]` — the brief text as the dispatch tool received it) are derived from artifacts the model does not author. `P1`/`P2` (phase stamps) and `F1` (terminal status) read stamps the model wrote itself via `track-note.sh` — they detect an omitted step, not a misreported one. `G1` and `G3` are MIXED: both read model-written governance stamps. `G1` re-hashes the file it points at, so the content is real but the pointer is chosen; `G3` compares the model'"'"'s own stamps against hook-written `trace[]`, so lowering one satisfies it — read `G3`'"'"'s message, which says whether a hook-observed bundle read corroborated the ordering. `G6` is the one check that observes the brief itself, which is why `A5` narrowed from "did content make the hop" to "was it the right content"._\n'
    printf '\n_Full list: `tests/prompt-level-checklist.md`. A clean audit is necessary, not sufficient._\n'
    printf '</details>\n'
  fi
fi
}

# Mechanical run stats.
printf '\n#### Run stats (hook-observed)\n\n'
printf -- '- **Tool calls:** %s\n' "${tool_calls:-0}"
te="$(jq -r '.token_estimate // empty' "$rec" 2>/dev/null || true)"
if [ -n "$te" ]; then
  tm="$(jq -r '.token_estimate_method // ""' "$rec" 2>/dev/null || true)"
  printf -- '- **Token estimate (rough):** ~%s  *(method: %s)*\n' "$te" "$tm"
fi
# Dispatches — WHAT each subagent was asked to do, and how much governance its brief
# carried. This leads because the raw trace below answers "how many agents ran" and
# nothing else: on a surface that leaves `agent_description` empty it degenerates into N
# identical `SubagentStart general-purpose (a3c8254…)` rows, which tells a reviewer
# nothing about what was delegated. track-brief.sh sees the dispatch tool's own
# `description` at PreToolUse, so it is available even when the trace's reason is not.
if [ -f "$rec" ] && [ "$(jq -r '.briefs | length' "$rec" 2>/dev/null || echo 0)" -gt 0 ]; then
  printf -- '- **Subagent dispatches (what each was asked to do — brief text as the tool received it):**\n'
  jq -r '.briefs[]
         | "  - \(.t): \(.subagent_type // .tool // "dispatch")"
           + ((.desc // "") | if . == "" then " — *(no description supplied)*" else " — \(.)" end)
           + " · governance: "
           + (if (.declared_na // false) then "declared n/a"
              elif (.thin // false)      then "**NONE of \(.lines_total) constraint lines** ⚠️"
              else "\(.lines_matched)/\(.lines_total) constraint lines"
                   + ((.sections // []) | if length == 0 then "" else " (\(join(", ")))" end)
                   + (if (.below_min // false) then " — thin ⚠️" else "" end)
              end)' \
    "$rec" 2>/dev/null || true
fi
if [ -f "$rec" ] && [ "$(jq -r '.trace | length' "$rec" 2>/dev/null || echo 0)" -gt 0 ]; then
  printf -- '- **Subagent lifecycle trace (in order):**\n'
  jq -r '.trace[] | "  - \(.t): \(.event) \(.agent_type // .agent_display_name // "") \((.agent_id // "") | if . == "" then "" else "(\(.))" end)\((.reason // .stop_reason // "") | if . == "" then "" else " — \(.)" end)"' \
    "$rec" 2>/dev/null || true
  # An empty reason column is a WIRING fact, not an absence of purpose — say which, so
  # nobody reads a bare trace as "the agents had no stated goal".
  if [ "$(jq -r '[.trace[]? | select((.reason // "") != "")] | length' "$rec" 2>/dev/null || echo 0)" -eq 0 ] \
     && [ "$(jq -r '.briefs | length' "$rec" 2>/dev/null || echo 0)" -eq 0 ]; then
    printf -- '  - _No "why" recorded for any agent: this surface did not supply `agent_description` on `SubagentStart`, and `track-brief.sh` is not wired (run `install-hooks.sh --apply`). Wire it and the dispatch list above fills in._\n'
  fi
fi

# Self-reported — clearly fenced off from the mechanical facts above.
if [ -f "$rec" ] && { [ "$(jq -r '.skills | length' "$rec" 2>/dev/null || echo 0)" -gt 0 ] || [ "${iterations:-0}" -gt 0 ]; }; then
  printf '\n#### Trace (self-reported — model claim, not hook-observed)\n\n'
  if [ "$(jq -r '.skills | length' "$rec" 2>/dev/null || echo 0)" -gt 0 ]; then
    printf -- '- **Skill activations (in order):**\n'
    jq -r '.skills[] | "  - \(.t): \(.skill)\((.step // "") | if . == "" then "" else " — \(.)" end)"' \
      "$rec" 2>/dev/null || true
  fi
  [ "${iterations:-0}" -gt 0 ] && printf -- '- **Iterations (RED→GREEN→review cycles):** %s\n' "$iterations"
fi

# The verdict, last in the machine-rendered zone — see the note above its definition.
render_discipline_audit
printf '\n'
} > "$_ab"

# The sha covers everything from the BEGIN marker to the line before END, exactly as
# --verify-body recomputes it. Emitted together so a body pasted verbatim always verifies.
cat "$_ab"
printf '<!-- END track-report auto block · sha=%s -->\n' "$(block_sha < "$_ab")"
exit 0
