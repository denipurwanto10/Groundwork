-- Migration 022 (down): the 'staff' accounts become viewers again.
UPDATE users SET role = 'viewer' WHERE role = 'staff';

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'viewer', 'driver'));
