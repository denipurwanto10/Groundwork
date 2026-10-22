# Migrations

Plain SQL files, applied in filename order. No migration framework
(deliberate: 2-day project, single table).

## Files

| File | Isi |
| ---- | --- |
| `001_create_entities.up.sql` | tabel `entities` + index + trigger `updated_at` |
| `002_entities_postgis.up.sql` | PostGIS + kolom `geom` (geography point) |
| `003_admin_regions.up.sql` | tabel `admin_regions` (batas administratif, read-only) |
| `004_region_display_geom.up.sql` | salinan tampilan yang disederhanakan per level |
| `005_zones.up.sql` | tabel `zones` (polygon buatan operator) |
| `006_vehicles.up.sql` | tabel `vehicles` + `vehicle_positions` |
| `007_vehicle_routes.up.sql` | tabel `vehicle_routes` (rute pengiriman) |
| `008_users.up.sql` | tabel `users` (JWT auth) |
| `009_add_user_roles.up.sql` | peran `admin` / `viewer` / `driver` (+ `staff` di 022) |
| `010_vehicle_driver.up.sql` | kolom `vehicles.driver_id` |
| `011_create_geofence_events.up.sql` | tabel `geofence_events` (lintasan batas zona) |
| `012_user_accounts.up.sql` | kolom akun: username, nama, gender |
| `013_zone_performance.up.sql` | `zone_metrics`: salinan tampilan, luas, keliling, jumlah titik, hitungan entitas/kendaraan |
| `014_vehicle_heading_and_route_color.up.sql` | `vehicles.last_heading`, `vehicles.route_color`, `vehicle_positions.heading` |
| `015_seed_entities.up.sql` | 8 landmark se-Indonesia (data review) |
| `016_seed_regions.up.sql` | garis batas negara + 34 provinsi (versi disederhanakan) |
| `017_create_audit_logs.up.sql` | tabel `audit_logs` (jejak aktivitas: siapa mengubah apa, kapan) |
| `018_route_started_at.up.sql` | `vehicle_routes.started_at` (rute menunggu driver menekan "Mulai Perjalanan") |
| `019_route_stops.up.sql` | `vehicle_routes.stops` (JSONB: titik-tujuan rute multi-stop dalam urutan kunjungan) |
| `020_create_telematics_events.up.sql` | tabel `telematics_events` (peringatan kecepatan — dibuang lagi di 023) |
| `021_create_tasks.up.sql` | tabel `tasks` (work order: judul, status, penerima tugas, tempat, tenggat) |
| `022_add_staff_role.up.sql` | peran `staff` (mengerjakan tugas di lapangan) |
| `023_drop_telematics_events.up.sql` | buang `telematics_events` (fitur peringatan kecepatan dihapus) |
| `024_single_route_color.up.sql` | semua `vehicles.route_color` diseragamkan ke merah primer tema (satu warna rute untuk semua unit) |
| `025_report_indexes.up.sql` | index tanggal untuk `vehicle_routes` dan `tasks` (bacaan rentang laporan operasional) |
| `026_create_push_subscriptions.up.sql` | tabel `push_subscriptions` (subscription Web Push per browser: endpoint, kunci p256dh/auth) |

Tiap file punya pasangan `*.down.sql` untuk rollback (urutan terbalik).

Migrasi 013 mengubah cara baca zona: nilai turunan (salinan tampilan, luas,
keliling, jumlah titik) dan hitungan entitas/kendaraan dihitung sekali saat
bentuk zona disimpan, lalu dibaca dari `zone_metrics`. Ini yang membuat
`GET /api/zones/:id` untuk polygon 227 ribu titik turun dari ±7 detik ke
±60 milidetik, dan membuat hitungan zona tetap benar tanpa memuat ulang
peta.

Migrasi 015 dan 016 mengisi database baru dengan data, jadi workspace tidak
pernah kosong setelah migrasi. Ingin kembali ke layar **"No entities yet."**?
Jalankan `scripts/empty-state.sql` (bukan migrasi — skrip yang bisa dipakai
kapan saja):

```bash
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f scripts/empty-state.sql
```

## Run (needs PostgreSQL credentials in `.env`)

```bash
# 1. Make sure the database exists (once):
#    CREATE DATABASE entity_management;

# 2. Load connection values from .env and apply:
export $(grep -v '^#' .env | xargs)
for f in migrations/0*.up.sql; do
  PGPASSWORD="$DB_PASSWORD" psql \
    -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
    -v ON_ERROR_STOP=1 -f "$f" || break
done

# Rollback (destroys the tables and their data):
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.down.sql
```

`ON_ERROR_STOP=1` makes psql exit non-zero on the first error so a
half-applied file is never reported as success.
