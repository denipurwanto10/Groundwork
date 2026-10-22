-- Migration 018 (down): drop the "started at" mark of a route.
ALTER TABLE vehicle_routes DROP COLUMN IF EXISTS started_at;
