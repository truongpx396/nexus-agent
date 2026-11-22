-- Record which key-encryption key wrapped each data key, so the KEK can be rotated:
-- a data key is unwrapped with the KEK its row names, and new keys are wrapped under
-- the current one. The id is a short digest of the KEK (crypto.KEK.ID), never the
-- key. Rows written before this column existed are NULL, and are opened by trying
-- each loaded KEK in turn.
ALTER TABLE encryption_keys ADD COLUMN kek_id text;
