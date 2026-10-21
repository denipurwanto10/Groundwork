-- Migration 009 (down): restore the original admin/operator/viewer role set.
--
-- 'driver' has no equivalent in the old set, so those accounts become 'viewer'
-- (read-only) rather than silently gaining 'operator'.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;

UPDATE users SET role = 'viewer' WHERE role = 'driver';

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'operator';

ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'operator', 'viewer'));
