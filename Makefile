SHELL := bash
.SHELLFLAGS := -eu -o pipefail -c
.DEFAULT_GOAL := help

.PHONY: help migrate seed-tenant run-control-plane run-worker evals evals-calibrate evals-baseline test

help: ## List available targets
	@grep -E '^[a-zA-Z_-]+:.*?## ' $(MAKEFILE_LIST) | \
	  awk -F':.*?## ' '{printf "  \033[36m%-20s\033[0m %s\n", $$1, $$2}'

migrate: ## Apply database migrations, incl. RLS policies — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

seed-tenant: ## Seed one tenant + agent + demo skill. Usage: make seed-tenant TENANT=acme — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md (TENANT=$(TENANT))"
	@exit 0

run-control-plane: ## Run the control plane: auth, RBAC, budgets, routing — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

run-worker: ## Run the stateless kernel worker — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

evals: ## Run the eval suite — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

evals-calibrate: ## Calibrate eval judges/thresholds — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

evals-baseline: ## Record/update the eval baseline — stub for now
	@echo "not yet implemented — see specs/001-agent-platform/tasks.md"
	@exit 0

test: ## Run all test suites (guarded — skips a toolchain until its dir/tool exists)
	@echo "== backend-go =="
	@if [ -d backend-go ] && command -v go >/dev/null 2>&1; then \
	  (cd backend-go && go test ./...); \
	else \
	  echo "skip: backend-go not present yet"; \
	fi
	@echo "== ml-python =="
	@if [ -d ml-python ] && command -v uv >/dev/null 2>&1; then \
	  (cd ml-python && uv run pytest); \
	else \
	  echo "skip: ml-python not present yet"; \
	fi
	@echo "== frontend =="
	@if [ -d frontend ] && command -v npm >/dev/null 2>&1; then \
	  (cd frontend && npm run test --if-present); \
	else \
	  echo "skip: frontend not present yet"; \
	fi
