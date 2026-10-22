-- Migration 027 (down): remove the idle detection again.
--
-- The idle sessions go with it: without the anchor there is nothing left to
-- record them for. The status of the units stays as it is — the column
-- vehicles.status keeps its three values, only the server no longer decides
-- "idle" by itself.

DROP INDEX IF EXISTS idx_idle_events_one_open;
DROP TABLE IF EXISTS idle_events;

ALTER TABLE vehicles DROP CONSTRAINT IF EXISTS vehicles_stationary_check;
ALTER TABLE vehicles
    DROP COLUMN IF EXISTS stationary_since,
    DROP COLUMN IF EXISTS stationary_anchor_lat,
    DROP COLUMN IF EXISTS stationary_anchor_lng;
