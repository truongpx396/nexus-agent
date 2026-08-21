# Migrations

This directory holds the raw-SQL schema migrations for nexus-agent's
Postgres database. There is no ORM and no migration framework -- migrations
are plain `.sql` files, applied in lexical filename order, and there is no
rollback/down file for this project (see "Forward-only" below).

## File naming and ordering

Files are named `NNNN_description.sql`, where `NNNN` is a zero-padded
4-digit sequence number (`0001`, `0002`, `0003`, ...). The sequence number is
the only thing that determines apply order -- files are applied by sorting
the directory listing lexically (`sort.Strings` on the glob result; see
"How migrations are applied" below), not by parsing dates or reading a
manifest.

The current set:

- `0001_config.sql` -- immutable config entities (tenants, users, agents,
  tools, models, price_books, meters, fx_rates, skills, connectors).
- `0002_runtime.sql` -- mutable, event-sourced runtime state (sessions,
  events, delegations, effect classes, orchestration plans, checkpoints,
  snapshots, idempotency claims, content access grants).
- `0003_rls.sql` -- Row-Level Security policies for every tenant-scoped
  table across both prior files, plus a small number of indexes flagged by
  review as missing on an already-defined table.

**Adding a new migration**: pick the next unused sequence number (currently
`0004`) and give it a short, descriptive name, e.g. `0004_billing_events.sql`.
Never reuse or renumber an existing file's sequence number -- once a
migration has shipped to any environment, its filename and contents are
frozen; a mistake is fixed by a new, later-numbered migration, not by
editing history.

## Expand/contract discipline (forward-only, additive-first)

Migrations in this repo are **forward-only**: there is no down-migration,
and a migration is never edited after it ships. Schema evolution follows the
**expand/contract** pattern:

1. **Expand**: add the new column/table/index. The old shape keeps working
   unchanged -- nothing that reads or writes the old shape breaks.
2. **Migrate**: deploy application code that writes to (and, once safe,
   reads from) the new shape, while the old shape is still present and still
   populated.
3. **Contract**: once every consumer has moved off the old shape, a later
   migration drops the now-unused column/table.

The concrete rule this implies: **a column (or table) is never dropped in
the same release that stops writing to it.** Expand and contract are always
at least one release apart. This is what makes a rolling deploy safe -- at
any instant during a rolling deploy, some fraction of application instances
are still running the previous version, and CI verifies (see below) that
migration N applies cleanly against migration N-1's already-applied state,
i.e. that the schema after migration N is still compatible with the
previous release's code, not just the next release's code (FR-094, FR-026).

`0001_config.sql`'s own header documents an example already in this
codebase: several fields from `data-model.md`'s `Agent` and `Skill` entities
(e.g. `agents.tool_profile_id`, `skills.bundle_digest`) are intentionally
omitted from that migration because they belong to a later, out-of-scope
task. A future migration can add them without touching `0001_config.sql` --
that is expand/contract in practice, not just in theory.

## `CREATE INDEX CONCURRENTLY`

`CREATE INDEX CONCURRENTLY` cannot run inside a transaction block. None of
the three migrations in this directory today need it -- they all run against
an empty database (there is no data to build an index over incrementally,
and a plain `CREATE INDEX` inside the migration's implicit transaction is
fine when the table is empty). But once a future migration adds an index to
a table that already holds production rows, a plain `CREATE INDEX` would
hold a lock that blocks writes for the duration of the build, which is not
acceptable on a populated table.

When that day comes: `CREATE INDEX CONCURRENTLY` must be isolated into its
own migration file, run outside the transaction wrapping every other
statement in this directory's convention. That migration cannot bundle
anything else that needs the transactional guarantees the rest of this
directory relies on.

## How migrations are applied

There is no standalone CLI/runner for this project (deliberately out of
scope for this migration set). The reference implementation of "apply every
migration" lives in
`backend-go/tests/integration/rls_isolation_test.go`'s `applyMigrations`
helper:

1. `filepath.Glob(filepath.Join(migrationsDir, "*.sql"))` collects every
   `.sql` file in this directory.
2. `sort.Strings(files)` sorts them lexically -- this is why the `NNNN`
   filename prefix is what controls apply order.
3. Each file's raw contents are read and executed as one `conn.Exec(...)`
   call, in order, against a **direct** (non-pooled) Postgres connection --
   never through PgBouncer. PgBouncer's transaction-mode pooling breaks
   certain DDL and session assumptions (e.g. a multi-statement file relying
   on session state persisting across statements within the same physical
   connection), so migrations always connect straight to Postgres,
   bypassing the pooling tier entirely. Only application runtime traffic
   goes through PgBouncer.

Any future migration tooling should preserve both properties: lexical glob
ordering, and a direct (unpooled) connection for DDL.

## CI verification (future work)

This README documents the discipline; it does not implement the CI job.
A later task is expected to wire a check into `.github/workflows/ci.yml`
that verifies each migration `N` applies cleanly against the database state
left by migration `N-1` as it exists in the *previous* released application
version -- not just against whatever the current branch's full migration
set produces end-to-end. That is the concrete, automatable form of the
expand/contract guarantee above: a rolling deploy is only safe if the
in-between schema state (old code, N applied) is never broken, and CI is
the place that should catch a violation before it ships, not the migration
files themselves.

## Setting tenant context safely

Migrations in this directory create RLS policies (`0003_rls.sql`) that key
off `current_setting('app.tenant_id', true)`. Application code sets that
context per-transaction with a parameterized call, never string-built SQL
(security-and-owasp.instructions.md A05/GO3):

```go
// SET LOCAL, not SET: the context must not outlive the transaction, or it
// can leak into whatever transaction a connection pooler hands the same
// physical connection to next.
_, err := tx.Exec(ctx, `SELECT set_config('app.tenant_id', $1, true)`, tenantID)
```

The `true` (`is_local`) argument to `set_config` is what makes this
transaction-local (`SET LOCAL app.tenant_id = ...`) rather than
session-local (`SET app.tenant_id = ...`) -- see
`backend-go/tests/integration/rls_isolation_test.go`'s
`setLocalTenant`/`setSessionTenant` pair and the "session-level SET is not
tenant-safe under pooled connections" subtest for why the session-local
variant is unsafe under PgBouncer's transaction-pooling mode and must never
be used for tenant scoping.
