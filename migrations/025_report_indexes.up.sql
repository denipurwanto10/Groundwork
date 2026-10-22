-- Migration 025 (up): indexes for the operational report.
--
-- The report reads three tables over a date range: the routes of the fleet,
-- the work orders and the crossings of the zone borders. Those tables were
-- only indexed for their own reads (the newest rows of one unit), so a range
-- over the whole workspace had to be scanned. These indexes make the report
-- read the range it asks for.
--
-- Re-runnable: an index that is already there is left alone.

CREATE INDEX IF NOT EXISTS idx_vehicle_routes_created
    ON vehicle_routes (created_at DESC);

CREATE INDEX IF NOT EXISTS idx_vehicle_routes_ended
    ON vehicle_routes (ended_at DESC);

CREATE INDEX IF NOT EXISTS idx_tasks_created
    ON tasks (created_at DESC);
