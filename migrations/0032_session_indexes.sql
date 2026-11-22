-- Indexes for the three session queries that had none that fit.
--
-- The session list filters on user_id and parent_session_id IS NULL and orders by
-- created_at descending; the orphan scan reads every running session across all
-- tenants every five minutes; the session-key lookup orders by created_at and
-- takes the newest. Each ran as a scan of the table, which grows with every
-- session ever created.
--
-- The new session-key index replaces the old one: (tenant_id, session_key) is its
-- leading prefix, so every lookup the old one served the new one serves too, and
-- the newest-first read needs no sort.
CREATE INDEX sessions_user_recent_idx ON sessions (tenant_id, user_id, created_at DESC) WHERE parent_session_id IS NULL;

CREATE INDEX sessions_running_idx ON sessions (updated_at) WHERE status = 'running';

CREATE INDEX sessions_tenant_session_key_recent_idx ON sessions (tenant_id, session_key, created_at DESC);
DROP INDEX sessions_tenant_session_key_idx;
