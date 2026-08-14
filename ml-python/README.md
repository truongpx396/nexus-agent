# ml-python

Python helper project for the Nexus Agent platform (eval runner, LLM-as-judge harness,
and related tooling). Managed with [uv](https://docs.astral.sh/uv/).

## Setup

```bash
uv sync
```

## Common commands

```bash
uv run pytest        # run tests
uv run ruff check .  # lint
uv run ruff format .  # format
```

This package is a scaffold as of Phase 1 (Setup); the eval runner and judge harness land in
Phase 2 (see `specs/001-agent-platform/tasks.md`, tasks T026e+).
