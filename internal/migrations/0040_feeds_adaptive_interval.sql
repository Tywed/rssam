-- Opt-in adaptive polling: a feed that publishes 3 items a day does not need
-- 24 polls a day. When set, the interval stretches from interval_minutes up
-- to ADAPTIVE_MAX_INTERVAL according to the items seen over the last week
-- (see storage.AdaptivePollInterval). Fewer polls = fewer feeds row versions.

ALTER TABLE feeds ADD COLUMN IF NOT EXISTS adaptive_interval BOOLEAN NOT NULL DEFAULT FALSE;
