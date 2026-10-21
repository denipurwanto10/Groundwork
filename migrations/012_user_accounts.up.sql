-- Account fields for the admin "Kelola User" screen. A username is now the
-- credential people sign in with, so it is required and unique; the email
-- address becomes optional, and the profile carries a name and a gender.

ALTER TABLE users ADD COLUMN IF NOT EXISTS username VARCHAR(64);
ALTER TABLE users ADD COLUMN IF NOT EXISTS name VARCHAR(120);
ALTER TABLE users ADD COLUMN IF NOT EXISTS gender VARCHAR(10);

-- Existing accounts get the name part of their address as the username.
-- Duplicates are impossible here: the stem is unique per group and the
-- second row of a group gets the account id appended.
WITH stems AS (
  SELECT id, left(coalesce(nullif(split_part(email, '@', 1), ''), 'user'), 55) AS stem
  FROM users
), numbered AS (
  SELECT id, stem, row_number() OVER (PARTITION BY stem ORDER BY id) AS rn
  FROM stems
)
UPDATE users u
SET username = CASE WHEN n.rn = 1 THEN n.stem ELSE n.stem || '-' || u.id END
FROM numbered n
WHERE u.id = n.id;

ALTER TABLE users ALTER COLUMN email DROP NOT NULL;
ALTER TABLE users ALTER COLUMN username SET NOT NULL;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'users_gender_check'
  ) THEN
    ALTER TABLE users
      ADD CONSTRAINT users_gender_check
      CHECK (gender IS NULL OR gender IN ('male', 'female'));
  END IF;
END $$;

CREATE UNIQUE INDEX IF NOT EXISTS idx_users_username_lower ON users (lower(username));
