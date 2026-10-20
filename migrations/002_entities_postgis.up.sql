-- Migration 002 (up): enable PostGIS and give entities a real spatial column.
--
-- Why: every later feature (region lookup, "entities inside this polygon",
-- distance search, vehicle geofencing) needs real geometry.
--
-- Design: latitude/longitude stay the source of truth that the API and the UI
-- already use; `geom` is a GENERATED column derived from them. That way the
-- existing CRUD code, validation and seed data keep working untouched, and the
-- two can never drift apart.
--
-- Re-runnable: every statement is guarded.

CREATE EXTENSION IF NOT EXISTS postgis;

ALTER TABLE entities
    ADD COLUMN IF NOT EXISTS geom geography(Point, 4326)
    GENERATED ALWAYS AS (ST_SetSRID(ST_MakePoint(longitude, latitude), 4326)::geography) STORED;

-- GiST is the only index type PostGIS spatial operators can use.
CREATE INDEX IF NOT EXISTS idx_entities_geom ON entities USING GIST (geom);
