-- Migration 024 (up): one colour for every delivery line.
--
-- The route colour used to be a choice per unit. It is not any more: every
-- delivery line wears the primary red of the theme, so the fleet form has no
-- picker and the map draws the same red for every unit.
--
-- Re-runnable: only the rows that still carry another colour are touched.

UPDATE vehicles
SET route_color = '#d0171e'
WHERE route_color IS DISTINCT FROM '#d0171e';
