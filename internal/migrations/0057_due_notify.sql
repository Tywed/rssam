-- The worker loops used to poll their tables every second (scheduler every
-- 5 s): ~190 000 statements a day on an idle instance. They now sleep until
-- the next known due time and are woken by NOTIFY when a due time moves
-- earlier than what they know: a feed created or resumed, a job inserted, a
-- delivery queued or retried by hand. Routine poll bookkeeping only pushes
-- next_check_at / next_retry_at later and stays silent.
CREATE OR REPLACE FUNCTION rssam_notify_due() RETURNS trigger
LANGUAGE plpgsql AS $$
BEGIN
  PERFORM pg_notify(TG_ARGV[0], '');
  RETURN NULL;
END $$;

DROP TRIGGER IF EXISTS feeds_due_notify ON feeds;
DROP TRIGGER IF EXISTS feeds_due_notify_ins ON feeds;
CREATE TRIGGER feeds_due_notify_ins
  AFTER INSERT ON feeds
  FOR EACH STATEMENT
  EXECUTE FUNCTION rssam_notify_due('rssam_feeds_due');
DROP TRIGGER IF EXISTS feeds_due_notify_upd ON feeds;
CREATE TRIGGER feeds_due_notify_upd
  AFTER UPDATE OF next_check_at, poll_paused, manual_paused ON feeds
  FOR EACH ROW
  WHEN (NEW.poll_paused = FALSE AND NEW.manual_paused = FALSE
        AND (OLD.poll_paused OR OLD.manual_paused
             OR NEW.next_check_at IS NULL
             OR NEW.next_check_at < OLD.next_check_at))
  EXECUTE FUNCTION rssam_notify_due('rssam_feeds_due');

DROP TRIGGER IF EXISTS subscriptions_due_notify ON subscriptions;
CREATE TRIGGER subscriptions_due_notify
  AFTER INSERT ON subscriptions
  FOR EACH STATEMENT
  EXECUTE FUNCTION rssam_notify_due('rssam_feeds_due');

DROP TRIGGER IF EXISTS jobs_due_notify ON jobs;
DROP TRIGGER IF EXISTS jobs_due_notify_ins ON jobs;
CREATE TRIGGER jobs_due_notify_ins
  AFTER INSERT ON jobs
  FOR EACH STATEMENT
  EXECUTE FUNCTION rssam_notify_due('rssam_jobs_due');
-- A released lock (retry, reclaim) is a job the dispatcher may be unaware of.
DROP TRIGGER IF EXISTS jobs_due_notify_upd ON jobs;
CREATE TRIGGER jobs_due_notify_upd
  AFTER UPDATE OF locked_at, run_at ON jobs
  FOR EACH ROW
  WHEN (NEW.locked_at IS NULL AND (OLD.locked_at IS NOT NULL OR NEW.run_at < OLD.run_at))
  EXECUTE FUNCTION rssam_notify_due('rssam_jobs_due');

DROP TRIGGER IF EXISTS webhook_logs_due_notify ON webhook_logs;
DROP TRIGGER IF EXISTS webhook_logs_due_notify_ins ON webhook_logs;
CREATE TRIGGER webhook_logs_due_notify_ins
  AFTER INSERT ON webhook_logs
  FOR EACH STATEMENT
  EXECUTE FUNCTION rssam_notify_due('rssam_webhooks_due');
DROP TRIGGER IF EXISTS webhook_logs_due_notify_upd ON webhook_logs;
CREATE TRIGGER webhook_logs_due_notify_upd
  AFTER UPDATE OF next_retry_at, status ON webhook_logs
  FOR EACH ROW
  WHEN (NEW.status IN ('pending', 'failed')
        AND (OLD.status NOT IN ('pending', 'failed')
             OR NEW.next_retry_at < OLD.next_retry_at))
  EXECUTE FUNCTION rssam_notify_due('rssam_webhooks_due');
