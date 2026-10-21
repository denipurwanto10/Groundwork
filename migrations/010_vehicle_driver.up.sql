-- Migration 010 (up): add driver_id to vehicles so each unit can be
-- assigned to a driver. A driver then sees only their own vehicles.
-- Re-runnable: column and index are guarded.

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS driver_id BIGINT REFERENCES users (id) ON DELETE SET NULL;

CREATE INDEX IF NOT EXISTS idx_vehicles_driver ON vehicles (driver_id);