-- Migration 014 (down): remove the heading and the route colour again.

ALTER TABLE vehicle_positions DROP COLUMN IF EXISTS heading;
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_heading_check;
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicle_positions_heading_check;
ALTER TABLE vehicles DROP COLUMN IF EXISTS route_color;
ALTER TABLE vehicles DROP COLUMN IF EXISTS last_heading;
