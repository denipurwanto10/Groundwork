-- Live tracking: the tracked fleet plus the positions it reported.
--
-- The same shape as the rest of the project: plain SQL, no migration framework,
-- and every statement is idempotent so the file can be applied more than once.
-- The CHECK constraints mirror the validation in internal/vehicle so a value
-- that the API rejects can never reach the database either.

CREATE TABLE IF NOT EXISTS vehicles (
    id             BIGSERIAL PRIMARY KEY,
    code           TEXT NOT NULL UNIQUE,
    name           TEXT NOT NULL,
    kind           TEXT NOT NULL DEFAULT 'truck',
    status         TEXT NOT NULL DEFAULT 'idle',
    last_lat       DOUBLE PRECISION,
    last_lng       DOUBLE PRECISION,
    last_speed_kmh DOUBLE PRECISION NOT NULL DEFAULT 0,
    last_seen_at   TIMESTAMPTZ,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    last_geom      geography(Point, 4326) GENERATED ALWAYS AS (
        CASE
            WHEN last_lat IS NULL OR last_lng IS NULL THEN NULL
            ELSE ST_SetSRID(ST_MakePoint(last_lng, last_lat), 4326)::geography
        END
    ) STORED,
    CONSTRAINT vehicles_kind_check   CHECK (kind IN ('truck', 'van', 'motorcycle', 'pickup')),
    CONSTRAINT vehicles_status_check CHECK (status IN ('idle', 'moving', 'offline')),
    CONSTRAINT vehicles_lat_check    CHECK (last_lat IS NULL OR last_lat BETWEEN -90 AND 90),
    CONSTRAINT vehicles_lng_check    CHECK (last_lng IS NULL OR last_lng BETWEEN -180 AND 180),
    CONSTRAINT vehicles_speed_check  CHECK (last_speed_kmh >= 0 AND last_speed_kmh <= 300)
);

CREATE INDEX IF NOT EXISTS idx_vehicles_last_geom ON vehicles USING GIST (last_geom);

CREATE TABLE IF NOT EXISTS vehicle_positions (
    id          BIGSERIAL PRIMARY KEY,
    vehicle_id  BIGINT NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    lat         DOUBLE PRECISION NOT NULL,
    lng         DOUBLE PRECISION NOT NULL,
    speed_kmh   DOUBLE PRECISION NOT NULL DEFAULT 0,
    recorded_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT vehicle_positions_lat_check   CHECK (lat BETWEEN -90 AND 90),
    CONSTRAINT vehicle_positions_lng_check   CHECK (lng BETWEEN -180 AND 180),
    CONSTRAINT vehicle_positions_speed_check CHECK (speed_kmh >= 0 AND speed_kmh <= 300)
);

CREATE INDEX IF NOT EXISTS idx_vehicle_positions_vehicle_time
    ON vehicle_positions (vehicle_id, recorded_at DESC);
