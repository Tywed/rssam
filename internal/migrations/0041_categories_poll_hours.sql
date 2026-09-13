-- Per-category polling window ("08:00-22:00", server time zone, may wrap past
-- midnight). Feeds of the category are not fetched outside the window; their
-- next_check_at is moved to the next opening instead. Empty = always.

ALTER TABLE categories ADD COLUMN IF NOT EXISTS poll_hours TEXT NOT NULL DEFAULT '';
