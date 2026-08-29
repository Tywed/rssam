-- Filters MVP: scope, actions, labels, feed-level webhook.

ALTER TABLE filters
  ADD COLUMN IF NOT EXISTS match_any_rule BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS inverse BOOLEAN NOT NULL DEFAULT FALSE,
  ADD COLUMN IF NOT EXISTS order_id INT NOT NULL DEFAULT 0,
  ADD COLUMN IF NOT EXISTS feed_scope TEXT NOT NULL DEFAULT 'all';

DO $$
BEGIN
  ALTER TABLE filters
    ADD CONSTRAINT filters_feed_scope_check CHECK (feed_scope IN ('all', 'include', 'exclude'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

CREATE TABLE IF NOT EXISTS filter_scope_items (
  id BIGSERIAL PRIMARY KEY,
  filter_id BIGINT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  feed_id BIGINT NULL REFERENCES feeds(id) ON DELETE CASCADE,
  category_id BIGINT NULL REFERENCES categories(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  CONSTRAINT filter_scope_items_target_check CHECK (
    (feed_id IS NOT NULL AND category_id IS NULL) OR
    (feed_id IS NULL AND category_id IS NOT NULL)
  )
);

CREATE INDEX IF NOT EXISTS filter_scope_items_filter_id_idx ON filter_scope_items(filter_id);

CREATE TABLE IF NOT EXISTS filter_actions (
  id BIGSERIAL PRIMARY KEY,
  filter_id BIGINT NOT NULL REFERENCES filters(id) ON DELETE CASCADE,
  action_type TEXT NOT NULL,
  action_param TEXT NOT NULL DEFAULT '',
  priority INT NOT NULL DEFAULT 0,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

DO $$
BEGIN
  ALTER TABLE filter_actions
    ADD CONSTRAINT filter_actions_type_check CHECK (action_type IN ('label', 'webhook', 'delete'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;

CREATE INDEX IF NOT EXISTS filter_actions_filter_id_idx ON filter_actions(filter_id);

CREATE TABLE IF NOT EXISTS labels (
  id BIGSERIAL PRIMARY KEY,
  user_id BIGINT NOT NULL,
  caption TEXT NOT NULL,
  fg_color TEXT NOT NULL DEFAULT '#ffffff',
  bg_color TEXT NOT NULL DEFAULT '#2980b9',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS labels_user_caption_uidx ON labels(user_id, caption);
CREATE INDEX IF NOT EXISTS labels_user_id_idx ON labels(user_id);

CREATE TABLE IF NOT EXISTS entry_labels (
  entry_id BIGINT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
  label_id BIGINT NOT NULL REFERENCES labels(id) ON DELETE CASCADE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  PRIMARY KEY (entry_id, label_id)
);

CREATE INDEX IF NOT EXISTS entry_labels_label_id_idx ON entry_labels(label_id);

ALTER TABLE feeds
  ADD COLUMN IF NOT EXISTS webhook_id BIGINT NULL REFERENCES webhooks(id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS feeds_webhook_id_idx ON feeds(webhook_id);
