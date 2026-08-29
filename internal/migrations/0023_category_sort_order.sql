-- Per-user category display order (sidebar, settings).
ALTER TABLE categories ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;

-- Preserve previous list order (newest id first → lowest sort_order).
WITH ranked AS (
  SELECT id, row_number() OVER (PARTITION BY user_id ORDER BY id DESC) - 1 AS ord
  FROM categories
)
UPDATE categories c
SET sort_order = r.ord
FROM ranked r
WHERE c.id = r.id;

CREATE INDEX IF NOT EXISTS categories_user_id_sort_order_idx ON categories(user_id, sort_order);
