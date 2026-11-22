-- Delivery retry: the outbox becomes at-least-once. A delivery that failed, or
-- that was opened and never attempted because the process died between the
-- event and the send, is picked up again by a periodic pass once next_attempt_at
-- is due. A newly opened row is due a minute out, which leaves the attempt that
-- opened it room to finish before the pass takes it; a failed attempt moves the
-- date out by a growing backoff, set where the failure is recorded.
ALTER TABLE deliveries ADD COLUMN next_attempt_at timestamptz NOT NULL DEFAULT now() + interval '1 minute';

DROP INDEX deliveries_pending_idx;
CREATE INDEX deliveries_due_idx ON deliveries (next_attempt_at) WHERE status IN ('pending', 'failed');
