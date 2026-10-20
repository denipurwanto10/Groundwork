-- Migration 002 (down): drop the spatial column and its index.
--
-- The postgis extension is intentionally left installed — later migrations
-- (admin regions, polygons, vehicles) depend on it, and dropping it would
-- cascade into objects this migration knows nothing about.
-- latitude/longitude are untouched, so all entity data survives.

DROP INDEX IF EXISTS idx_entities_geom;
ALTER TABLE entities DROP COLUMN IF EXISTS geom;
