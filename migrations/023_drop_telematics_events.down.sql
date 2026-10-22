-- Migration 023 (down): bring the telematics history back.
--
-- The violations that were recorded are gone with the table: there is no
-- backup of them. The shape is the one of migration 020, so the old code
-- would find it again.

CREATE TABLE IF NOT EXISTS telematics_events (
    id         BIGSERIAL PRIMARY KEY,
    vehicle_id BIGINT      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    event_type VARCHAR(16) NOT NULL CHECK (event_type IN ('OVERSPEED')),
    speed_kmh  DOUBLE PRECISION NOT NULL CHECK (speed_kmh > 0),
    limit_kmh  DOUBLE PRECISION NOT NULL CHECK (limit_kmh > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_telematics_events_vehicle ON telematics_events (vehicle_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_telematics_events_created ON telematics_events (created_at DESC);
