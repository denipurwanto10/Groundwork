-- Migration 013 (down): drop the precomputed zone metrics and their triggers.
--
-- The stored polygon and the zone rows go back to the state of migration 012:
-- the display copy, the measurements and the counters are computed per
-- request again.

DROP TRIGGER IF EXISTS trg_entities_zone_count ON entities;
DROP TRIGGER IF EXISTS trg_vehicles_zone_count ON vehicles;
DROP TRIGGER IF EXISTS trg_zones_metrics ON zones;
DROP TRIGGER IF EXISTS trg_zones_recount ON zones;

DROP FUNCTION IF EXISTS zones_sync_entity_count();
DROP FUNCTION IF EXISTS zones_sync_vehicle_count();
DROP FUNCTION IF EXISTS zones_sync_metrics();
DROP FUNCTION IF EXISTS zones_recount();

DROP TABLE IF EXISTS zone_metrics;

-- Restore the unconditional updated_at trigger of migration 005.
DROP TRIGGER IF EXISTS trg_zones_updated_at ON zones;
CREATE TRIGGER trg_zones_updated_at
    BEFORE UPDATE ON zones
    FOR EACH ROW
    EXECUTE FUNCTION set_zones_updated_at();
