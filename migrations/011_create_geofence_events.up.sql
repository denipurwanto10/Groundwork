-- Migration 011 (up): geofence events — one row per zone boundary a vehicle
-- crossed.
--
-- Detection lives next to the position update (internal/handler/geofence.go):
-- the previous point and the new one are compared in the same round trip that
-- stores the position, so the map and the history can never disagree.
-- Re-runnable: table and indexes are guarded.

CREATE TABLE IF NOT EXISTS geofence_events (
    id         BIGSERIAL PRIMARY KEY,
    vehicle_id BIGINT      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    zone_id    BIGINT      NOT NULL REFERENCES zones (id) ON DELETE CASCADE,
    event_type VARCHAR(8)  NOT NULL CHECK (event_type IN ('ENTER', 'EXIT')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- History is read newest-first, optionally narrowed to one unit or one zone
-- (GET /api/geofence-events).
CREATE INDEX IF NOT EXISTS idx_geofence_events_vehicle ON geofence_events (vehicle_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_geofence_events_zone    ON geofence_events (zone_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_geofence_events_created ON geofence_events (created_at DESC);
