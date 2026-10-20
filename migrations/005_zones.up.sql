-- Migration 005 (up): operator-drawn zones.
--
-- Deliberately a separate table from admin_regions: those hold official BPS
-- boundaries, are import-only and read-only in the app; zones are polygons the
-- operator draws, edits and deletes. Two tables make "official vs user-drawn"
-- a structural fact rather than a flag that can drift.
--
-- Re-runnable: every statement is guarded.

CREATE TABLE IF NOT EXISTS zones (
    id           BIGSERIAL PRIMARY KEY,
    name         VARCHAR(120) NOT NULL CHECK (char_length(btrim(name)) > 0),
    category     VARCHAR(32)  NOT NULL DEFAULT 'other'
                 CHECK (category IN ('operational', 'restricted', 'planned', 'other')),
    color        VARCHAR(7)   NOT NULL DEFAULT '#2563eb'
                 CHECK (color ~ '^#[0-9a-fA-F]{6}$'),
    description  TEXT         NOT NULL DEFAULT '',
    -- Optional link to an official region. The composite FK keeps a zone from
    -- pointing at a region that does not exist; deleting the region clears it.
    region_level SMALLINT,
    region_code  TEXT,
    -- geometry, not geography: ST_Contains/ST_Intersects (zone x entity, zone x
    -- zone) have no geography form, and geography area/perimeter are still
    -- available by casting. SRID 4326 matches entities and admin_regions.
    geom         geometry(MultiPolygon, 4326) NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT zones_geom_not_empty CHECK (NOT ST_IsEmpty(geom)),
    CONSTRAINT zones_geom_valid     CHECK (ST_IsValid(geom)),
    -- Belt and braces against a stray 0,0 point passing the coordinate check.
    CONSTRAINT zones_area_positive  CHECK (ST_Area(geom) > 0),
    CONSTRAINT zones_region_fk FOREIGN KEY (region_level, region_code)
        REFERENCES admin_regions (level, code)
        ON UPDATE CASCADE ON DELETE SET NULL
);

-- Serves bbox map queries and the spatial joins in the integration stage.
CREATE INDEX IF NOT EXISTS idx_zones_geom ON zones USING GIST (geom);
CREATE INDEX IF NOT EXISTS idx_zones_region ON zones (region_code);
CREATE INDEX IF NOT EXISTS idx_zones_category ON zones (category);

CREATE OR REPLACE FUNCTION set_zones_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_zones_updated_at ON zones;
CREATE TRIGGER trg_zones_updated_at
    BEFORE UPDATE ON zones
    FOR EACH ROW
    EXECUTE FUNCTION set_zones_updated_at();
