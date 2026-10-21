-- Migration 014 (up): heading and route colour for the tracked fleet.
--
-- Two things the fleet was missing:
--
--   * heading — the compass direction the unit is facing. The map used to
--     derive it from the last two positions, which is wrong for a unit that
--     stands still or turns on the spot. A reported heading is stored, kept in
--     the position history and pushed to the browsers.
--   * route colour — the colour of the delivery line belongs to the vehicle,
--     not to the global theme, so two units can be told apart on the map.
--
-- Re-runnable: every statement is guarded.

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS last_heading double precision;

ALTER TABLE vehicles
    ADD COLUMN IF NOT EXISTS route_color VARCHAR(7) NOT NULL DEFAULT '#d0171e'
    CHECK (route_color ~ '^#[0-9a-fA-F]{6}$');

ALTER TABLE vehicle_positions
    ADD COLUMN IF NOT EXISTS heading double precision;

-- The heading is a compass bearing: 0 = north, 90 = east.
DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'vehicles_heading_check'
    ) THEN
        ALTER TABLE vehicles
            ADD CONSTRAINT vehicles_heading_check
            CHECK (last_heading IS NULL OR (last_heading >= 0 AND last_heading < 360));
    END IF;

    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint WHERE conname = 'vehicle_positions_heading_check'
    ) THEN
        ALTER TABLE vehicle_positions
            ADD CONSTRAINT vehicle_positions_heading_check
            CHECK (heading IS NULL OR (heading >= 0 AND heading < 360));
    END IF;
END $$;
