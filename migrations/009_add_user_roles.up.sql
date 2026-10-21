-- Migration 009 (up): replace the role set with admin / viewer / driver.
--
-- The first pass of the users table shipped admin/operator/viewer before any
-- RBAC existed, so 'operator' never granted more than 'viewer'. It folds into
-- 'viewer' here; promote the accounts that need write access to 'admin'.
-- Re-runnable: the constraint is dropped before it is recreated.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;

UPDATE users SET role = 'viewer' WHERE role NOT IN ('admin', 'viewer', 'driver');

ALTER TABLE users ALTER COLUMN role SET DEFAULT 'viewer';

ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'viewer', 'driver'));
