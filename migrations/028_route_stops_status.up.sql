-- Migration 028 (up): the status of every stop of a route.
--
-- A stop of a route is not a point on a list anymore: it is a step of the
-- trip. The server watches the position of the unit and marks the stop as
-- "arrived" when the unit is close enough to it; the driver then confirms
-- that the stop is done ("completed", with a note as the proof) or that it is
-- passed by ("skipped").
--
-- The stops of a route live in their own table from now on. The JSONB column
-- vehicle_routes.stops stays for compatibility, but it only holds the places:
-- the status of a stop is read from here, never from the JSON.
--
-- The order of the visits is the one the server worked out (seq starts at 0),
-- and the last stop of the route is the destination of the route itself.
--
-- Re-runnable: every statement is guarded.

CREATE TABLE IF NOT EXISTS route_stops (
    id              BIGSERIAL PRIMARY KEY,
    route_id        BIGINT NOT NULL REFERENCES vehicle_routes(id) ON DELETE CASCADE,
    seq             INTEGER NOT NULL,
    name            TEXT NOT NULL DEFAULT '',
    lat             DOUBLE PRECISION NOT NULL,
    lng             DOUBLE PRECISION NOT NULL,
    status          TEXT NOT NULL DEFAULT 'pending',
    arrived_at      TIMESTAMPTZ,
    completed_at    TIMESTAMPTZ,
    completed_by    BIGINT REFERENCES users(id) ON DELETE SET NULL,
    completion_note TEXT NOT NULL DEFAULT '',
    completed_lat   DOUBLE PRECISION,
    completed_lng   DOUBLE PRECISION,
    CONSTRAINT route_stops_status_check
        CHECK (status IN ('pending', 'arrived', 'completed', 'skipped')),
    CONSTRAINT route_stops_route_seq_key UNIQUE (route_id, seq)
);

CREATE INDEX IF NOT EXISTS route_stops_route_seq_idx
    ON route_stops (route_id, seq);

CREATE INDEX IF NOT EXISTS route_stops_route_status_idx
    ON route_stops (route_id, status);

-- Backfill: the stops that were kept as a JSON array become one row per
-- element, in the visiting order the server worked out.
--
-- The status of a backfilled stop follows the status of its route: a route
-- that is already done has its stops done as well, a cancelled route has its
-- stops skipped, and a route that is still running starts from the beginning.
INSERT INTO route_stops
       (route_id, seq, name, lat, lng, status, arrived_at, completed_at, completion_note)
SELECT rt.id,
       (s.ordinality - 1)::integer,
       COALESCE(s.stop->>'name', ''),
       CASE WHEN s.stop->>'lat' ~ '^-?[0-9]+(\.[0-9]+)?$'
            THEN (s.stop->>'lat')::double precision ELSE rt.dest_lat END,
       CASE WHEN s.stop->>'lng' ~ '^-?[0-9]+(\.[0-9]+)?$'
            THEN (s.stop->>'lng')::double precision ELSE rt.dest_lng END,
       CASE rt.status
           WHEN 'completed' THEN 'completed'
           WHEN 'cancelled' THEN 'skipped'
           ELSE 'pending'
       END,
       CASE WHEN rt.status = 'completed' THEN rt.ended_at END,
       CASE WHEN rt.status = 'completed' THEN rt.ended_at END,
       CASE WHEN rt.status = 'completed' THEN 'Otomatis: rute selesai' ELSE '' END
  FROM vehicle_routes rt
  CROSS JOIN LATERAL jsonb_array_elements(rt.stops) WITH ORDINALITY AS s(stop, ordinality)
 WHERE jsonb_typeof(rt.stops) = 'array'
   AND jsonb_array_length(rt.stops) > 0
   AND NOT EXISTS (SELECT 1 FROM route_stops rs WHERE rs.route_id = rt.id)
ON CONFLICT (route_id, seq) DO NOTHING;

-- Backfill: a route with a single destination kept it in the columns of the
-- route itself. It becomes one stop, so every route has the same shape from
-- now on.
INSERT INTO route_stops
       (route_id, seq, name, lat, lng, status, arrived_at, completed_at, completion_note)
SELECT rt.id,
       0,
       COALESCE(rt.dest_name, ''),
       rt.dest_lat,
       rt.dest_lng,
       CASE rt.status
           WHEN 'completed' THEN 'completed'
           WHEN 'cancelled' THEN 'skipped'
           ELSE 'pending'
       END,
       CASE WHEN rt.status = 'completed' THEN rt.ended_at END,
       CASE WHEN rt.status = 'completed' THEN rt.ended_at END,
       CASE WHEN rt.status = 'completed' THEN 'Otomatis: rute selesai' ELSE '' END
  FROM vehicle_routes rt
 WHERE jsonb_typeof(rt.stops) = 'array'
   AND jsonb_array_length(rt.stops) = 0
   AND NOT EXISTS (SELECT 1 FROM route_stops rs WHERE rs.route_id = rt.id)
ON CONFLICT (route_id, seq) DO NOTHING;
