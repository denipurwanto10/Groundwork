-- Migration 010 (down): remove driver_id from vehicles.

DROP INDEX IF EXISTS idx_vehicles_driver;
ALTER TABLE vehicles DROP COLUMN IF EXISTS driver_id;