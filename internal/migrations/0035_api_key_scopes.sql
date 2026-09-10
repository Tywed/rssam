-- API keys carry a scope (read < write < admin) and an optional expiry.
-- Existing keys keep full access so nothing breaks on upgrade.
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS scope TEXT NOT NULL DEFAULT 'admin';
ALTER TABLE api_keys ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ NULL;
ALTER TABLE api_keys DROP CONSTRAINT IF EXISTS api_keys_scope_check;
ALTER TABLE api_keys ADD CONSTRAINT api_keys_scope_check CHECK (scope IN ('read', 'write', 'admin'));

-- Both indexes duplicate the UNIQUE constraints created with the tables
-- (api_keys_token_hash_key, sessions_session_id_key): one less index to
-- maintain on every login and key creation.
DROP INDEX IF EXISTS api_keys_token_hash_idx;
DROP INDEX IF EXISTS sessions_session_id_idx;
