-- Seed (optional): 8 Jakarta landmarks for local review, one per type.
-- Run ONCE on a fresh database after 001_create_entities.up.sql:
--
--   PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
--     -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 \
--     -f migrations/seed.sql
--
-- Not numbered (002_*) on purpose: it must NOT run automatically,
-- a fresh migration from scratch stays empty ("No entities yet.").

INSERT INTO entities (name, type, status, latitude, longitude) VALUES
    ('Kepulauan Seribu', 'destination', 'active', -5.9000, 106.5500),
    ('Monas', 'monument', 'active', -6.1754, 106.8272),
    ('Kota Tua', 'historic', 'active', -6.1352, 106.8133),
    ('Taman Mini Indonesia Indah', 'park', 'active', -6.3024, 106.8952),
    ('Ancol', 'recreation', 'active', -6.1256, 106.8317),
    ('Ragunan', 'zoo', 'active', -6.3118, 106.8209),
    ('Jalan Sabang', 'culinary', 'active', -6.1850, 106.8290),
    ('Hotel Borobudur', 'lodging', 'active', -6.1900, 106.8300);
