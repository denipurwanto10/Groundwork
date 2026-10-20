DROP INDEX IF EXISTS idx_admin_regions_geom_display;
ALTER TABLE admin_regions DROP COLUMN IF EXISTS geom_display;
