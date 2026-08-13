SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c

# Nexus Agent — repo-root Makefile.
#
# Phase 1 (Setup) scaffolding only: this wires the lint/fmt/test tooling for
# the three sibling trees (backend-go, ml-python, frontend) and stubs the
# quickstart commands that depend on code/migrations that do not exist yet.
# See specs/001-agent-platform/tasks.md (T005, T006, T007, T008) and
# specs/001-agent-platform/quickstart.md for the commands this wires.
#
# NOTE: `test` and the `*-fmt`/`*-lint` targets below are REAL — they invoke
# the sibling clusters' actual toolchains (go, uv, npm) and will fail if run
# before those trees exist. `migrate`, `seed-tenant`, `run-control-plane`,
# `run-worker`, `evals`, `evals-calibrate`, and `evals-baseline` are HONEST
# NO-OP STUBS — the binaries/migrations they would run do not exist yet, so
# they print a pointer to the owning task and exit 0 rather than pretend to
# do something they can't.

.DEFAULT_GOAL := help

GO_DIR := backend-go
PY_DIR := ml-python
FE_DIR := frontend

.PHONY: help
help: ## List targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk -F':.*?## ' '{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

# ── Lint / format (T005, T006) ───────────────────────────────────────────────

.PHONY: lint
lint: lint-go lint-python lint-ts ## Run all linters (Go, Python, TypeScript)

.PHONY: lint-go
lint-go: ## Lint Go sources (golangci-lint)
	@if ! find $(GO_DIR) -name '*.go' -print -quit | grep -q .; then \
	  echo "lint-go: no .go files yet under $(GO_DIR) — skipping (see specs/001-agent-platform/tasks.md)."; \
	else \
	  cd $(GO_DIR) && golangci-lint run ./...; \
	fi

.PHONY: lint-python
lint-python: ## Lint Python sources (ruff)
	cd $(PY_DIR) && uv run ruff check .

.PHONY: lint-ts
lint-ts: ## Lint frontend sources (eslint, via the npm lint script)
	cd $(FE_DIR) && npm run lint

.PHONY: fmt
fmt: fmt-go fmt-python fmt-ts ## Check formatting (Go, Python, TypeScript); fails on drift, never writes

# fmt-go does NOT rely on `.SHELLFLAGS := -eu -o pipefail -c` alone for its
# error propagation: that flag is silently ignored by GNU Make < 3.82 (stock
# macOS ships 3.81), which would otherwise let a `var="$$(failing-cmd)"`
# substitution fail silently and report a false PASS. Every check below is
# explicit instead, so this target is correct on any Make version.
.PHONY: fmt-go
fmt-go: ## Check Go formatting (gofmt + goimports; no writes)
	@command -v goimports >/dev/null 2>&1 || { \
	  echo "goimports not found on PATH — install: go install golang.org/x/tools/cmd/goimports@latest" >&2; exit 1; }
	@cd $(GO_DIR) && bad="$$(gofmt -l .)" || exit 1; \
	  if [ -n "$$bad" ]; then echo "gofmt: needs formatting:"; echo "$$bad"; exit 1; fi
	@cd $(GO_DIR) && bad="$$(goimports -l .)" || exit 1; \
	  if [ -n "$$bad" ]; then echo "goimports: needs formatting:"; echo "$$bad"; exit 1; fi

.PHONY: fmt-python
fmt-python: ## Check Python formatting (ruff format --check; no writes)
	cd $(PY_DIR) && uv run ruff format --check .

.PHONY: fmt-ts
fmt-ts: ## Check frontend formatting (prettier --check, via the npm format:check script)
	cd $(FE_DIR) && npm run format:check

# ── Test (T008) ───────────────────────────────────────────────────────────────
# Real aggregator: the underlying tools exist and genuinely run, even though
# there is nothing but scaffolding to test in Phase 1. No frontend test job
# is wired here — frontend/package.json is only contracted to carry
# dev/build/lint/preview scripts; a test runner will be added when one lands.

.PHONY: test
test: test-go test-python ## Run all test suites (Go, Python)

.PHONY: test-go
test-go: ## Run Go tests
	@if ! find $(GO_DIR) -name '*.go' -print -quit | grep -q .; then \
	  echo "test-go: no .go files yet under $(GO_DIR) — skipping (see specs/001-agent-platform/tasks.md)."; \
	else \
	  cd $(GO_DIR) && go test ./...; \
	fi

.PHONY: test-python
test-python: ## Run Python tests
	@if ! find $(PY_DIR) -path '$(PY_DIR)/.venv' -prune -o \( -name 'test_*.py' -o -name '*_test.py' \) -print | grep -q .; then \
	  echo "test-python: no test files yet under $(PY_DIR) — skipping (see specs/001-agent-platform/tasks.md)."; \
	else \
	  cd $(PY_DIR) && uv run pytest; \
	fi

# ── Quickstart stubs (T008) ──────────────────────────────────────────────────
# Honest no-ops: nothing here fakes success or fails the build. Each prints
# what it would do and where the real implementation lands, then exits 0.

.PHONY: migrate
migrate: ## Apply database migrations (stub — not yet implemented)
	@echo "make migrate: not yet implemented — migrations land in backend-go/migrations/ (see specs/001-agent-platform/tasks.md T010)."
	@exit 0

.PHONY: seed-tenant
seed-tenant: ## Seed a demo tenant + agent + skill for local dev (stub — not yet implemented). Usage: make seed-tenant TENANT=acme
	@echo "make seed-tenant: not yet implemented — see specs/001-agent-platform/tasks.md T008 and quickstart.md Setup."
	@exit 0

.PHONY: run-control-plane
run-control-plane: ## Run the control-plane service (stub — not yet implemented)
	@echo "make run-control-plane: not yet implemented — entrypoint lands in backend-go/cmd/control-plane/ (see specs/001-agent-platform/tasks.md T075+)."
	@exit 0

.PHONY: run-worker
run-worker: ## Run the stateless kernel worker (stub — not yet implemented)
	@echo "make run-worker: not yet implemented — entrypoint lands in backend-go/cmd/runtime-worker/main.go (see specs/001-agent-platform/tasks.md T046)."
	@exit 0

.PHONY: evals
evals: ## Run the eval suite: k trials/case, per-case intervals, three-valued verdict (stub — not yet implemented)
	@echo "make evals: not yet implemented — eval runner lands in ml-python/src/evals/ (see specs/001-agent-platform/tasks.md T026e)."
	@exit 0

.PHONY: evals-calibrate
evals-calibrate: ## Calibrate the LLM judge against the human-labelled gold set (stub — not yet implemented)
	@echo "make evals-calibrate: not yet implemented — see specs/001-agent-platform/tasks.md T026m (judge calibration)."
	@exit 0

.PHONY: evals-baseline
evals-baseline: ## Record the current eval baseline + environment digest (stub — not yet implemented)
	@echo "make evals-baseline: not yet implemented — see specs/001-agent-platform/tasks.md T026i (eval environment digest)."
	@exit 0
