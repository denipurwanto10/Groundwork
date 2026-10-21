-- Migration 011 (down): drop the geofence history.
DROP TABLE IF EXISTS geofence_events;
