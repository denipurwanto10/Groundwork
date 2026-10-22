-- Migration 020 (up): telematics events — one row per rule a vehicle broke.
--
-- Detection lives next to the position update (internal/handler/telematics.go):
-- the reported speed is compared with the configured limit in the same
-- request that stores the position, so the map and the history can never
-- disagree.
--
-- `limit_kmh` is stored with the event, so the history still shows the limit
-- that was broken after the limit itself has been changed.
--
-- Re-runnable: table and indexes are guarded.

CREATE TABLE IF NOT EXISTS telematics_events (
    id         BIGSERIAL PRIMARY KEY,
    vehicle_id BIGINT      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    event_type VARCHAR(16) NOT NULL CHECK (event_type IN ('OVERSPEED')),
    speed_kmh  DOUBLE PRECISION NOT NULL CHECK (speed_kmh > 0),
    limit_kmh  DOUBLE PRECISION NOT NULL CHECK (limit_kmh > 0),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The history is read newest-first, optionally narrowed to one unit
-- (GET /api/telematics-events).
CREATE INDEX IF NOT EXISTS idx_telematics_events_vehicle ON telematics_events (vehicle_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_telematics_events_created ON telematics_events (created_at DESC);
