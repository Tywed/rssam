-- Filters / rules / matches.

CREATE TABLE IF NOT EXISTS filters (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL DEFAULT 0,
  name TEXT NOT NULL,
  enabled BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS filters_user_id_idx ON filters(user_id);
CREATE INDEX IF NOT EXISTS filters_enabled_idx ON filters(enabled);

CREATE TABLE IF NOT EXISTS filter_rules (
  id BIGSERIAL PRIMARY KEY,
  filter_id BIGINT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  field TEXT NOT NULL,
  pattern TEXT NOT NULL,
  negate BOOLEAN NOT NULL DEFAULT FALSE,
  op TEXT NOT NULL DEFAULT 'and',
  priority INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS filter_rules_filter_id_idx ON filter_rules(filter_id);
CREATE INDEX IF NOT EXISTS filter_rules_priority_idx ON filter_rules(filter_id, priority, id);

CREATE TABLE IF NOT EXISTS filter_matches (
  id BIGSERIAL PRIMARY KEY,
  filter_id BIGINT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  entry_id BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  matched_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  details JSONB NULL
);

CREATE UNIQUE INDEX IF NOT EXISTS filter_matches_filter_id_entry_id_uidx ON filter_matches(filter_id, entry_id);
CREATE INDEX IF NOT EXISTS filter_matches_filter_id_idx ON filter_matches(filter_id);
CREATE INDEX IF NOT EXISTS filter_matches_entry_id_idx ON filter_matches(entry_id);
CREATE INDEX IF NOT EXISTS filter_matches_matched_at_idx ON filter_matches(matched_at);

