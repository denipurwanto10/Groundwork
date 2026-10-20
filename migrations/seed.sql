-- Seed (optional): 8 landmarks across Indonesia for local review, one per type.
-- Run ONCE on a fresh database after 001_create_entities.up.sql:
--
--   PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
--     -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 \
--     -f migrations/seed.sql
--
-- Not numbered (002_*) on purpose: it must NOT run automatically,
-- a fresh migration from scratch stays empty ("No entities yet.").

INSERT INTO entities (name, type, status, latitude, longitude) VALUES
    ('Tanah Lot', 'destination', 'active', -8.6210, 115.0870),
    ('Monas', 'monument', 'active', -6.1754, 106.8272),
    ('Benteng Rotterdam', 'historic', 'active', -5.1340, 119.4050),
    ('Tanjung Puting', 'park', 'active', -3.0000, 111.9500),
    ('Danau Toba', 'recreation', 'active', 2.6840, 98.8750),
    ('Gembira Loka', 'zoo', 'active', -7.8030, 110.3970),
    ('Sate Padang Ajo Ramon', 'culinary', 'active', -0.9470, 100.3560),
    ('Hotel Novotel Balikpapan', 'lodging', 'active', -1.2650, 116.8320);
