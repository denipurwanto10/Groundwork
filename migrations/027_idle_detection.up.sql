-- Migration 027 (up): idle detection — the anchor of a standing unit and the
-- history of its idle sessions.
--
-- The server decides one status of its own now (besides offline): a unit that
-- keeps reporting the same spot is idle. To know that, it has to remember
-- where the unit started standing still — that is the anchor.
--
-- `stationary_since` is the moment the anchor was placed,
-- `stationary_anchor_lat`/`_lng` are the point itself. All three are null
-- for a unit that has never been seen standing still.
--
-- `idle_events` is the history of the idle sessions: one row per spell of
-- standing still that lasted long enough. A row without `ended_at` is a
-- session that is still running.
--
-- One unit has at most ONE open session (ended_at IS NULL) at a time. The
-- rule lives in the application logic (internal/handler/vehicles.go: the
-- anchor tracking closes the open session before the sweep can open a new
-- one, and the sweep never opens a second one for a unit that already has
-- one). The partial unique index at the end of this file makes the same
-- promise in the database, so a mistake can never leave two open sessions
-- behind for one unit.

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS stationary_since TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS stationary_anchor_lat DOUBLE PRECISION,
    ADD COLUMN IF NOT EXISTS stationary_anchor_lng DOUBLE PRECISION;

-- The three columns belong together: a unit either has an anchor (and a
-- clock that says since when it stands there) or has neither.
ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_stationary_check;
ALTER TABLE vehicles ADD CONSTRAINT vehicles_stationary_check CHECK (
    (stationary_since IS NULL AND stationary_anchor_lat IS NULL AND stationary_anchor_lng IS NULL)
    OR (stationary_since IS NOT NULL AND stationary_anchor_lat IS NOT NULL AND stationary_anchor_lng IS NOT NULL)
);

CREATE TABLE IF NOT EXISTS idle_events (
    id         BIGSERIAL PRIMARY KEY,
    vehicle_id BIGINT      NOT NULL REFERENCES vehicles (id) ON DELETE CASCADE,
    -- The moment the unit started standing still: the same instant as
    -- vehicles.stationary_since when the session began.
    started_at TIMESTAMPTZ NOT NULL,
    -- Null while the session is still running.
    ended_at   TIMESTAMPTZ,
    anchor_lat DOUBLE PRECISION NOT NULL,
    anchor_lng DOUBLE PRECISION NOT NULL,
    -- The zone the anchor lies in; null when the unit stands outside every
    -- zone. ON DELETE SET NULL: a zone that is deleted leaves the history of
    -- the idle sessions behind, it does not delete it.
    zone_id    BIGINT      REFERENCES zones (id) ON DELETE SET NULL
);

-- The history is read newest-first, narrowed to one unit (GET
-- /api/idle-events).
CREATE INDEX IF NOT EXISTS idx_idle_events_vehicle ON idle_events (vehicle_id, started_at DESC);
CREATE INDEX IF NOT EXISTS idx_idle_events_zone    ON idle_events (zone_id, started_at DESC);

-- At most one open session per unit.
CREATE UNIQUE INDEX IF NOT EXISTS idx_idle_events_one_open
    ON idle_events (vehicle_id) WHERE ended_at IS NULL;
