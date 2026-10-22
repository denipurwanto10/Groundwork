-- Migration 029 (down): remove the service reminders again.
--
-- The history of the services goes with it: without the odometer there is
-- nothing to schedule a service on. The odometer of the units is lost too —
-- it is a running total that cannot be rebuilt from the fleet row alone.

DROP INDEX IF EXISTS idx_vehicle_services_vehicle;
DROP TABLE IF EXISTS vehicle_services;

ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_service_interval_check;
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_service_notified_level_check;
ALTER TABLE vehicles
    DROP COLUMN IF EXISTS total_distance_m,
    DROP COLUMN IF EXISTS service_interval_km,
    DROP COLUMN IF EXISTS last_service_at,
    DROP COLUMN IF EXISTS last_service_odometer_m,
    DROP COLUMN IF EXISTS service_notified_level;
