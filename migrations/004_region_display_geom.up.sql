-- Precomputed display geometry for admin_regions.
--
-- The BPS source polygons are far too detailed to serialise per request:
-- the country outline alone is 2.5M points and ST_SimplifyPreserveTopology
-- takes ~4 minutes on it. Simplifying on every read is not viable, so the
-- generalised shape is stored once and served straight from this column.
--
-- Tolerance is per level, in degrees (~110 km per degree at the equator):
--   level 1 province  0.01  ~ 1.1 km
--   level 2 regency   0.005 ~ 550 m
--   level 3 district  0.002 ~ 220 m
--   level 4 village   0.001 ~ 110 m
-- Level 0 (country) is deliberately left NULL: provinces tile the country,
-- so frontends draw the outline from level 1 and skip the 4-minute simplify.

ALTER TABLE admin_regions
    ADD COLUMN IF NOT EXISTS geom_display geometry(Geometry, 4326)
    GENERATED ALWAYS AS (
        CASE level
            WHEN 1 THEN ST_Multi(ST_SimplifyPreserveTopology(geom::geometry, 0.01))
            WHEN 2 THEN ST_Multi(ST_SimplifyPreserveTopology(geom::geometry, 0.005))
            WHEN 3 THEN ST_Multi(ST_SimplifyPreserveTopology(geom::geometry, 0.002))
            WHEN 4 THEN ST_Multi(ST_SimplifyPreserveTopology(geom::geometry, 0.001))
            ELSE NULL
        END
    ) STORED;

CREATE INDEX IF NOT EXISTS idx_admin_regions_geom_display
    ON admin_regions USING GIST (geom_display);

COMMENT ON COLUMN admin_regions.geom_display IS
    'Generalised geometry for map display, precomputed per admin level. NULL for level 0.';
