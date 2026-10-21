DROP INDEX IF EXISTS idx_users_username_lower;
ALTER TABLE users DROP CONSTRAINT IF EXISTS users_gender_check;

-- Accounts created with a username only have no address, and the old schema
-- requires one: fall back to the username so the column can be NOT NULL again.
UPDATE users SET email = username WHERE email IS NULL;
ALTER TABLE users ALTER COLUMN email SET NOT NULL;

ALTER TABLE users DROP COLUMN IF EXISTS username;
ALTER TABLE users DROP COLUMN IF EXISTS name;
ALTER TABLE users DROP COLUMN IF EXISTS gender;
