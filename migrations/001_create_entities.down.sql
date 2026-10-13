-- Migration 001 (down): drop entities table and its helper objects.
-- Trigger is dropped before the function that it depends on.

DROP TRIGGER IF EXISTS trg_entities_updated_at ON entities;
DROP FUNCTION IF EXISTS set_entities_updated_at();
DROP TABLE IF EXISTS entities;
