# track-env.base.sh — repo-wide COMMITTED hook preset (single-branch-development bundle).
# Auto-seeded by install-hooks.sh from the detected repo stack. SAFE TO EDIT + COMMIT.
#
# Context this was generated for: spec-driven repo — active plan: specs/001-agent-platform/plan.md
# (governed by a project constitution). Stack after Phase 1: Go (backend-go/), Python+uv
# (ml-python/), React+Vite+TS (frontend/), docker compose (Postgres/Redis), GitHub Actions.
#
# TWO CATEGORIES (the tag is documentation, not part of the name):
#   [TASK-DERIVED] preflight proposes per run; confirm at Step 1. Left EMPTY here so an
#                  unedited copy fails LOUD (guard denies all edits) — never silently wrong.
#   [REPO-POLICY]  repo-wide constant; set once, do not regenerate per run.
# Precedence: exported env > worktree track-env.sh > this file > script default.

# --- guard: writable scope + frozen entrypoints [TASK-DERIVED — set per run] -
export TRACK_ALLOWED_PREFIXES="${TRACK_ALLOWED_PREFIXES:-}"          # colon-separated path prefixes this branch may edit. EMPTY ⇒ guard fails closed. RUNS_DIR is always writable and must NOT be listed here.
export TRACK_FROZEN_PATHS="${TRACK_FROZEN_PATHS:-}"                  # exact files no branch may edit. Freeze shared entrypoints (e.g. backend-go/cmd/control-plane/main.go) once they exist and tracks must self-register.
# Repo-root-relative, ALWAYS — the guard compares against the path relative to the git
# worktree root, never to its own CWD. The old value "migrations/" therefore never matched
# backend-go/migrations/... and the append-only rule was silently inert.
export TRACK_IMMUTABLE_PREFIXES="${TRACK_IMMUTABLE_PREFIXES:-backend-go/migrations/}"  # [REPO-POLICY] committed files here are append-only.
export TRACK_GUARD_DESTRUCTIVE="${TRACK_GUARD_DESTRUCTIVE:-1}"       # [REPO-POLICY] deny DROP TABLE/TRUNCATE TABLE/FLUSHALL/rm -rf. Heredoc bodies are data and are not scanned.
# Empty is the correct default: the guard allows a worker to publish its OWN branch once so
# `gh pr create` can reach the remote. Set to 1 ONLY for an sso-pr-review-feedback flow that
# updates an ALREADY-published PR branch — and prefer `TRACK_ALLOW_FF_PUSH=1 <cmd>` for the
# one command over turning it on repo-wide.
export TRACK_ALLOW_FF_PUSH="${TRACK_ALLOW_FF_PUSH:-}"                # [REPO-POLICY] 1 ONLY for a PR-rework flow.
export TRACK_DEFAULT_BRANCH="${TRACK_DEFAULT_BRANCH:-main}"          # [REPO-POLICY] never publishable by a worker; also the branch I1 flags work on.

# --- run state ---------------------------------------------------------------
export RUNS_DIR="${RUNS_DIR:-runs}"                        # [REPO-POLICY] run record dir — MUST be gitignored. Always writable by the guard (bundle, PR body, run record).

# --- preflight ---------------------------------------------------------------
export PREFLIGHT_REQUIRE_GH="${PREFLIGHT_REQUIRE_GH:-1}"            # [REPO-POLICY] require authenticated gh (0 to waive on setup runs).
export PREFLIGHT_REQUIRE_TOOLCHAIN="${PREFLIGHT_REQUIRE_TOOLCHAIN:-}" # [TASK-DERIVED] per-task bins on PATH, e.g. "go,python3,node,npm,docker".

# --- dependency version-lock (skill-deps.json) + probe cache [REPO-POLICY] ---
export TRACK_DEPS_CACHE_TTL_HOURS="${TRACK_DEPS_CACHE_TTL_HOURS:-72}" # cache the version probe this many hours (0 = always re-check).
export TRACK_DEPS_STRICT="${TRACK_DEPS_STRICT:-0}"                  # 1 = an out-of-range pinned version hard-fails preflight; 0 = warn.
export TRACK_DEPS_MANIFEST="${TRACK_DEPS_MANIFEST:-}"              # path to skill-deps.json; empty = auto (beside the hooks).

# --- evidence gate (CATALOG seeded from the real stack — [REPO-POLICY]) -------
# label:pattern pairs, ';'-separated; the pattern is matched against the COMMAND. Labels
# here MUST match the kinds used in TRACK_EVIDENCE_RULES / TRACK_REQUIRED_EVIDENCE.
export TRACK_EVIDENCE_KINDS="${TRACK_EVIDENCE_KINDS:-go-build:go build;go-test:go test;go-lint:golangci-lint;py-lint:ruff check|ruff format --check;py-test:pytest;frontend-build:npm run build;frontend-lint:npm run lint;compose-config:docker compose config|docker-compose config;ci-lint:actionlint|yamllint;make-check:make -n|make --dry-run}"
# `go-lint` covers `golangci-lint run` AND `golangci-lint config verify` — the latter is the
# honest check for a lint config on a tree with no sources yet, where `run` has nothing to do.
# Diff-conditional requirement: touching a path makes its kind mandatory at Stop. This is
# the line that would have caught Phase 1's real defect — docker-compose.yml, ci.yml and
# the Makefile (~300 lines, the highest-risk governance surface) shipped with NO evidence
# because the floor only named go-build/py-lint/frontend-build, and the one governance
# violation that reached the PR (7 of 9 CI jobs missing timeout-minutes) was in that
# unverified cluster. Globs are shell patterns where `*` spans `/`.
# Manifests and lockfiles are listed explicitly: a dependency bump changes what the code
# compiles against without touching a single source file, and a scaffold phase is almost
# ENTIRELY manifests — Phase 1's diff was go.mod/go.sum/pyproject.toml/uv.lock/package.json,
# none of which would demand evidence from the source-extension rules alone.
export TRACK_EVIDENCE_RULES="${TRACK_EVIDENCE_RULES:-*.go:go-build;backend-go/go.mod:go-build;backend-go/go.sum:go-build;backend-go/.golangci.yml:go-lint;.golangci.yml:go-lint;*.py:py-lint;ml-python/pyproject.toml:py-lint;ml-python/uv.lock:py-lint;*.ts:frontend-build;*.tsx:frontend-build;frontend/package.json:frontend-build;frontend/package-lock.json:frontend-build;docker-compose.yml:compose-config;.env.example:compose-config;.github/workflows/*:ci-lint;Makefile:make-check}"
# Prose-only diffs can never satisfy a code requirement. ALL-or-nothing: one code file
# anywhere in the diff restores the full requirement set, so this cannot smuggle code past.
export TRACK_EVIDENCE_SKIP_GLOBS="${TRACK_EVIDENCE_SKIP_GLOBS:-*.md;docs/*;specs/*;.specify/*;runs/*}"
export TRACK_REQUIRED_EVIDENCE="${TRACK_REQUIRED_EVIDENCE:-}"        # [TASK-DERIVED] kinds required on EVERY diff (floor); empty = rules-only, which is usually right now that RULES cover the stack.
export TRACK_BASE_REF="${TRACK_BASE_REF:-origin/main}"                 # [REPO-POLICY] real base or a committed diff looks empty and passes silently.
export TRACK_VACUOUS_PATTERN="${TRACK_VACUOUS_PATTERN:-}"            # [REPO-POLICY] empty = built-in list ("matched no packages", "[no test files]", "No tests found", …). A matching capture stays `pass` but is flagged `vacuous:true` — it verified nothing.

# --- ceilings / hardening ----------------------------------------------------
export TRACK_MAX_TOOL_CALLS="${TRACK_MAX_TOOL_CALLS:-600}"          # [REPO-POLICY] tool-call hard stop. The Phase 1 scaffold used 370.
export TRACK_MAX_TOKEN_ESTIMATE="${TRACK_MAX_TOKEN_ESTIMATE:-3000000}" # [REPO-POLICY] transcript ceiling; blocks Stop + writes status:budget-exceeded. 0 disables. The Phase 1 scaffold measured 1,481,446 new tokens (against 66.1M cache reads).
export TRACK_SELF_HEAL_ATTEMPTS="${TRACK_SELF_HEAL_ATTEMPTS:-2}"    # [REPO-POLICY] retries per DISTINCT failure before halting `blocked`. Prompt-enforced; here so the number survives a context compaction.
export TRACK_SENTINEL="${TRACK_SENTINEL:-1}"                        # [REPO-POLICY] scan staged diff for secrets/leftovers.
# Makes track-audit.sh a BLOCKING Stop gate instead of an advisory CLI report. Unset in the
# previous version, so every discipline finding (governance pinning, brief coverage, phase
# sequence, evidence convergence) was reported and then ignored. If a session legitimately
# cannot end, this is the ONE line to blank out.
export TRACK_AUDIT="${TRACK_AUDIT:-1}"                              # [REPO-POLICY] 1 = audit blocks Stop on FAIL.

# --- notify (optional) -------------------------------------------------------
export TRACK_NOTIFY_WEBHOOK="${TRACK_NOTIFY_WEBHOOK:-}"             # [REPO-POLICY] terminal-state webhook; empty = no notify.
