-- Migration 016 (down): remove the bundled boundary outlines.
--
-- Only the rows this migration seeded are removed: the ones that carry the
-- "bundled outlines" source version. A boundary imported afterwards from the
-- full COD-AB dataset stays.

DELETE FROM admin_regions
 WHERE source_version = 'COD-AB bundled outlines (simplified)';
