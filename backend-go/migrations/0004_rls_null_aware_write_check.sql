-- -----------------------------------------------------------------------------
-- Fix: the NULL-aware RLS policies on `tools` and `effect_classes`
-- (0003_rls.sql) had a write-path isolation gap.
--
-- Postgres reuses a policy's USING clause as its implicit WITH CHECK when
-- none is given. The read-side disjunct `tenant_id IS NULL OR tenant_id =
-- current_setting(...)::uuid` correctly makes globally-owned rows
-- (tenant_id IS NULL) READABLE by every tenant — but without an explicit
-- WITH CHECK, that same disjunct also made `tenant_id = NULL` a WRITABLE
-- value for any ordinary tenant-scoped session: ANY tenant could INSERT a
-- row with tenant_id = NULL and have every OTHER tenant's session treat it
-- as trusted global built-in data. Verified live against Postgres 16 as a
-- non-superuser, non-BYPASSRLS role.
--
-- The WITH CHECK below omits the NULL disjunct, so only a role that
-- bypasses RLS entirely (the migration/seed role, never an ordinary
-- application connection) can write a NULL-tenant row. Read-side
-- visibility (the USING clause) is unchanged.
-- -----------------------------------------------------------------------------

ALTER POLICY tenant_isolation ON tools
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

ALTER POLICY tenant_isolation ON effect_classes
  WITH CHECK (tenant_id = current_setting('app.tenant_id', true)::uuid);

-- -----------------------------------------------------------------------------
-- Note on a Postgres custom-GUC behavior every future caller of these
-- policies (on ANY of the 14 RLS-protected tables, not just the two above)
-- needs to know, discovered and verified live during this migration's
-- review:
--
-- Always call `set_config('app.tenant_id', $1, true)` at the START of
-- EVERY transaction that touches a tenant-scoped table -- never
-- conditionally, never "only if this might be a new tenant." Skipping it
-- is not silently safe. On a connection that has NEVER had the GUC set,
-- current_setting('app.tenant_id', true) returns NULL and every policy
-- resolves to deny-all, as intended. But once ANY transaction on that same
-- physical connection has set-and-committed the GUC, Postgres
-- "materializes" it at the custom-GUC boot default -- the empty string
-- '', not NULL -- for the rest of that connection's life. A later
-- transaction that skips set_config then gets current_setting(...) = '',
-- and ''::uuid raises "invalid input syntax for type uuid", so the query
-- fails with a Postgres ERROR rather than a clean zero-row result.
--
-- This is still fail-closed: the ERROR aborts the transaction before any
-- row is ever returned, so no cross-tenant (or any) data leaks either way.
-- But it means "forgot to set the tenant context" surfaces as a loud
-- exception, not a quiet empty result, and can appear on ANY tenant-scoped
-- table at any time after a connection's first tenant-scoped transaction.
-- If you see `invalid input syntax for type uuid: ""` from a tenant-scoped
-- query, this is almost certainly the cause: find the code path that
-- reused a connection without calling set_config first.
-- -----------------------------------------------------------------------------
