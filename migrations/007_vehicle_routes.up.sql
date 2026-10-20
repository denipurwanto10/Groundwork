-- Migration 007 (up): delivery routes for the tracked fleet.
--
-- A route is a planned path from an origin to a destination for one vehicle:
-- what a courier app shows as "jalur pengiriman". The geometry is a real
-- LineString (the driving path when a routing engine answered, a straight line
-- otherwise), kept apart from the positions the vehicle reports, so the plan
-- and the track can be compared.
--
-- Re-runnable: every statement is guarded.

CREATE TABLE IF NOT EXISTS vehicle_routes (
    id            BIGSERIAL PRIMARY KEY,
    vehicle_id    BIGINT NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    origin_name   TEXT NOT NULL DEFAULT '',
    dest_name     TEXT NOT NULL DEFAULT '',
    origin_lat    DOUBLE PRECISION NOT NULL,
    origin_lng    DOUBLE PRECISION NOT NULL,
    dest_lat      DOUBLE PRECISION NOT NULL,
    dest_lng      DOUBLE PRECISION NOT NULL,
    -- geometry, not geography: ST_LineLocatePoint/ST_LineSubstring (progress,
    -- remaining distance) need the geometry form.
    geom          geometry(LineString, 4326) NOT NULL,
    distance_m    DOUBLE PRECISION NOT NULL DEFAULT 0,
    duration_s    DOUBLE PRECISION NOT NULL DEFAULT 0,
    -- 'osrm' = driving path from the routing engine, 'direct' = straight line
    -- fallback when the engine is unreachable. The UI says which one it shows.
    source        TEXT NOT NULL DEFAULT 'direct',
    status        TEXT NOT NULL DEFAULT 'active',
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    ended_at      TIMESTAMPTZ,
    CONSTRAINT vehicle_routes_origin_lat_check CHECK (origin_lat BETWEEN -90 AND 90),
    CONSTRAINT vehicle_routes_origin_lng_check CHECK (origin_lng BETWEEN -180 AND 180),
    CONSTRAINT vehicle_routes_dest_lat_check   CHECK (dest_lat BETWEEN -90 AND 90),
    CONSTRAINT vehicle_routes_dest_lng_check   CHECK (dest_lng BETWEEN -180 AND 180),
    CONSTRAINT vehicle_routes_source_check     CHECK (source IN ('osrm', 'direct')),
    CONSTRAINT vehicle_routes_status_check     CHECK (status IN ('active', 'completed', 'cancelled')),
    CONSTRAINT vehicle_routes_distance_check   CHECK (distance_m >= 0 AND duration_s >= 0),
    CONSTRAINT vehicle_routes_geom_valid       CHECK (
        NOT ST_IsEmpty(geom) AND ST_IsValid(geom) AND ST_NPoints(geom) >= 2
    )
);

-- One active route per vehicle: setting a new one cancels the previous one, so
-- a unit can never be drawn following two delivery paths at once.
CREATE UNIQUE INDEX IF NOT EXISTS uq_vehicle_routes_active
    ON vehicle_routes (vehicle_id) WHERE status = 'active';

CREATE INDEX IF NOT EXISTS idx_vehicle_routes_geom
    ON vehicle_routes USING GIST (geom);
CREATE INDEX IF NOT EXISTS idx_vehicle_routes_vehicle
    ON vehicle_routes (vehicle_id, created_at DESC);
