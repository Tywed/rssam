-- Categories get an author (0.3.8): like feeds, only the author or an
-- admin renames one, and an editor deletes one only when nobody else
-- follows it. Existing rows take the creator from the audit log when it
-- recorded the creation; the rest stay without an author (admin only).
ALTER TABLE categories ADD COLUMN IF NOT EXISTS owner_id BIGINT REFERENCES users(id) ON DELETE SET NULL;
UPDATE categories c
SET owner_id = (
  SELECT a.actor_id FROM audit_log a
  WHERE a.action = 'category.create' AND a.target_type = 'category' AND a.target_id = c.id
    AND a.actor_id IS NOT NULL
  ORDER BY a.id LIMIT 1)
WHERE c.owner_id IS NULL;
