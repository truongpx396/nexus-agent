# track-env.base.sh — repo-wide COMMITTED hook preset (single-branch-development bundle).
# Auto-seeded by install-hooks.sh from the detected repo stack. SAFE TO EDIT + COMMIT.
#
# Context this was generated for: spec-driven repo — active plan: specs/001-agent-platform/plan.md (governed by a project constitution)
# Detected toolchain: <none detected>
#
# TWO CATEGORIES (the tag is documentation, not part of the name):
#   [TASK-DERIVED] preflight proposes per run; confirm at Step 1. Left EMPTY here so an
#                  unedited copy fails LOUD (guard denies all edits) — never silently wrong.
#   [REPO-POLICY]  repo-wide constant; set once, do not regenerate per run.
# Precedence: exported env > worktree track-env.sh > this file > script default.

# --- guard: writable scope + frozen entrypoints [TASK-DERIVED — set per run] -
export TRACK_ALLOWED_PREFIXES="${TRACK_ALLOWED_PREFIXES:-}"          # colon-separated path prefixes this branch may edit. EMPTY ⇒ guard fails closed.
export TRACK_FROZEN_PATHS="${TRACK_FROZEN_PATHS:-}"                  # exact files no branch may edit (leave empty on bootstrap).
export TRACK_IMMUTABLE_PREFIXES="${TRACK_IMMUTABLE_PREFIXES:-migrations/}"  # [REPO-POLICY] committed files here are append-only.
export TRACK_GUARD_DESTRUCTIVE="${TRACK_GUARD_DESTRUCTIVE:-1}"       # [REPO-POLICY] deny DROP/TRUNCATE/FLUSHALL/rm -rf.
export TRACK_ALLOW_FF_PUSH="${TRACK_ALLOW_FF_PUSH:-}"               # [REPO-POLICY] 1 ONLY for a PR-rework flow.

# --- run state ---------------------------------------------------------------
export RUNS_DIR="${RUNS_DIR:-runs}"                        # [REPO-POLICY] run record dir — MUST be gitignored.

# --- preflight ---------------------------------------------------------------
export PREFLIGHT_REQUIRE_GH="${PREFLIGHT_REQUIRE_GH:-1}"            # [REPO-POLICY] require authenticated gh (0 to waive on setup runs).
export PREFLIGHT_REQUIRE_TOOLCHAIN="${PREFLIGHT_REQUIRE_TOOLCHAIN:-}" # [TASK-DERIVED] per-task bins on PATH (detected repo-wide: none).

# --- dependency version-lock (skill-deps.json) + probe cache [REPO-POLICY] ---
export TRACK_DEPS_CACHE_TTL_HOURS="${TRACK_DEPS_CACHE_TTL_HOURS:-72}" # cache the version probe this many hours (0 = always re-check).
export TRACK_DEPS_STRICT="${TRACK_DEPS_STRICT:-0}"                  # 1 = an out-of-range pinned version hard-fails preflight; 0 = warn.
export TRACK_DEPS_MANIFEST="${TRACK_DEPS_MANIFEST:-}"              # path to skill-deps.json; empty = auto (beside the hooks).

# --- evidence gate (CATALOG seeded from detected stack — [REPO-POLICY]) -------
export TRACK_EVIDENCE_KINDS="${TRACK_EVIDENCE_KINDS:-}"      # label:pattern pack.
export TRACK_EVIDENCE_RULES="${TRACK_EVIDENCE_RULES:-}"      # diff-path glob → required kind.
export TRACK_REQUIRED_EVIDENCE="${TRACK_REQUIRED_EVIDENCE:-}"        # [TASK-DERIVED] kinds required on EVERY diff (floor); empty = rules-only.
export TRACK_BASE_REF="${TRACK_BASE_REF:-origin/main}"                 # [REPO-POLICY] real base or a committed diff looks empty and passes silently.

# --- ceilings / hardening ----------------------------------------------------
export TRACK_MAX_TOOL_CALLS="${TRACK_MAX_TOOL_CALLS:-200}"          # [REPO-POLICY] tool-call hard stop.
export TRACK_MAX_TOKEN_ESTIMATE="${TRACK_MAX_TOKEN_ESTIMATE:-200000}" # [REPO-POLICY] chars÷4 transcript ceiling; blocks Stop + writes status:budget-exceeded. 0 disables. Undercounts.
export TRACK_SELF_HEAL_ATTEMPTS="${TRACK_SELF_HEAL_ATTEMPTS:-2}"    # [REPO-POLICY] retries per DISTINCT failure before halting `blocked`. Prompt-enforced; here so the number survives a context compaction.
export TRACK_SENTINEL="${TRACK_SENTINEL:-1}"                        # [REPO-POLICY] scan staged diff for secrets/leftovers.

# --- notify (optional) -------------------------------------------------------
export TRACK_NOTIFY_WEBHOOK="${TRACK_NOTIFY_WEBHOOK:-}"             # [REPO-POLICY] terminal-state webhook; empty = no notify.
