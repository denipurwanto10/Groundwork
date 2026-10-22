-- Migration 019 (up): the stops of a multi-stop route.
--
-- A route can visit more than one destination. The stops are kept as a JSON
-- array in the visiting order the server worked out, so the map can number
-- them (1, 2, 3) and the unit can be shown on its way to the next one.
--
-- The array holds objects with lat, lng and name, the same shape the API
-- sends and receives. A route with a single destination has an empty array:
-- its origin and destination columns already say everything.
--
-- Re-runnable: every statement is guarded.

ALTER TABLE vehicle_routes
    ADD COLUMN IF NOT EXISTS stops JSONB NOT NULL DEFAULT '[]'::jsonb;

DO $$
BEGIN
  IF NOT EXISTS (
    SELECT 1 FROM pg_constraint WHERE conname = 'vehicle_routes_stops_check'
  ) THEN
    ALTER TABLE vehicle_routes
      ADD CONSTRAINT vehicle_routes_stops_check
      CHECK (jsonb_typeof(stops) = 'array');
  END IF;
END $$;
