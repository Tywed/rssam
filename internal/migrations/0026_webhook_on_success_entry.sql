-- Per-webhook action applied to the entry after successful delivery.

ALTER TABLE webhooks
  ADD COLUMN IF NOT EXISTS on_success_entry TEXT NOT NULL DEFAULT 'none';

DO $$
BEGIN
  ALTER TABLE webhooks
    ADD CONSTRAINT webhooks_on_success_entry_check
    CHECK (on_success_entry IN ('none', 'hash', 'delete', 'mark_read'));
EXCEPTION
  WHEN duplicate_object THEN
    NULL;
END $$;
