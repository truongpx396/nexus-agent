# ml-python

ML/eval helper package for the Nexus Agent platform.

This is a Phase 1 bootstrap scaffold: it declares no runtime dependencies yet.
The eval-runner and LLM-judge dependencies land in Phase 2 Foundational.

## Getting started

Install [uv](https://docs.astral.sh/uv/) if you don't already have it, then from
this directory:

```bash
uv sync
```

Run the test suite:

```bash
uv run pytest
```

Lint the code:

```bash
uv run ruff check .
```
