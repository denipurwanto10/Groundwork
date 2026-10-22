-- Migration 019 (down): drop the stops of a multi-stop route.

ALTER TABLE vehicle_routes DROP CONSTRAINT IF EXISTS vehicle_routes_stops_check;
ALTER TABLE vehicle_routes DROP COLUMN IF EXISTS stops;
