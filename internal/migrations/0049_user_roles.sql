-- users.is_admin becomes a three-level role: admin (everything), editor
-- (manages the shared feed catalog), reader (subscribes and reads).
ALTER TABLE users ADD COLUMN IF NOT EXISTS role TEXT NOT NULL DEFAULT 'reader';

DO $$
BEGIN
  ALTER TABLE users ADD CONSTRAINT users_role_check CHECK (role IN ('admin', 'editor', 'reader'));
EXCEPTION
  WHEN duplicate_object THEN NULL;
END $$;

DO $$
BEGIN
  IF EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'users' AND column_name = 'is_admin') THEN
    UPDATE users SET role = 'admin' WHERE is_admin;
    ALTER TABLE users DROP COLUMN is_admin;
  END IF;
END $$;
