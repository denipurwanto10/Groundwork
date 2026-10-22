-- Migration 028 (down): drop the status of the stops.
--
-- The stops go back to what they were: a JSON array of places in
-- vehicle_routes.stops, without a status of their own. The routes that ran
-- keep their history in vehicle_routes; only the per-stop status is lost.

DROP TABLE IF EXISTS route_stops;
