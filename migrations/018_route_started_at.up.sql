-- Migration 018 (up): a planned route can be started.
--
-- A planned route is a plan, not a trip: the unit only drives it once the
-- driver says so. `started_at` is the moment the driver pressed "Start
-- trip". A route without it is waiting for the driver.
--
-- Re-runnable: the column is added only when it is missing.

ALTER TABLE vehicle_routes ADD COLUMN IF NOT EXISTS started_at TIMESTAMPTZ;
