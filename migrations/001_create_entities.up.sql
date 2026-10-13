-- Migration 001 (up): create entities table.
--
-- Stores geographically-tagged entities: identity, basic attributes,
-- status, coordinates, and audit timestamps.
-- Re-runnable: every statement is guarded (IF NOT EXISTS / OR REPLACE).

CREATE TABLE IF NOT EXISTS entities (
    id         BIGSERIAL PRIMARY KEY,
    name       VARCHAR(255) NOT NULL CHECK (char_length(btrim(name)) > 0),
    type       VARCHAR(100) NOT NULL CHECK (char_length(btrim(type)) > 0),
    status     VARCHAR(50)  NOT NULL DEFAULT 'active'
               CHECK (status IN ('active', 'inactive')),
    latitude   DOUBLE PRECISION NOT NULL CHECK (latitude BETWEEN -90 AND 90),
    longitude  DOUBLE PRECISION NOT NULL CHECK (longitude BETWEEN -180 AND 180),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX IF NOT EXISTS idx_entities_status ON entities (status);
CREATE INDEX IF NOT EXISTS idx_entities_type ON entities (type);

-- Keep updated_at correct even if a future UPDATE forgets to set it.
CREATE OR REPLACE FUNCTION set_entities_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = now();
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_entities_updated_at ON entities;
CREATE TRIGGER trg_entities_updated_at
    BEFORE UPDATE ON entities
    FOR EACH ROW
    EXECUTE FUNCTION set_entities_updated_at();
