-- Migration 025 (down): drop the indexes of the report again.
--
-- The tables keep their rows: only the indexes that the report reads with go
-- back, and the report falls back on the scans it had before.

DROP INDEX IF EXISTS idx_vehicle_routes_created;
DROP INDEX IF EXISTS idx_vehicle_routes_ended;
DROP INDEX IF EXISTS idx_tasks_created;
