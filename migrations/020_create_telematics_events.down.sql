-- Migration 020 (down): drop the telematics history.
DROP TABLE IF EXISTS telematics_events;
