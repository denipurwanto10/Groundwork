# Migrations

Plain SQL files, applied in filename order. No migration framework
(deliberate: 2-day project, single table).

## Files

- `001_create_entities.up.sql` — creates `entities` + indexes + `updated_at` trigger.
- `001_create_entities.down.sql` — drops them (rollback only).

## Run (needs PostgreSQL credentials in `.env`)

```bash
# 1. Make sure the database exists (once):
#    CREATE DATABASE entity_management;

# 2. Load connection values from .env and apply:
export $(grep -v '^#' .env | xargs)
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.up.sql

# Rollback (destroys the table and its data):
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.down.sql
```

`ON_ERROR_STOP=1` makes psql exit non-zero on the first error so a
half-applied file is never reported as success.
