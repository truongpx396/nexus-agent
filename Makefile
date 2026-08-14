SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

# -----------------------------------------------------------------------------
# Nexus Agent Platform — root Makefile
#
# Single documented entrypoint for local dev + CI (devops-cicd.instructions.md).
# Phase 1 (Setup): the quickstart-referenced targets below are honest no-ops
# until their implementing task lands — each names the task/phase that will
# wire it up (T008). Toolchain targets (fmt-*/lint-*/build-*/test-*) already
# work against whichever of backend-go/, ml-python/, frontend/ has real
# source, and skip gracefully when a stack has none yet.
# -----------------------------------------------------------------------------

TENANT ?=

.PHONY: help
help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk -F':.*?## ' '{printf "  \033[36m%-22s\033[0m %s\n", $$1, $$2}'

# -----------------------------------------------------------------------------
# Quickstart stubs (T008) — honest no-ops, no real implementation yet. Each
# names the task/phase that will implement it. See
# specs/001-agent-platform/quickstart.md and specs/001-agent-platform/tasks.md.
# -----------------------------------------------------------------------------

.PHONY: migrate
migrate: ## [stub] Apply DB migrations incl. RLS policies (real impl: Phase 2 data-model tasks)
	@echo "migrate: stub — real migrations land with the Phase 2 data-model tasks. No-op."

.PHONY: seed-tenant
seed-tenant: ## [stub] Seed one tenant + agent + demo skill — usage: make seed-tenant TENANT=acme
	@echo "seed-tenant: stub for TENANT=$(TENANT) — real seeding lands with the Phase 2 tenant/onboarding tasks. No-op."

.PHONY: run-control-plane
run-control-plane: ## [stub] Run the control plane (auth, RBAC, budgets, routing) (real impl: backend-go control-plane tasks)
	@echo "run-control-plane: stub — real control-plane service lands with the backend-go control-plane tasks. No-op."

.PHONY: run-worker
run-worker: ## [stub] Run the stateless kernel worker (real impl: backend-go kernel-worker tasks)
	@echo "run-worker: stub — real kernel worker lands with the backend-go kernel-worker tasks. No-op."

.PHONY: evals
evals: ## [stub] Run the eval suite (k trials/case, three-valued verdict) (real impl: Phase 2 T026e-T026t)
	@echo "evals: stub — real eval runner lands in Phase 2 (T026e-T026t). No-op."

.PHONY: evals-calibrate
evals-calibrate: ## [stub] Calibrate judge vs human-labelled gold set (real impl: Phase 2 T026e-T026t)
	@echo "evals-calibrate: stub — real judge calibration lands in Phase 2 (T026e-T026t). No-op."

.PHONY: evals-baseline
evals-baseline: ## [stub] Record eval baseline + environment digest (real impl: Phase 2 T026e-T026t)
	@echo "evals-baseline: stub — real baseline recording lands in Phase 2 (T026e-T026t). No-op."

# -----------------------------------------------------------------------------
# Source-detection helper. NOT `compgen -G 'dir/**/*.ext'`: that relies on
# bash's `globstar` shopt for recursive **, which does not exist on bash 3.2
# (macOS's system /bin/bash — this Makefile must work there, not just on
# CI's modern-bash ubuntu runners). `find -print -quit` is portable and
# short-circuits on the first match.
# -----------------------------------------------------------------------------
has_src = find $(1) \( -name node_modules -o -name .venv -o -name .git \) -prune -o -name '$(2)' -print -quit 2>/dev/null | grep -q .

# -----------------------------------------------------------------------------
# Go (backend-go/)
# -----------------------------------------------------------------------------

.PHONY: fmt-go
fmt-go: ## Format Go sources
	@if $(call has_src,backend-go,*.go); then \
	  cd backend-go && gofmt -l . && \
	  if command -v goimports > /dev/null 2>&1; then goimports -l . ; \
	  else echo 'goimports not installed — skipping import-order check'; fi ; \
	else \
	  echo 'fmt-go: no Go sources under backend-go/ yet — skipping.'; \
	fi

.PHONY: lint-go
lint-go: ## Lint Go sources
	@if $(call has_src,backend-go,*.go); then \
	  cd backend-go && golangci-lint run ./... ; \
	else \
	  echo 'lint-go: no Go sources under backend-go/ yet — skipping.'; \
	fi

.PHONY: build-go
build-go: ## Build Go binaries
	@if $(call has_src,backend-go,*.go); then \
	  cd backend-go && go build ./... ; \
	else \
	  echo 'build-go: no Go sources under backend-go/ yet — skipping.'; \
	fi

.PHONY: test-go
test-go: ## Test Go sources
	@if $(call has_src,backend-go,*.go); then \
	  cd backend-go && go test -race ./... ; \
	else \
	  echo 'test-go: no Go sources under backend-go/ yet — skipping.'; \
	fi

# -----------------------------------------------------------------------------
# Python (ml-python/) — UV only, never bare pip (python.instructions.md)
# -----------------------------------------------------------------------------

.PHONY: fmt-py
fmt-py: ## Format Python sources
	@if $(call has_src,ml-python,*.py); then \
	  cd ml-python && uv run ruff format . ; \
	else \
	  echo 'fmt-py: no Python sources under ml-python/ yet — skipping.'; \
	fi

.PHONY: lint-py
lint-py: ## Lint Python sources
	@if $(call has_src,ml-python,*.py); then \
	  cd ml-python && uv run ruff check . ; \
	else \
	  echo 'lint-py: no Python sources under ml-python/ yet — skipping.'; \
	fi

.PHONY: test-py
test-py: ## Test Python sources
	@if $(call has_src,ml-python,*.py); then \
	  cd ml-python && \
	  if $(call has_src,tests,*.py) || $(call has_src,.,test_*.py); then \
	    uv run pytest ; \
	  else \
	    echo 'test-py: no tests found yet — skipping.'; \
	  fi ; \
	else \
	  echo 'test-py: no Python sources under ml-python/ yet — skipping.'; \
	fi

# -----------------------------------------------------------------------------
# Frontend (frontend/)
# -----------------------------------------------------------------------------

.PHONY: fmt-ts
fmt-ts: ## Format frontend sources
	@if $(call has_src,frontend,*.tsx) || $(call has_src,frontend,*.ts); then \
	  cd frontend && npm run format ; \
	else \
	  echo 'fmt-ts: no frontend sources under frontend/ yet — skipping.'; \
	fi

.PHONY: lint-ts
lint-ts: ## Lint frontend sources
	@if $(call has_src,frontend,*.tsx) || $(call has_src,frontend,*.ts); then \
	  cd frontend && npm run lint ; \
	else \
	  echo 'lint-ts: no frontend sources under frontend/ yet — skipping.'; \
	fi

.PHONY: build-ts
build-ts: ## Build frontend
	@if $(call has_src,frontend,*.tsx) || $(call has_src,frontend,*.ts); then \
	  cd frontend && npm run build ; \
	else \
	  echo 'build-ts: no frontend sources under frontend/ yet — skipping.'; \
	fi

.PHONY: test-ts
test-ts: ## Test frontend
	@echo 'test-ts: no frontend test suite yet.'

# -----------------------------------------------------------------------------
# Aggregates
# -----------------------------------------------------------------------------

.PHONY: fmt
fmt: fmt-go fmt-py fmt-ts ## Format all toolchains

.PHONY: lint
lint: lint-go lint-py lint-ts ## Lint all toolchains

.PHONY: build
build: build-go build-ts ## Build all toolchains (no python build step)

.PHONY: test
test: test-go test-py test-ts ## Run all test suites (also satisfies T008's `test` stub — see deviation note)

.PHONY: ci
ci: lint build test ## Full local gate — mirrors what GitHub Actions runs
