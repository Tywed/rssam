-- Audit trail of administrative and account-security actions (who did what,
-- from where). Rows are written only for rare, human-triggered actions, so
-- the table stays small: the primary key is the only index and listing,
-- filtering and retention all scan it. actor_name is denormalised so the
-- history stays readable after the user is deleted.

CREATE TABLE IF NOT EXISTS audit_log (
  id          BIGSERIAL PRIMARY KEY,
  at          TIMESTAMPTZ NOT NULL DEFAULT now(),
  actor_id    BIGINT NULL REFERENCES users(id) ON DELETE SET NULL,
  actor_name  TEXT NOT NULL DEFAULT '',
  ip          TEXT NOT NULL DEFAULT '',
  action      TEXT NOT NULL,
  target_type TEXT NOT NULL DEFAULT '',
  target_id   BIGINT NULL,
  details     JSONB NOT NULL DEFAULT '{}'::jsonb
);
