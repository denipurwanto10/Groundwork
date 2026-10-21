-- Empty state: the workspace as a brand-new database looks before the demo
-- data is added.
--
-- Migration 015 seeds eight landmarks so a fresh database has something to
-- show. This script takes them away again, together with the demo fleet and
-- its history, so the "No entities yet." screen can be reproduced and looked
-- at.
--
--   PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
--     -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 \
--     -f scripts/empty-state.sql
--
-- It is a script, not a migration: a migration runs once, this one is a
-- switch you flip whenever you want the empty screen back.
--
-- Zones and regions are left alone: they are the work of the operator, not
-- demo data.

BEGIN;

DELETE FROM vehicle_routes;
DELETE FROM vehicle_positions;
DELETE FROM geofence_events;
DELETE FROM vehicles;

DELETE FROM entities;

COMMIT;
