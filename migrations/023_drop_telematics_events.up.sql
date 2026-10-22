-- Migration 023 (up): drop the telematics history.
--
-- The speed warning is gone: the fleet no longer raises an OVERSPEED event
-- when a unit drives faster than a limit, so the table behind it has no
-- writer left. The rows it held are the old violations; nobody reads them
-- any more, so they go with the table.

DROP TABLE IF EXISTS telematics_events;
