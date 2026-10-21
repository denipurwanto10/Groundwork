-- Migration 013 (up): make zone reads cheap.
--
-- Reading a zone used to recompute three expensive PostGIS expressions on
-- every single request:
--
--   1. the display simplification (ST_SimplifyPreserveTopology) — about a
--      second for a polygon with 200k+ vertices,
--   2. the geodesic area and perimeter (ST_Area/ST_Perimeter on geography) —
--      several hundred milliseconds for the same polygon,
--   3. the entity and vehicle counts (ST_Covers per row).
--
-- That is why GET /api/zones/:id took seconds on a big zone while the same
-- work in psql looked fast: the psql run measured the geometry, not the whole
-- statement. The values do not change while a zone is not edited, so they are
-- now computed once, when the shape is written, and read from the row.
--
-- The derived values live in their own table, zone_metrics, and not in
-- generated columns of zones. A stored generated column is recomputed on
-- every UPDATE of its row, and the counters of a zone change whenever a
-- place is added or a unit crosses a border: with the counters in zones,
-- every position report of the fleet rewrote the whole zone row (geometry
-- included) and held its lock while it did. A side table keeps the counters
-- cheap and the zones table quiet.
--
-- Re-runnable: every statement is guarded.

-- The metrics of a zone: the copy the map draws, the geodesic measurements and
-- the live counters.
CREATE TABLE IF NOT EXISTS zone_metrics (
    zone_id        BIGINT PRIMARY KEY REFERENCES zones (id) ON DELETE CASCADE,
    -- Display copy: the polygon the map draws. Above 2.000 vertices it is
    -- simplified with a 0.0005 degree tolerance (the rule the API used
    -- before), so the response stays small without touching the stored shape.
    geom_display   geometry(Geometry, 4326) NOT NULL,
    area_km2       DOUBLE PRECISION NOT NULL DEFAULT 0,
    perimeter_km   DOUBLE PRECISION NOT NULL DEFAULT 0,
    vertex_count   INTEGER NOT NULL DEFAULT 0,
    entity_count   INTEGER NOT NULL DEFAULT 0,
    vehicle_count  INTEGER NOT NULL DEFAULT 0,
    updated_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- The first version of this migration stored the values in generated columns
-- of zones. They are dropped here: the side table replaces them.
ALTER TABLE zones DROP COLUMN IF EXISTS geom_display;
ALTER TABLE zones DROP COLUMN IF EXISTS area_km2;
ALTER TABLE zones DROP COLUMN IF EXISTS perimeter_km;
ALTER TABLE zones DROP COLUMN IF EXISTS vertex_count;
ALTER TABLE zones DROP COLUMN IF EXISTS entity_count;
ALTER TABLE zones DROP COLUMN IF EXISTS vehicle_count;

-- The trigger of the first version counted into the columns that are dropped
-- above, so it goes with them.
DROP TRIGGER IF EXISTS trg_zones_recount ON zones;
DROP FUNCTION IF EXISTS zones_recount();

-- The metrics of a zone that was just created or reshaped are computed from
-- scratch: the old shape does not describe the new one.
CREATE OR REPLACE FUNCTION zones_sync_metrics()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO zone_metrics (zone_id, geom_display, area_km2, perimeter_km,
                              vertex_count, entity_count, vehicle_count)
    VALUES (
        NEW.id,
        CASE
            WHEN ST_NPoints(NEW.geom) > 2000
                THEN ST_SimplifyPreserveTopology(NEW.geom, 0.0005)
            ELSE NEW.geom
        END,
        ST_Area(NEW.geom::geography) / 1e6,
        ST_Perimeter(NEW.geom::geography) / 1000,
        ST_NPoints(NEW.geom),
        (SELECT count(*) FROM entities e WHERE ST_Covers(NEW.geom, e.geom::geometry)),
        (SELECT count(*) FROM vehicles v
          WHERE v.last_geom IS NOT NULL AND ST_Covers(NEW.geom, v.last_geom::geometry))
    )
    ON CONFLICT (zone_id) DO UPDATE SET
        geom_display  = EXCLUDED.geom_display,
        area_km2      = EXCLUDED.area_km2,
        perimeter_km  = EXCLUDED.perimeter_km,
        vertex_count  = EXCLUDED.vertex_count,
        entity_count  = EXCLUDED.entity_count,
        vehicle_count = EXCLUDED.vehicle_count,
        updated_at    = now();
    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_zones_metrics ON zones;
CREATE TRIGGER trg_zones_metrics
    AFTER INSERT OR UPDATE OF geom ON zones
    FOR EACH ROW
    EXECUTE FUNCTION zones_sync_metrics();

-- An entity that appears, moves or disappears changes the count of every zone
-- that covers it. ST_Covers is index-aware, so only the zones whose bounding
-- box contains the point are tested.
CREATE OR REPLACE FUNCTION zones_sync_entity_count()
RETURNS TRIGGER AS $$
BEGIN
    -- A rename or a status change does not move the place.
    IF TG_OP = 'UPDATE' AND NEW.geom IS NOT DISTINCT FROM OLD.geom THEN
        RETURN NULL;
    END IF;

    -- The old place leaves the zones it was in.
    IF TG_OP IN ('DELETE', 'UPDATE') THEN
        UPDATE zone_metrics m
           SET entity_count = m.entity_count - 1, updated_at = now()
          FROM zones z
         WHERE z.id = m.zone_id AND ST_Covers(z.geom, OLD.geom::geometry);
    END IF;

    -- The new place joins the zones it is in now.
    IF TG_OP IN ('INSERT', 'UPDATE') THEN
        UPDATE zone_metrics m
           SET entity_count = m.entity_count + 1, updated_at = now()
          FROM zones z
         WHERE z.id = m.zone_id AND ST_Covers(z.geom, NEW.geom::geometry);
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_entities_zone_count ON entities;
CREATE TRIGGER trg_entities_zone_count
    AFTER INSERT OR UPDATE OR DELETE ON entities
    FOR EACH ROW
    EXECUTE FUNCTION zones_sync_entity_count();

-- The same for the fleet: a unit that reports a position is counted in every
-- zone it is inside.
CREATE OR REPLACE FUNCTION zones_sync_vehicle_count()
RETURNS TRIGGER AS $$
BEGIN
    -- A rename does not move the unit.
    IF TG_OP = 'UPDATE' AND NEW.last_geom IS NOT DISTINCT FROM OLD.last_geom THEN
        RETURN NULL;
    END IF;

    -- The old position leaves the zones it was in.
    IF TG_OP IN ('DELETE', 'UPDATE') AND OLD.last_geom IS NOT NULL THEN
        UPDATE zone_metrics m
           SET vehicle_count = m.vehicle_count - 1, updated_at = now()
          FROM zones z
         WHERE z.id = m.zone_id AND ST_Covers(z.geom, OLD.last_geom::geometry);
    END IF;

    -- The new position joins the zones it is in now.
    IF TG_OP IN ('INSERT', 'UPDATE') AND NEW.last_geom IS NOT NULL THEN
        UPDATE zone_metrics m
           SET vehicle_count = m.vehicle_count + 1, updated_at = now()
          FROM zones z
         WHERE z.id = m.zone_id AND ST_Covers(z.geom, NEW.last_geom::geometry);
    END IF;

    RETURN NULL;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_vehicles_zone_count ON vehicles;
CREATE TRIGGER trg_vehicles_zone_count
    AFTER INSERT OR UPDATE OR DELETE ON vehicles
    FOR EACH ROW
    EXECUTE FUNCTION zones_sync_vehicle_count();

-- updated_at must mean "the operator edited this zone", not "a vehicle drove
-- through it". The old trigger fired on every UPDATE, including the count
-- updates, which made the map reload for nothing.
DROP TRIGGER IF EXISTS trg_zones_updated_at ON zones;
CREATE TRIGGER trg_zones_updated_at
    BEFORE UPDATE ON zones
    FOR EACH ROW
    WHEN (
        OLD.name        IS DISTINCT FROM NEW.name
     OR OLD.category    IS DISTINCT FROM NEW.category
     OR OLD.color       IS DISTINCT FROM NEW.color
     OR OLD.description IS DISTINCT FROM NEW.description
     OR OLD.region_level IS DISTINCT FROM NEW.region_level
     OR OLD.region_code  IS DISTINCT FROM NEW.region_code
     OR OLD.geom         IS DISTINCT FROM NEW.geom
    )
    EXECUTE FUNCTION set_zones_updated_at();

-- One-off fill for the rows that already existed when this migration ran.
INSERT INTO zone_metrics (zone_id, geom_display, area_km2, perimeter_km,
                          vertex_count, entity_count, vehicle_count)
SELECT z.id,
       CASE
           WHEN ST_NPoints(z.geom) > 2000
               THEN ST_SimplifyPreserveTopology(z.geom, 0.0005)
           ELSE z.geom
       END,
       ST_Area(z.geom::geography) / 1e6,
       ST_Perimeter(z.geom::geography) / 1000,
       ST_NPoints(z.geom),
       (SELECT count(*) FROM entities e WHERE ST_Covers(z.geom, e.geom::geometry)),
       (SELECT count(*) FROM vehicles v
         WHERE v.last_geom IS NOT NULL AND ST_Covers(z.geom, v.last_geom::geometry))
  FROM zones z
ON CONFLICT (zone_id) DO UPDATE SET
    geom_display  = EXCLUDED.geom_display,
    area_km2      = EXCLUDED.area_km2,
    perimeter_km  = EXCLUDED.perimeter_km,
    vertex_count  = EXCLUDED.vertex_count,
    entity_count  = EXCLUDED.entity_count,
    vehicle_count = EXCLUDED.vehicle_count,
    updated_at    = now();
