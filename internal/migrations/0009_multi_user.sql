-- Multi-user: users, api_keys, sessions, tenant scoping.

CREATE TABLE IF NOT EXISTS users (
  id BIGSERIAL PRIMARY KEY,
  username VARCHAR(50) NOT NULL UNIQUE,
  password_hash VARCHAR(255) NOT NULL DEFAULT '',
  fever_api_key VARCHAR(32) NOT NULL DEFAULT '',
  is_admin BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS users_fever_api_key_idx ON users(fever_api_key) WHERE fever_api_key <> '';

INSERT INTO users (id, username, password_hash, is_admin)
VALUES (1, 'default', '', TRUE)
ON CONFLICT (username) DO NOTHING;

SELECT setval(
  pg_get_serial_sequence('users', 'id'),
  GREATEST((SELECT COALESCE(MAX(id), 1) FROM users), 1)
);

CREATE TABLE IF NOT EXISTS api_keys (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  name VARCHAR(255) NOT NULL DEFAULT '',
  token_hash VARCHAR(64) NOT NULL UNIQUE,
  last_used_at TIMESTAMPTZ NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS api_keys_user_id_idx ON api_keys(user_id);
CREATE INDEX IF NOT EXISTS api_keys_token_hash_idx ON api_keys(token_hash);

CREATE TABLE IF NOT EXISTS sessions (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
  session_id VARCHAR(255) NOT NULL UNIQUE,
  expires_at TIMESTAMPTZ NOT NULL,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions(user_id);
CREATE INDEX IF NOT EXISTS sessions_session_id_idx ON sessions(session_id);

-- categories
ALTER TABLE categories ADD COLUMN IF NOT EXISTS user_id BIGINT;
UPDATE categories SET user_id = 1 WHERE user_id IS NULL;
ALTER TABLE categories ALTER COLUMN user_id SET NOT NULL;

DO $$
BEGIN
  ALTER TABLE categories
    ADD CONSTRAINT categories_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS categories_user_id_idx ON categories(user_id);

-- feeds
ALTER TABLE feeds ADD COLUMN IF NOT EXISTS user_id BIGINT;
UPDATE feeds SET user_id = 1 WHERE user_id IS NULL;
ALTER TABLE feeds ALTER COLUMN user_id SET NOT NULL;

DO $$
BEGIN
  ALTER TABLE feeds
    ADD CONSTRAINT feeds_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

ALTER TABLE feeds DROP CONSTRAINT IF EXISTS feeds_feed_url_key;
DROP INDEX IF EXISTS feeds_feed_url_key;

CREATE UNIQUE INDEX IF NOT EXISTS feeds_user_id_feed_url_uidx ON feeds(user_id, feed_url);
CREATE INDEX IF NOT EXISTS feeds_user_id_idx ON feeds(user_id);

-- entries (denormalized user_id for tenant queries)
ALTER TABLE entries ADD COLUMN IF NOT EXISTS user_id BIGINT;
UPDATE entries e
SET user_id = f.user_id
FROM feeds f
WHERE f.id = e.feed_id AND e.user_id IS NULL;
UPDATE entries SET user_id = 1 WHERE user_id IS NULL;
ALTER TABLE entries ALTER COLUMN user_id SET NOT NULL;

DO $$
BEGIN
  ALTER TABLE entries
    ADD CONSTRAINT entries_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

CREATE INDEX IF NOT EXISTS entries_user_id_idx ON entries(user_id);

-- filters / webhooks backfill from legacy default 0
UPDATE filters SET user_id = 1 WHERE user_id = 0;
UPDATE webhooks SET user_id = 1 WHERE user_id = 0;

DO $$
BEGIN
  ALTER TABLE filters
    ADD CONSTRAINT filters_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
  ALTER TABLE webhooks
    ADD CONSTRAINT webhooks_user_id_fkey FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE;
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;
