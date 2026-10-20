-- Migration 003 (down): drop administrative regions.
DROP TRIGGER IF EXISTS trg_admin_regions_updated_at ON admin_regions;
DROP FUNCTION IF EXISTS set_admin_regions_updated_at();
DROP TABLE IF EXISTS admin_regions;
