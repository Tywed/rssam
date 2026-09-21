-- webhooks.filter_id has been NULL for every row since 0046 moved the
-- binding into filter_actions; nothing reads or writes it any more.
ALTER TABLE webhooks DROP COLUMN IF EXISTS filter_id;
