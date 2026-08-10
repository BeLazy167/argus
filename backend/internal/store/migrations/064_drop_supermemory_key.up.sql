-- The per-installation Supermemory BYOK key. Memory now lives in Postgres and
-- nothing reads this column: the client, the registry key cache and the three
-- API endpoints that wrote it are gone.
--
-- Only one installation ever held a value, and its memory was exported to
-- memory_export_archive and imported into memories before this ran. The column
-- holds ciphertext that is useless without ENCRYPTION_KEY and worthless with
-- it, since the account it authenticates is being closed.
ALTER TABLE installations DROP COLUMN IF EXISTS supermemory_key_enc;
