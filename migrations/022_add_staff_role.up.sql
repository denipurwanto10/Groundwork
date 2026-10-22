-- Migration 022 (up): the 'staff' user type.
--
-- The workspace had admin (writes everything), viewer (reads everything) and
-- driver (the unit on the road). A work order needs a fourth kind of account:
-- the person who does the job in the field but does not touch the fleet, the
-- zones or the accounts.
--
-- 'staff' is that account: it may open the task board and move a work order
-- along (pending -> in progress -> completed), and nothing else.
--
-- The constraint is dropped before it is recreated, so the file can be applied
-- more than once.

ALTER TABLE users DROP CONSTRAINT IF EXISTS users_role_check;

ALTER TABLE users ADD CONSTRAINT users_role_check
    CHECK (role IN ('admin', 'staff', 'viewer', 'driver'));
