-- Migration 029 (up): the odometer of a unit and the schedule of its service.
--
-- `total_distance_m` is the odometer: how far the unit has driven since it
-- was created. It is accumulated by the server, one position report at a
-- time (internal/handler/vehicles.go adds the step to the row in the same
-- statement that stores the position), so reading the odometer never means
-- reading the whole history of the positions again.
--
-- The service schedule is derived from that odometer: the next service is
-- due when the odometer reaches the reading of the last service plus the
-- interval of the unit. A unit without an interval of its own falls back to
-- the default of the server (SERVICE_INTERVAL_DEFAULT_KM), which is why
-- `service_interval_km` may be null.
--
-- `last_service_at` and `last_service_odometer_m` are the two halves of the
-- baseline: when the unit was serviced, and how far it had driven then.
-- `last_service_odometer_m` is 0 and `last_service_at` is null for a unit
-- that was never serviced — its schedule is counted from the odometer 0.
--
-- `service_notified_level` remembers the last warning that went out for the
-- running service cycle ('' / 'due_soon' / 'overdue'). The sweep compares the
-- level of a unit with this one, so a unit is warned once per level and not
-- on every tick. Only a written service record resets it.

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS total_distance_m DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS service_interval_km DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS last_service_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS last_service_odometer_m DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS service_notified_level TEXT NOT NULL DEFAULT '';

-- The interval of a unit: either the server default (null) or a number of
-- kilometres that has to make sense. The rule mirrors the validation in
-- internal/vehicle.
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_service_interval_check;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_service_interval_check CHECK (
    service_interval_km IS NULL OR (service_interval_km > 0 AND service_interval_km <= 100000)
);

-- The warning that was sent last for the running service cycle.
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_service_notified_level_check;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_service_notified_level_check CHECK (
    service_notified_level IN ('', 'due_soon', 'overdue')
);

-- The history of the services: one row per service that was written down.
CREATE TABLE IF NOT EXISTS vehicle_services (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  BIGINT      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    -- When the service was done.
    serviced_at TIMESTAMPTZ NOT NULL,
    -- The odometer of the unit at that moment: a snapshot of
    -- vehicles.total_distance_m.
    odometer_m  DOUBLE PRECISION,
    -- What was done, in one line. It may be empty.
    note        TEXT,
    -- Who wrote it down. ON DELETE SET NULL: the account may be deleted, the
    -- service record stays.
    recorded_by BIGINT      REFERENCES users (id) ON DELETE SET NULL
);

-- The history is read newest-first, narrowed to one unit (GET
-- /api/vehicles/:id/services).
CREATE INDEX IF NOT EXISTS idx_vehicle_services_vehicle ON vehicle_services (vehicle_id, serviced_at DESC);

-- Backfill the odometer of every unit from the positions it reported.
--
-- The steps are the ones the Overview tab and the operational report count:
-- the distance between two consecutive positions, measured on the sphere
-- (ST_Distance on geography), with the same two guards. A step that would
-- need an impossible speed (more than 200 km/h) is not a step — it is a jump
-- of the position, and a unit that is placed at the origin of a new route
-- has not driven that distance. A step without a duration is no step at all.
--
-- The odometer is the distance of the whole life of the unit, so every step
-- of its history counts here — not only the steps of a period.
--
-- A unit without a single position keeps the 0 it was created with.
UPDATE vehicles v
   SET total_distance_m = COALESCE(d.meters, 0)
  FROM (
        SELECT vehicle_id, sum(meters) AS meters
          FROM (
                SELECT vehicle_id,
                       ST_Distance(
                           ST_SetSRID(ST_MakePoint(lng, lat), 4326)::geography,
                           ST_SetSRID(ST_MakePoint(prev_lng, prev_lat), 4326)::geography) AS meters,
                       EXTRACT(EPOCH FROM (recorded_at - prev_at)) AS seconds
                  FROM (
                        SELECT vehicle_id, lat, lng, recorded_at,
                               lag(lat)         OVER w AS prev_lat,
                               lag(lng)         OVER w AS prev_lng,
                               lag(recorded_at) OVER w AS prev_at
                          FROM vehicle_positions
                         WINDOW w AS (PARTITION BY vehicle_id ORDER BY recorded_at, id)
                       ) p
                 WHERE prev_lat IS NOT NULL
               ) s
         WHERE seconds > 0 AND meters / seconds * 3.6 <= 200
         GROUP BY vehicle_id
       ) d
 WHERE d.vehicle_id = v.id;
