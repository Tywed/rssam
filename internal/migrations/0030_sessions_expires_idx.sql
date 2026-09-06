-- Expired sessions are now purged by the retention cleanup; this index makes
-- "WHERE expires_at < now()" a range scan instead of a seq scan.
CREATE INDEX IF NOT EXISTS sessions_expires_at_idx ON sessions(expires_at);
