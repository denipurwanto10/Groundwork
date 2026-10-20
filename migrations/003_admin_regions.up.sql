-- Migration 003 (up): administrative regions (country -> village).
--
-- Source: HDX dataset `cod-ab-idn` — "Indonesia - Subnational Administrative
-- Boundaries", published by Badan Pusat Statistik (BPS), licence CC BY-IGO.
-- Level numbering follows the dataset: 0 country, 1 province, 2 regency/city,
-- 3 district, 4 village.
--
-- `code` is the publisher's own region code (BPS), never generated here, so
-- `parent_code` is the hierarchy key. Codes are stored verbatim (leading zeros
-- preserved) — comparing them as numbers would break the hierarchy.
--
-- Re-runnable: every statement is guarded, and (level, code) is unique so the
-- importer can upsert instead of duplicating.

CREATE TABLE IF NOT EXISTS admin_regions (
    id             BIGSERIAL PRIMARY KEY,
    code           TEXT     NOT NULL CHECK (char_length(btrim(code)) > 0),
    name           TEXT     NOT NULL CHECK (char_length(btrim(name)) > 0),
    level          SMALLINT NOT NULL CHECK (level BETWEEN 0 AND 4),
    parent_code    TEXT,
    -- Nullable: a handful of source features ship empty/degenerate geometry and
    -- must not abort the whole import. Absence is reported after each run.
    geom           geography(MultiPolygon, 4326),
    source         TEXT     NOT NULL DEFAULT 'HDX cod-ab-idn (BPS)',
    source_version TEXT,
    license        TEXT     NOT NULL DEFAULT 'CC BY-IGO',
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX IF NOT EXISTS uq_admin_regions_level_code ON admin_regions (level, code);
CREATE INDEX IF NOT EXISTS idx_admin_regions_code ON admin_regions (code);
-- Serves "regions of level N under parent P", the drill-down query.
CREATE INDEX IF NOT EXISTS idx_admin_regions_level_parent ON admin_regions (level, parent_code);
-- Serves "which region contains this point" (ST_Covers) and bbox map queries.
CREATE INDEX IF NOT EXISTS idx_admin_regions_geom ON admin_regions USING GIST (geom);

CREATE OR REPLACE FUNCTION set_admin_regions_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_admin_regions_updated_at ON admin_regions;
CREATE TRIGGER trg_admin_regions_updated_at
    BEFORE UPDATE ON admin_regions
    FOR EACH ROW
    EXECUTE FUNCTION set_admin_regions_updated_at();
