# Groundwork — Field Operations, Mapped
 
Platform operasi wilayah untuk Indonesia: peta sebagai antarmuka kerja.
Setiap aset punya lokasi, setiap operasi punya wilayah, setiap pergerakan
punya rute. Go + Gin + PostgreSQL/PostGIS di backend, React + Vite +
TypeScript + Leaflet di frontend.

- **Entitas** — titik aset (destinasi, monumen, kuliner, …) dengan CRUD penuh.
- **Wilayah administratif** — batas resmi provinsi/kabupaten/kecamatan (L0–L3).
- **Zona operasional** — polygon buatan sendiri, atau dibuat langsung dari
  wilayah administratif / gugus pulau.
- **Armada** — posisi kendaraan real-time lewat WebSocket, dengan simulator
  sebagai sumber data; setiap unit punya arah tempuh, dan semua jalur rutenya
  digambar dengan warna merah primer tema.
- **Rute** — jalur jalan raya dari–ke per kendaraan (OSRM), digambar di peta
  dan ditutup otomatis saat unit tiba, lengkap dengan jarak tempuh dan ETA
  (jam tiba) di card kendaraan.
- **Pengingat servis** — odometer seumur kendaraan, jadwal servis per unit,
  dan peringatan lewat Web Push saat unit mendekati jadwalnya (lihat
  [Pengingat Servis](#pengingat-servis)).

## Daftar Isi

- [Tech Stack](#tech-stack)
- [Basemap](#basemap)
- [Arsitektur](#arsitektur)
- [Mulai Cepat](#mulai-cepat)
- [Data Wilayah](#data-wilayah)
- [Endpoint API](#endpoint-api)
- [Perilaku Zona](#perilaku-zona)
- [Audit Log](#audit-log-jejak-aktivitas)
- [Driver View](#driver-view-layar-driver)
- [Status Per-Stop](#status-per-stop-bukti-singgah)
- [Task Management](#task-management-work-orders)
- [Laporan & Export](#laporan--export-laporan-operasional)
- [Pengingat Servis](#pengingat-servis)
- [Notifikasi Desktop](#notifikasi-desktop-web-push)
- [Playback Rute](#playback-rute-history-perjalanan)
- [Keamanan](#keamanan)
- [Pengujian](#pengujian)
- [Struktur Proyek](#struktur-proyek)

## Tech Stack

| Lapisan  | Teknologi |
| -------- | --------- |
| Backend  | Go 1.27, Gin, pgx/v5 (`pgxpool`), `coder/websocket`, `godotenv` |
| Frontend | React 19, TypeScript, Vite 8, Leaflet 1.9 + React-Leaflet 5, Leaflet-Geoman |
| Database | PostgreSQL 18 + PostGIS (`geometry(MultiPolygon,4326)`, `geography(Point,4326)`, `geometry(LineString,4326)`) |
| Peta     | tile raster Google Maps (`mt0–mt3.google.com`), tanpa API key |
| Routing  | OSRM publik (`router.project-osrm.org`), fallback garis lurus bila engine tidak menjawab |
| Migrasi  | SQL polos di `migrations/`, tanpa framework |
| HTTP     | kompresi gzip bawaan (`internal/middleware`), tanpa dependensi baru |

## Basemap

Satu gaya peta saja: **Google Maps** (road map), diambil langsung dari server
tile Google — tanpa API key, tanpa akun, tanpa tagihan.

- URL: `https://{s}.google.com/vt/lyrs=m&hl=en&x={x}&y={y}&z={z}`,
  `{s}` = `mt0` … `mt3` (lihat `frontend/src/components/EntityMap.tsx`)
- Ganti gaya cukup mengganti satu nilai `lyrs=`:
  `m` = jalan raya (default), `y` = satelit + label, `s` = satelit polos,
  `p` = terrain.
- Provider lain lewat `VITE_MAP_TILES_URL` di `frontend/.env` (mis. Maps
  Static API resmi dengan API key).
- Kredit "Map data © Google" tetap ada di DOM, transparan dan baru muncul
  saat kursor/fokus keyboard menyentuh pojok peta (lihat `theme-red.css`).
- Tema gelap membalik tile lewat filter CSS, supaya peta tidak menyilaukan.

> Catatan: endpoint `mt*.google.com/vt` bukan API resmi Google Maps (tidak
> butuh key, tapi di luar proyek demo pribadi bertentangan dengan ToS Google).
> Untuk produksi, pakai API resmi: set `VITE_MAP_TILES_URL` ke Maps Static
> API dengan key milik sendiri.

## Arsitektur

```text
frontend/ (React + Vite :5173)
  │  fetch /api/*  +  WebSocket /api/vehicles/stream
  ▼  (dev proxy: Vite, Gin tanpa CORS)
Go + Gin (:8080 default, :8091 di mesin ini) ── pgxpool ──► PostgreSQL + PostGIS
  └── internal/routing ──HTTP──► OSRM
```

**Backend** (`cmd/` + `internal/`)

| Paket | Isi |
| ----- | --- |
| `cmd/server` | entry point, Gin, graceful shutdown, registrasi route |
| `cmd/import-regions` | importer dataset COD-AB (zip/geojson) → `admin_regions`; bisa mengunduh sendiri (`-url`) |
| `cmd/simulator` | pengirim posisi palsu untuk armada (sumber data live tracking) |
| `internal/config`, `internal/db` | `.env` + DSN, `pgxpool` + health check |
| `internal/entity`, `internal/vehicle` | model + `Normalize`/`Validate` + unit test; `vehicle` juga model rute (`Route`, `RouteStop`) |
| `internal/handler` | entities, zones, vehicles, routes, regions, islands, zone-from-region, reports, health, auth |
| `internal/middleware` | kompresi gzip + `Vary`/`Content-Encoding` |
| `internal/routing` | klien OSRM (`OSRM_BASE_URL`, `off` = fallback `direct`) |
| `internal/ws` | hub fan-out untuk stream posisi kendaraan |

**Frontend** (`frontend/src/`)

`App.tsx` memegang state dan tab-tabnya (Overview / Places / Zones / Fleet /
Tasks / Reports / Users / Activity Log); `EntityMap` menyusun layer peta:
`RegionLayer` (batas administratif), `ZoneLayer` (polygon + editor Geoman),
`VehicleLayer` (puck armada), `RouteLayer` (jalur). Panel: `EntityList`/
`EntityDetail`/`EntityForm`/`ZonePanel`/`RegionZonePicker`/`VehiclePanel`/
`TaskBoard`/`ReportsPanel`. Kontrak API di `lib/types.ts`, client di
`lib/api.ts`, stream di `lib/vehicleStream.ts`. Tema visual terakhir
(`theme-red.css`) menentukan warna pin dan kontrol peta.

REST untuk snapshot, WebSocket untuk stream posisi. Autentikasi JWT lokal
(`admin` / `staff` / `viewer` / `driver`); hanya admin yang boleh menulis
data workspace, dan hanya driver + staff yang boleh memindahkan status tugas.

## Mulai Cepat

### Prasyarat

- Windows + Git Bash (perintah di bawah memakai Git Bash)
- Go 1.27+ (`go version`)
- Node.js 20+ (`node --version`)
- PostgreSQL 14+ **dengan PostGIS** (yang dipakai di sini: PostgreSQL 18.6)

### 1. Database dan `.env`

```bash
# Buat database sekali saja:
CREATE DATABASE entity_management;

cp .env.example .env      # lalu isi DB_PASSWORD dan JWT_SECRET
```

| Variable | Default | Keterangan |
| -------- | ------- | ---------- |
| `SERVER_HOST` | `127.0.0.1` | Antarmuka tempat server listen. Loopback = hanya mesin ini |
| `SERVER_PORT` | `8080` | Port backend (di mesin ini dipakai `8091`) |
| `SERVER_COMPRESSION` | `true` | Kompresi gzip untuk respons besar |
| `SERVER_COMPRESS_THRESHOLD` | `1024` | Ukuran minimum body yang dikompres (byte) |
| `SERVER_READ_TIMEOUT` | `10s` | Batas baca request |
| `SERVER_WRITE_TIMEOUT` | `10s` | Batas tulis respons |
| `SERVER_IDLE_TIMEOUT` | `60s` | Batas koneksi menganggur |
| `SERVER_OFFLINE_AFTER` | `60s` | Berapa lama unit boleh diam sebelum dihitung offline; `0` mematikan pemeriksaannya |
| `SERVER_IDLE_AFTER` | `10m` | Berapa lama unit boleh diam (masih melapor) sebelum dihitung idle; `0` mematikan deteksi idle server |
| `SERVER_IDLE_RADIUS_M` | `25` | Jarak maksimum dari jangkar (meter) yang masih dihitung "tidak berpindah" |
| `SERVER_IDLE_MAX_SPEED_KMH` | `2` | Kecepatan di atas ini (km/jam) selalu dianggap bergerak |
| `DB_HOST` / `DB_PORT` | `localhost` / `5432` | Koneksi Postgres |
| `DB_USER` / `DB_NAME` | `postgres` / `entity_management` | Koneksi Postgres |
| `DB_PASSWORD` | _(kosong)_ | Wajib diisi lokal, jangan commit |
| `DB_SSL_MODE` | `disable` | `require` untuk server ber-TLS |
| `JWT_SECRET` | _(kosong)_ | Kunci tanda tangan token; wajib isi (`openssl rand -base64 32`) |
| `JWT_TTL` | `24h` | Masa berlaku access token |
| `JWT_REFRESH_TTL` | `720h` | Masa berlaku refresh token (30 hari) |
| `OSRM_BASE_URL` | `https://router.project-osrm.org` | Set `off` untuk rute garis lurus |
| `VITE_BACKEND` (`frontend/`) | `http://127.0.0.1:8091` | Target proxy Vite saat dev |
| `VITE_API_BASE_URL` (`frontend/`) | _(kosong)_ | Origin backend untuk build produksi |
| `VITE_MAP_TILES_URL` (`frontend/`) | tile Google Maps | Provider tile lain (mis. Maps Static API) |

### 2. Migrasi

Plain SQL, dijalankan berurutan. Detail per file: `migrations/README.md`.

```bash
export $(grep -v '^#' .env | xargs)
for f in migrations/0*.up.sql; do
  PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
    -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -f "$f" || break
done
```

Migrasi 015 mengisi 8 landmark se-Indonesia dan 016 mengisi garis batas
negara + 34 provinsi (versi disederhanakan, sekitar 100 KB SQL), jadi
database baru tidak pernah kosong. Ingin kembali ke layar **"No entities
yet."**? Jalankan `scripts/empty-state.sql` — isinya skrip, bukan migrasi,
jadi bisa dipakai kapan saja:

```bash
PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
  -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -f scripts/empty-state.sql
```

Rollback: jalankan `migrations/*.down.sql` dengan urutan terbalik — menghapus
tabel beserta datanya.

### 3. Akun pertama

Database yang baru dimigrasi belum punya akun, jadi tidak ada yang bisa masuk.
Layar login berubah jadi form **"Create the first admin"** selama workspace
masih kosong (`GET /api/auth/status` yang memberi tahu). Setelah itu, akun
lain dibuat oleh admin dari tab **Users**.

#### Akun demo di workspace lokal ini

Akun-akun ini sudah dibuat di database lokal dan **semuanya memakai password
yang sama**:

| Username | Peran | Untuk apa |
| -------- | ----- | --------- |
| `admin` | admin | akun penuh: semua tab, armada, zona, akun, laporan |
| `budi.driver` | driver | layar `/driver` — unit `TRK-001` ditugaskan ke sini |
| `taskdriver` | driver | driver lain, untuk menguji batas akses |
| `taskstaff` | staff | orang lapangan (hanya memindahkan status tugas) |
| `fleetview` | viewer | hanya membaca |

Password semuanya: **`Groundwork123`**

> Password ini **hanya untuk demo lokal** — API-nya listen di loopback, jadi
> tidak ada yang bisa memakainya dari luar mesin ini. Password database, JWT
> secret, dan kunci privat VAPID tetap di `.env` (tidak masuk repo).

### 4. Data wilayah (opsional, tapi lebih detail)

Migrasi 016 sudah mengisi garis batas negara dan provinsi. Untuk batas yang
lebih detail (kabupaten, kecamatan) dan bentuk yang persis, impor dataset
COD-AB penuh — lihat [Data Wilayah](#data-wilayah).

### 5. Backend

```bash
go build -o bin/server.exe ./cmd/server
SERVER_PORT=8091 ./bin/server.exe        # :8080 bila .env tidak diubah
curl http://127.0.0.1:8091/health
```

Server listen di `127.0.0.1` (loopback) secara bawaan: API ini berbicara
HTTP polos tanpa sertifikat, jadi tidak boleh terbuka ke jaringan. Kalau
`SERVER_HOST` diisi alamat publik, server tetap jalan tapi memberi
peringatan di log.

Simulator armada (terminal lain) — membuat kendaraan yang belum ada, lalu
mengirim satu posisi per interval:

```bash
go run ./cmd/simulator -api http://127.0.0.1:8091 \
  -email fleetadm@test.local -password Groundwork123 -count 5 -interval 2s
```

Tiap laporan posisi membawa `heading`: arah kompas dalam derajat (0 = utara,
90 = timur), sama dengan yang dipakai peta untuk memutar marker. Simulator
menghitungnya dari arah garis rute 10 m ke depan, jadi marker truck mengikuti
belokan jalan yang ia lalui, bukan tetap tegak lurus ke garis. Unit tanpa
rute bergerak bebas, tapi tetap menghadap arah geraknya.

#### Alur "Plan route" (Fleet)

1. Admin pilih unit → **Plan route**. Titik awal otomatis diisi dengan posisi
   terakhir unit tersebut.
2. Admin klik **+ Add Stop** untuk menambah baris tujuan, lalu ikon crosshair
   pada baris itu dan klik peta. Baris bisa dihapus dengan tombol **×**.
   Maksimal 12 titik tujuan. Permintaan dikirim ke
   `POST /api/vehicles/:id/route` saat **Build route** ditekan.
3. Backend memanggil OSRM dan menyimpan polyline-nya di
   `vehicle_routes.geom` (LineString, SRID 4326). Bila OSRM meny snap kedua
   ujung ke simpul yang sama — garis yang tidak bisa disimpan PostGIS — rute
   memakai garis lurus (`source: "direct"`), bukan gagal. Rute lama dibatalkan
   dan rute baru disimpan dalam satu transaksi, jadi satu unit selalu punya
   paling banyak satu rute aktif.
   `distance_m` dan `duration_s` dari balasan OSRM ikut disimpan di
   `vehicle_routes`, jadi jarak tempuh dan durasi rencana tersedia tanpa
   memanggil OSRM lagi.
4. Peta menggambar polyline dalam **warna unit tersebut**, kamera menyesui
   ke jalur baru, dan card kendaraan menampilkan *"Following route to [nama
   tujuan]"*. Rute aktif ikut dibawa oleh `GET /api/vehicles`
   (`active_route`).
5. Card kendaraan di tab Live menampilkan **jarak tempuh** (`Distance 60 km`),
   **ETA** (jam tiba, bukan durasi) dan progress bar kecil yang menunjukkan
   sejauh mana unit sudah menyusuri rute. Card unit yang dipilih (panel
   detail) menampilkan angka yang sama dengan lebih lengkap: persen selesai,
   sisa jarak, jam tiba, dan total jarak + lama tempuh rencana.
6. Simulator membaca `GET /api/routes` tiap tick: unit tanpa rute bergerak
   bebas, unit dengan rute **menyusuri garis** — posisinya selalu tepat di
   garis rute (0 m dari garis), mulai dari titik pertama (origin), melewati
   setiap titik dalam urutan, dan berhenti di titik terakhir lalu menutup
   rutenya. Flag `-pace` (bawaan 20 meter per tick) mengatur seberapa jauh
   unit berjalan tiap tick: makin kecil, makin dekat marker mengikuti
   belokan jalannya dan tidak memotong tikungan.


#### Multi-stop routing (VRP)

Satu rute boleh punya beberapa titik tujuan: admin menambah berhenti dengan
**+ Add Stop**, lalu server yang menentukan urutan kunjungan.

**Urutan kunjungan** dihitung di `internal/routing` (`PlanMulti`), dengan tiga
mesin dari yang terbaik ke yang paling memaafkan:

1. **Trip service OSRM** (`/trip/v1/driving/`) — menghitung urutan optimal dan
   mengembalikan satu polyline panjang yang melewati semua titik. Server publik
   `router.project-osrm.org` mendukungnya; urutan hasil pengiriman dibaca dari
   `waypoint_index` tiap waypoint.
2. **Route service OSRM** dengan titik yang sudah diurutkan sendiri — dipakai
   kalau server tidak punya trip service. Urutannya dihitung dengan *greedy
   nearest neighbour* (`OptimizeOrder`): dari posisi sekarang ke titik terdekat
   yang tersisa, lalu ulangi. Bukan tur terpendek yang mungkin (masalah
   NP-hard), tapi tur yang baik dengan biaya O(n²).
3. **Garis lurus** antar titik — dipakai kalau tidak ada engine yang menjawab,
   supaya fiturnya tetap jalan tanpa internet.

Urutan hasil disimpan di `vehicle_routes.stops` (JSONB, urutan kunjungan),
sehingga peta bisa menomori titik (1, 2, 3) dan card unit menampilkan urutan
kunjungan yang sudah ditentukan. Rute dengan satu tujuan saja menyimpan
`stops: []` — kolom `origin_*`/`dest_*` yang sudah cukup.

Status tiap stop **tidak** hidup di JSONB itu: sejak migrasi 028 tiap stop
adalah baris di tabel `route_stops` (`seq`, `status`, `arrived_at`,
`completed_at`, `completed_by`, `completion_note`, `completed_lat/lng`), jadi
tahu stop mana yang sudah dilayani — lihat
[Status Per-Stop](#status-per-stop-bukti-singgah). Rute satu tujuan tetap
satu baris: tujuannya sendiri.

Batas 12 titik per rute (`maxRouteStops`), karena trip service publik mulai
lambat di atas angka itu. Rute ditutup di titik **terakhir**: kedatangan di
tujuan hanya dihitung kalau unit sudah hampir selesai dengan garisnya, jadi
tujuan yang kebetulan dekat dengan titik awal tidak menutup rute lebih awal.

### 6. Frontend

```bash
cd frontend
npm install
npm run dev            # http://localhost:5173
```

Vite mem-proxy `/api` dan `/health` ke `VITE_BACKEND` (default
`127.0.0.1:8091`), termasuk upgrade WebSocket (`ws: true`). Badge di header
menunjukkan `API online` / `API checking` / `API offline`; saat offline
frontend mencoba lagi otomatis tiap 15 detik.

## Data Wilayah

Batas administratif berasal dari **HDX COD-AB Indonesia** (`cod-ab-idn`,
CC BY-IGO, sumber BPS). Arsipnya ±456 MB dan **tidak ikut repo** (`/data`
ada di `.gitignore`).

Yang **sudah ikut repo** adalah garis batas negara + 34 provinsi dalam versi
sederhanakan (migrasi 016, sekitar 100 KB SQL): layer wilayah dan drill-down
provinsi langsung hidup setelah migrasi, tanpa unduhan apa pun. Batas yang
lebih detail (kabupaten, kecamatan) dan bentuk yang persis tetap perlu
dataset penuh:

```bash
# Unduh sendiri dari HDX (456 MB, masuk folder data/), lalu impor per level
go run ./cmd/import-regions -url -levels 0
go run ./cmd/import-regions -url -levels 1
go run ./cmd/import-regions -url -levels 2
go run ./cmd/import-regions -url -levels 3

# Probe dulu (tanpa menulis) kalau file sudah ada di tangan
go run ./cmd/import-regions -file data/idn_admin_boundaries.geojson.zip -probe

# Hasil di mesin ini: 7.626 baris (L0=1, L1=34, L2=522, L3=7.069), 0 orphan
```

Importer menimpa baris yang sudah ada (upsert pada `(level, code)`), jadi
impor dataset penuh setelah migrasi 016 akan mengganti garis batas kasar
dengan yang asli.

## Endpoint API

| Method | Path | Keterangan |
| ------ | ---- | ---------- |
| `GET` | `/health` | `200` bila DB sehat, `503` bila tidak |
| `GET` | `/api/auth/status` | apakah workspace masih butuh akun pertama |
| `POST` | `/api/auth/login` | sign in → `token` + `refresh_token` |
| `POST` | `/api/auth/refresh` | tukar refresh token dengan access token baru |
| `POST` | `/api/auth/setup` | buat admin pertama (hanya kalau belum ada akun) |
| `GET` | `/api/entities` | daftar aset; `?zone=<id>` memfilter yang di dalam zona (`ST_Covers`) |
| `GET` `POST` | `/api/entities`, `/api/entities/:id` | baca satu / buat |
| `PUT` `DELETE` | `/api/entities/:id` | ubah penuh / hapus (`204`) |
| `GET` | `/api/regions` | batas administratif; wajib `?level=0..4`, opsional `parent`, `code`, `q` |
| `GET` | `/api/regions/count`, `/api/regions/:code` | jumlah / satu wilayah |
| `GET` `POST` | `/api/zones` | daftar (GeoJSON FeatureCollection) / buat (geometry wajib) |
| `GET` | `/api/zones/counts` | hitungan entitas/kendaraan per zona (ringan, untuk auto-refresh) |
| `GET` `PUT` `DELETE` | `/api/zones/:id` | baca / ubah / hapus |
| `POST` | `/api/zones/import` | impor GeoJSON FeatureCollection → banyak zona |
| `POST` | `/api/zones/from-region` | buat zona dari kode wilayah administratif |
| `GET` | `/api/islands` | gugus pulau yang bisa dijadikan zona |
| `POST` | `/api/zones/from-island` | buat zona dari gugus pulau |
| `GET` `POST` | `/api/vehicles` | daftar armada + posisi terakhir + rute aktif (`active_route`) + status idle (`idle_since`, `idle_duration_s`) + zona yang ditempati (`current_zone`) + odometer (`total_distance_km`) + jadwal servis (`service`) / daftar unit baru |
| `PUT` | `/api/vehicles/:id` | ubah unit (nama, tipe, driver, `service_interval_km` opsional) |
| `POST` | `/api/vehicles/:id/position` | lapor posisi (`lat`, `lng`, `speed_kmh`, `heading`, `status` opsional) → disiar ke WS, odometer bertambah |
| `GET` | `/api/vehicles/:id/history` | histori posisi unit: `?date=YYYY-MM-DD` atau `?from=&to=` |
| `GET` | `/api/vehicles/:id/services` | riwayat servis unit (terbaru dulu); driver hanya unitnya sendiri |
| `POST` | `/api/vehicles/:id/services` | catat servis `{note?, serviced_at?}` (admin) → jadwal servis mulai dari odometer sekarang |
| `GET` | `/api/vehicles/stream` | WebSocket: frame `snapshot` saat connect, lalu `position` |
| `GET` | `/api/idle-events` | riwayat sesi idle (terbaru dulu); `?vehicle_id=&from=&to=&limit=` (bawaan 100, maks. 500) |
| `GET` | `/api/routes` | semua rute aktif |
| `GET` | `/api/vehicles/:id/routes` | riwayat rute satu kendaraan |
| `GET` | `/api/vehicles/:id/route` | rute aktif satu kendaraan (dengan geometri; `404` bila tidak ada) |
| `POST` | `/api/vehicles/:id/route` | rencanakan rute `{origin:{lat,lng,name}, destinations:[{…}]}` (multi-stop) |
| `DELETE` | `/api/vehicles/:id/route` | batalkan rute aktif |
| `POST` | `/api/vehicles/:id/route/start` | mulai perjalanan (driver unitnya, atau admin) |
| `POST` | `/api/vehicles/:id/route/stops/:stop_id/complete` | selesaikan stop yang sedang dilayani (`{note}` opsional — catatannya adalah bukti) |
| `POST` | `/api/vehicles/:id/route/stops/:stop_id/skip` | lewati stop yang sedang dilayani (`{note}` opsional) |
| `GET` | `/api/tasks` | papan tugas (work order); driver hanya melihat tugasnya sendiri |
| `GET` | `/api/tasks/:id` | satu tugas (`403` kalau tugas milik orang lain dan peminta driver) |
| `POST` | `/api/tasks` | buat tugas baru (admin) |
| `PUT` | `/api/tasks/:id` | ubah tugas penuh (admin) |
| `DELETE` | `/api/tasks/:id` | hapus tugas (`204`, admin) |
| `PUT` | `/api/tasks/:id/status` | pindahkan status tugas (admin / staff / driver) |
| `GET` | `/api/reports` | laporan operasional satu periode (`from`, `to`, `vehicle_id` opsional) |
| `GET` | `/api/reports/export.csv` | laporan yang sama sebagai file CSV (unduhan) |
| `GET` | `/api/audit-logs` | jejak aktivitas (admin); `?limit=&offset=&action=&entity_type=&q=` |
| `GET` | `/api/push/vapid-public-key` | kunci publik VAPID + `enabled` (false kalau server belum punya pasangan kunci) |
| `POST` | `/api/push/subscribe` | simpan subscription Web Push browser ini (`{endpoint, keys:{p256dh, auth}}`) |
| `DELETE` | `/api/push/subscribe` | hapus subscription milik akun yang login (`{endpoint}`) |
| `POST` | `/api/push/test` | kirim push percobaan ke subscription milik pemanggil; balas `{sent, enabled}` |

Bentuk entity:

```json
{"id": 1, "name": "Monas", "type": "monument", "status": "active",
 "latitude": -6.1754, "longitude": 106.8272,
 "created_at": "…", "updated_at": "…"}
```

Bentuk unit (yang di `GET /api/vehicles`):

```json
{"id": 1, "code": "TRK-001", "name": "Truck Simulator 1", "kind": "truck",
 "status": "moving", "driver_id": null,
 "lat": -6.6942, "lng": 107.2701, "speed_kmh": 33.3,
 "heading": 353.2, "route_color": "#d0171e",
 "last_seen_at": "…", "active_route": {…},
 "idle_since": null, "idle_duration_s": null,
 "current_zone": null,
 "total_distance_km": 1574.42,
 "service": {"interval_km": 5000, "last_service_at": null,
             "last_service_km": 0, "next_due_km": 5000,
             "remaining_km": 3425.58, "status": "ok"}}
```

`total_distance_km` = odometer unit (akumulasi sejak unit dibuat), dan
`service` = jadwal servisnya:

```json
{"interval_km": 5000,
 "last_service_at": "2026-10-22T09:12:00+07:00",
 "last_service_km": 12500.5,
 "next_due_km": 17500.5,
 "remaining_km": 3425.58,
 "status": "ok"}
```

`status` adalah `ok` (masih jauh dari jadwal), `due_soon` (sisa jarak sudah
masuk ambang peringatan) atau `overdue` (sudah lewat jadwal). Lihat
[Pengingat Servis](#pengingat-servis) untuk aturannya.

`idle_since` = waktu mulai sesi idle yang sedang berjalan (`null` kalau unit
tidak idle), `idle_duration_s` = detiknya (dihitung server, `null` kalau tidak
idle), dan `current_zone` = zona yang sedang ditempati unit:

```json
{"zone_id": 11, "zone_name": "Gudang Utara",
 "since": "2026-10-22T09:12:00+07:00", "dwell_s": 1500}
```

`current_zone` = `null` kalau unit berada di luar semua zona atau tidak punya
ENTER yang belum ditutup EXIT-nya. Lihat
[Status Unit](#status-unit-idle--moving--offline) untuk aturannya.

Bentuk tugas (yang di `GET /api/tasks`):

```json
{"id": 7, "title": "Antar 3 palet oli ke Monas",
 "description": "Bawa barang dari gudang, konfirmasi barang diterima.",
 "status": "pending",
 "assigned_user_id": 12, "place_id": 47,
 "due_date": "2026-10-25", "created_at": "…",
 "assigned_name": "Budi Santoso", "place_name": "Monas"}
```

`assigned_name` dan `place_name` ikut perjalanan supaya kartu tugas tidak
perlu request kedua. `due_date` adalah tanggal saja (`YYYY-MM-DD`), `null`
kalau tugasnya tidak punya tenggat.

`heading` adalah arah kompas unit dalam derajat (0 = utara, 90 = timur),
`null` kalau feed tidak melaporkannya. `route_color` adalah warna jalur
rute; sekarang hanya satu untuk semua unit — merah primer tema (`#d0171e`),
server yang menetapkannya dan form armada tidak menawarkannya lagi.

Bentuk rute (yang di `GET /api/routes` dan di `active_route`):
```json
{"id": 32, "vehicle_id": 1,
 "origin": {"lat": -6.929, "lng": 107.636, "name": "Gudang"},
 "destination": {"lat": -6.95, "lng": 107.61, "name": "Cisitu"},
 "stops": [{"id": 90, "seq": 0, "lat": -6.85, "lng": 107.6, "name": "Toko Melchior",
            "status": "arrived", "arrived_at": "2026-10-22T17:56:52+07:00",
            "completed_at": null, "completion_note": ""}],
 "stops_total": 3, "stops_done": 1,
 "geometry": {"type": "LineString", "coordinates": [[107.636, -6.929], "…"]},
 "distance_m": 14187, "duration_s": 1016, "source": "osrm",
 "status": "active", "progress": 0.05, "remaining_m": 13478,
 "created_at": "…", "ended_at": null}
```

`stops` adalah **langkah-langkah perjalanan**, bukan cuma daftar titik: tiap
stop membawa statusnya sendiri (`pending` / `arrived` / `completed` /
`skipped`) — lihat [Status Per-Stop](#status-per-stop-bukti-singgah).
`stops_total` = jumlah stop, `stops_done` = yang sudah selesai (dikonfirmasi
atau dilewati).

Bentuk sesi idle (yang di `GET /api/idle-events`):

```json
{"id": 7, "vehicle_id": 1, "vehicle_name": "Truck Simulator 1",
 "zone_id": 11, "zone_name": "Gudang Utara",
 "started_at": "…", "ended_at": null, "duration_s": 1500}
```

`ended_at` = `null` selama sesi masih berjalan; `duration_s` dihitung
server, jadi sesi yang masih berjalan dihitung sampai sekarang.

`source` = `osrm` (jalur jalan raya) atau `direct` (garis lurus). GeoJSON
berisi koordinat `[lng, lat]`. `progress` dihitung PostGIS dari posisi
terakhir unit (`ST_LineLocatePoint`).

`distance_m` (meter) dan `duration_s` (detik) diambil dari balasan OSRM
(`routes[0].distance` dan `routes[0].duration`), lalu disimpan di
`vehicle_routes.distance_m` dan `vehicle_routes.duration_s`. Rute garis lurus
menghitung sendiri dari jarak great-circle dengan kecepatan tetap (~50 km/h).
`remaining_m` adalah jarak yang belum ditempuh; ETA di UI = waktu sekarang +
sisa waktu tempuh, memakai kecepatan unit saat ini dan jatuh ke durasi
rencana bila unit sedang diam.

Validasi: `name`/`type` wajib (maks 255/100), `status` `active`/`inactive`,
latitude −90…90, longitude −180…180. `PUT` adalah full replace.
Error selalu `{"error":{"code":"…","message":"…"}}`, dengan `fields` untuk
`validation_failed`.

```bash
curl -X POST http://127.0.0.1:8091/api/entities \
  -H 'Content-Type: application/json' \
  -d '{"name":"Monas","type":"monument","status":"active","latitude":-6.1754,"longitude":106.8272}'

curl 'http://127.0.0.1:8091/api/entities?zone=11'
```

## Perilaku Zona

- Kategori: `operational`, `restricted`, `planned`, `other`; warna bebas
  (`#rrggbb`), dipakai di peta dan kartu zona.
- Geometry di respons adalah **salinan tampilan**: polygon di atas 2.000 titik
  disederhanakan (`ST_SimplifyPreserveTopology`, toleransi `0.0005°`) supaya
  respons tidak membengkak. Data di database tetap utuh.
- Salinan tampilan, luas, keliling, jumlah titik, dan hitungan entitas/
  kendaraan **dihitung sekali** saat bentuk zona disimpan (migrasi 013,
  tabel `zone_metrics`), bukan dihitung ulang tiap permintaan. Ini yang
  membuat `GET /api/zones/:id` untuk polygon 227 ribu titik turun dari
  ±7 detik menjadi ±60 milidetik.
- Hitungan entitas/kendaraan dijaga trigger di database dan dikirim ke
  browser lewat frame `zones`, jadi kartu zona tidak perlu dimuat ulang
  setelah aset ditambah atau unit melewati batas. `GET /api/zones/counts`
  adalah endpoint ringan untuk hal yang sama.
- Editor vertex hanya aktif sampai `MAX_EDITABLE_VERTICES = 2.000`; di atas itu
  polygon tidak bisa diedit (menghindari freeze browser).
- `PUT` **tanpa** `geometry` hanya mengubah atribut — bentuk tersimpan tidak
  ditulis ulang. Frontend membandingkan geometry saat load vs saat simpan dan
  hanya mengirim geometry bila benar-benar berubah.

## Status Unit (idle / moving / offline)

Status unit **adalah data**: kolom `vehicles.status` hanya menerima tiga nilai
(`idle`, `moving`, `offline` — dibatasi CHECK constraint). Reporter posisi
yang mengisinya, dan kalau `status` tidak dikirim, API memakai `moving`.

**Pengecualiannya ada dua, dan keduanya milik server:**

- **Offline** — unit yang berhenti melapor lebih dari `SERVER_OFFLINE_AFTER`
  (bawaan 60 detik) ditandai offline oleh *sweep*
  (`internal/handler/vehicles.go`, `MarkStaleOffline`) yang berjalan tiap 10
  detik. Unit yang melapor lagi **bangkit sendiri**, karena setiap laporan
  posisi menulis status dan waktu yang baru — tidak perlu ada tindakan lain.
- **Idle** — unit yang **masih melapor** tetapi tidak berpindah tempat
  ditandai idle oleh server sendiri. Deteksi ini memakai *jangkar*
  (anchor): titik tempat unit mulai tidak berpindah, dan jam yang berjalan
  sejak itu.

Unit yang **belum pernah melapor** tidak ditandai: tidak ada yang bisa
"terlambat" kalau belum pernah bicara.

### Idle vs offline

Dua-duanyastatus "diam", tapi bukan hal yang sama:

| | Idle | Offline |
| --- | --- | --- |
| Unit masih melapor? | **Ya** | Tidak |
| Artinya | Unit hidup, tapi tidak bergerak | Unit berhenti bicara |
| Ambang | `SERVER_IDLE_AFTER` (bawaan 10 menit) | `SERVER_OFFLINE_AFTER` (bawaan 60 detik) |
| Siapa yang menentukan | Server (deteksi diam) | Server (deteksi sunyi) |

**Offline selalu menang.** Unit yang diam tapi berhenti melapor jadi
`offline`, bukan `idle` — dan memang tidak bisa jadi idle, karena ambang idle
(10 menit) selalu lebih besar dari ambang offline (60 detik), jadi idle hanya
bisa terjadi pada unit yang laporannya masih segar. Sweep idle sendiri juga
melewati unit yang sudah waktunya offline; itu urusan sweep offline.

### Cara server mengukur "diam"

Dua angka menentukan, keduanya di `.env`:

- `SERVER_IDLE_RADIUS_M` (bawaan **25** meter) — jarak maksimum dari jangkar
  yang masih dihitung "tidak berpindah". Diukur di bola (`ST_Distance`
  geography), bukan di peta datar.
- `SERVER_IDLE_MAX_SPEED_KMH` (bawaan **2** km/jam) — laporan dengan kecepatan
  di atas ini dianggap bergerak, **walaupun titik GPS-nya nyaris tidak
  berubah**.

Aturannya:

1. Laporan posisi pertama sebuah unit menjadi **jangkar**-nya: titik awal
   unit mulai tidak berpindah, dan jam (`stationary_since`) mulai jalan.
2. Laporan berikutnya yang masih di dalam radius jangkar (dan kecepatannya di
   bawah ambang) **tidak mengubah** jangkar dan **tidak mereset** jam. Unit
   itu diam, dan jamnya terus berjalan.
3. Laporan yang keluar dari radius jangkar — atau yang lebih cepat dari
   ambang — **memindahkan** jangkar ke posisi baru dan **mereset** jam. Kalau
   ada sesi idle yang masih berjalan, sesi itu ditutup (`ended_at` diisi) dan
   status unit kembali mengikuti laporan (`moving`).

### Sesi idle

Sesi idle = satu periode diam yang cukup lama. Semuanya disimpan di tabel
`idle_events` (migrasi 027):

- Satu unit punya **paling banyak satu** sesi yang masih berjalan
  (`ended_at IS NULL`) — dijaga oleh logika aplikasi *dan* oleh unique index
  parsial di database.
- Sweep idle (`MarkStationaryIdle`, tiap 30 detik) membuka sesi untuk unit
  yang sudah diam melewati `SERVER_IDLE_AFTER`, dan mencatat zona yang memuat
  titik jangkarnya (`zone_id`, `null` bila di luar semua zona). Sweep-nya
  idempoten: unit yang sudah punya sesi terbuka tidak dibuatkan sesi kedua.
- Session ditutup oleh laporan posisi berikutnya (unit bergerak lagi) — tidak
  perlu sweep khusus untuk itu.

Historisnya dibaca lewat `GET /api/idle-events`.

### Dwell time di zona

Dwell = berapa lama unit berada di sebuah zona. Dihitung dari data geofence
yang sudah ada, tanpa tabel baru: dwell sebuah unit di sebuah zona = waktu
sejak **ENTER terakhir** di zona itu yang belum punya EXIT pasangan. Untuk
unit yang sedang berada di dalam zona, dwell berjalan = `now() -` waktu ENTER
itu. Unit di luar semua zona (atau yang tidak punya ENTER terbuka) tidak punya
dwell.

### Semua tampilan ikut berubah

Karena statusnya disimpan di database: daftar armada (`GET /api/vehicles`),
frame WebSocket, hitungan Overview (`GET /api/overview`), dan Driver View.
Browser yang sedang terbuka tahu segera karena sweep mengirim frame
`vehicle_updated` untuk unit yang berubah.

Yang **tidak** ada: tebakan offline dari UI. Kalau server mati, badge "API
offline" yang muncul, bukan status unit yang berubah.

> Batas jujurnya: deteksi ini menilai diam dari laporan posisi simulator.
> GPS sungguhan yang loncat-loncat bisa perlu radius lebih besar, dan angka
> bawaan (25 m / 2 km/jam) dipilih untuk data simulator project ini.

## Audit Log (Jejak Aktivitas)

Siapa mengubah apa, dan kapan. Setiap penulisan yang berhasil meninggalkan
satu baris: akun yang meminta, apa yang dia lakukan, objeknya, dan kapan.
Baris itu ditulis sendiri oleh middleware (`internal/audit`), jadi tidak ada
handler yang bisa "lupa" mencatat — dan handler baru langsung tercatat
tanpa disentuh sama sekali.

- **Pencatatan otomatis** — middleware berjalan setelah middleware auth,
  jadi ia tahu akun mana yang membuat permintaan. Permintaan yang **gagal**
  (4xx/5xx) tidak dicatat: tidak ada yang berubah, jadi tidak ada yang
  perlu dicatat. Yang dicatat adalah setiap `POST`/`PUT`/`DELETE` yang
  berhasil.
- **Apa saja yang dicatat** — entitas, zona, kendaraan, akun, dan rute.
  Satu hal dikecualikan: laporan posisi kendaraan
  (`POST /api/vehicles/:id/position`) adalah telemetri, bukan perubahan
  sebuah record, jadi tidak masuk jejak — kalau ikut masuk, jejaknya akan
  tenggelam oleh laporan tiap dua detik.
- **Kalimatnya** — backend menyusun kalimat yang dibaca manusia, contoh:
  `created zone "Gudang Utama"`, `deleted vehicle "Truk 01"`,
  `planned a route for vehicle "Truk 01"`. Nama objek diambil dari jawaban
  handler; untuk penghapusan, nama diambil **sebelum** baris dihapus
  (kalau tidak, namanya sudah hilang).
- **Akun yang dihapus** — `user_id` menunjuk akun dengan
  `ON DELETE SET NULL`, jadi jejaknya tetap ada setelah akunnya dihapus;
  identitasnya ikut tersalin di kolom `user_email`.
- **Hanya admin** — tab **Activity Log** di sidebar dan
  `GET /api/audit-logs` hanya untuk role `admin` (`403` untuk yang lain).

Bentuk barisnya:

```json
{"id": 12, "user_id": 5, "user_email": "admin@gmail.com",
 "action": "create", "entity_type": "zone", "entity_id": 31,
 "details": "created zone \"Gudang Utama\"",
 "created_at": "2026-10-22T06:08:50+07:00"}
```

### Cara mengetesnya

1. Masuk sebagai admin, buka tab **Activity Log** di sidebar.
2. Ubah sesuatu: tambah/hapus zona, kendaraan, atau akun; rencanakan rute.
   Barisnya langsung muncul di urutan atas (tab memuat ulang sendiri setiap
   kali dibuka, dan tombol reload ada di toolbar).
3. Cek lewat API:

   ```bash
   curl "http://127.0.0.1:8091/api/audit-logs?limit=10" \
     -H "Authorization: Bearer $TOKEN"
   ```

   Filter yang tersedia: `?action=create`, `?entity_type=zone`,
   `?q=...` (mencari di kalimat maupun di nama akun), plus
   `?limit=` dan `?offset=` untuk halaman berikutnya.

## Driver View (Layar Driver)

Role `driver` tidak pernah melihat dashboard yang penuh panel. Begitu login,
ia langsung berada di `/driver`: layar yang dibuat untuk satu tangan di atas
motor, layar kecil.

- **Peta layar penuh** — peta milik driver sendiri (`DriverMap`), bukan peta
  dashboard: hanya unit miliknya dan rutenya. Kamera punya dua aturan
  yang tidak saling bertabrakan: saat rute baru muncul, kamera menyesuaikan
  seluruh garis agar tujuan terlihat; selama perjalanan, kamera hanya
  "mendorong" unit kembali ke layar kalau unit mau keluar dari layar
  (menyeret peta mematikan pengikutan, karena itu berarti driver mengambil
  alih).
- **Kartu di bawah** — sheet di bawah layar: identitas unit (nama, plat,
  tipe, kecepatan, kapan posisi terakhir), perjalanan yang sedang berjalan
  (tujuan, sisa jarak, total jarak, lama jalan, ETA, progres), dan daftar
  perjalanan hari ini. Sheet bisa ditutup (handle di atasnya) supaya peta
  mendapat seluruh layar; saat ditutup, cardnya jadi satu baris ringkas
  dengan tombol kecil.
- **Tombol "Mulai Perjalanan"** — tombol besar (62 px, menempel di bawah
  kartu) yang memanggil `POST /api/vehicles/:id/route/start`. Tombol ini
  benar-benar memicu gerakan, bukan hanya mengubah tampilan:
  - migrasi 018 menambah `vehicle_routes.started_at` — saat driver menekan
    tombol, waktu itu dicatat (`COALESCE`, jadi tombol kedua tidak mengubah
    waktu mulai);
  - `POST /api/vehicles/:id/route/start` hanya boleh dipanggil driver unit
    itu (atau admin); driver lain mendapat `403`, unit tanpa rute aktif
    mendapat `409`;
  - simulator (yang berperan sebagai GPS unit) **menunggu** sinyal itu:
    rute milik unit yang punya driver belum dijalankan sampai driver menekan
    tombol. Unit tanpa driver tetap langsung berjalan seperti biasa, jadi
    alur demo lama tidak berubah.

### Cara mengetestnya

1. **Buat akun driver** (admin → tab **Users** → *Add account*, user type
   `driver`), lalu **tugaskan unit** ke akun itu: tab **Fleet** → unit →
   *Edit* → kolom *Driver*.
2. **Rencanakan rute** untuk unit tersebut: tab **Fleet** → *Plan route* →
   pilih titik tujuan → *Build route*.
3. **Jalankan simulator** supaya unit bergerak (terminal lain):

   ```bash
   go run ./cmd/simulator -api http://127.0.0.1:8091 \
     -email fleetadm@test.local -password Groundwork123 -count 4 -interval 2s
   ```

   Di log simulator, unit itu menunggu: `TRK-001 ... 0km/h rute menunggu`.
4. **Login sebagai driver** (username/email + password akun driver). Browser
   langsung ke `http://localhost:5173/driver`: peta penuh, kartu trip di
   bawah, tombol **Start trip** besar.
5. **Tekan "Start trip"**. Kartu berubah jadi "Trip running since …", status
   unit jadi *Moving*, dan di log simulator unit mulai berjalan:
   `TRK-001 ... 27km/h rute#43`. Peta bergerak mengikuti garis rute.

> Akun driver uji yang sudah dibuat di workspace lokal ini:
> `budi.driver` / `Groundwork123` (email `budi.driver@test.local`), dengan
> unit `TRK-001` yang ditugaskan ke sana. Hapus dari tab **Users** kalau
> sudah tidak dipakai.

## Status Per-Stop (Bukti Singgah)

Dulu titik singgah di rute multi-stop cuma daftar: tidak ada yang tahu stop
mana yang sudah dilayani, mana yang belum, dan tidak ada bukti apa pun saat
driver mengklaim sudah sampai. Sekarang tiap titik singgah adalah **langkah
perjalanan** dengan status sendiri, dan penyelesaikannya tercatat.

Statusnya **data**, bukan tebakan UI: kolomnya ada di tabel `route_stops`
(migrasi 028), jadi dashboard, Driver View, dan peta membaca hal yang sama.

### Empat status

| Status | Siapa yang menetapkannya | Arti |
| ------ | ------------------------- | ---- |
| `pending` | server, saat rute dibuat | Unit belum sampai di stop ini |
| `arrived` | **server**, dari laporan posisi | Server melihat unit di dalam radius 150 m dari stop. Stop **belum** selesai — driver masih harus mengonfirmasi |
| `completed` | **driver** (atau admin) | Driver mengonfirmasi stop selesai; catatan, nama penyelesai, waktu server, dan posisi kendaraan ikut tersimpan |
| `skipped` | **driver** (atau admin) | Stop dilewati tanpa dilayani; trip langsung lanjut ke stop berikutnya |

Alurnya **berurutan**: stop yang sedang dilayani selalu stop pertama yang
belum selesai (`pending` atau `arrived`, urutan `seq` terkecil). Stop
berikutnya tidak pernah dideteksi maupun diselesaikan sebelum stop
sebelumnya `completed`/`skipped` — kendaraan bisa saja lewat dekat stop
urutan akhir di awal perjalanan, dan itu bukan kunjungan.

### Isi bukti singgah

Saat driver menekan **Selesaikan stop ini**, server menyimpan:

- **catatan** driver (maks. 500 karakter, opsional) — mis. "Paket diterima satpam";
- **nama penyelesai** (`completed_by` — akun yang menekan tombol);
- **waktu server** (`completed_at` — jam database, bukan jam telepon);
- **posisi kendaraan** saat stop diselesaikan (`completed_lat` / `completed_lng`).

> Batas jujurnya: bukti singgah di project ini adalah **catatan + waktu +
> posisi yang dicatat server**. Foto bukti belum termasuk, karena project ini
> belum punya penyimpanan file — dan itu disengaja untuk tahap ini.

### Yang otomatis, dan yang manual

- **Kedatangan** (`arrived`) **deteksi server**, dari laporan posisi
  kendaraan: jarak ke titik stop ≤ 150 m (`arrivalRadiusM`, radius yang sama
  dengan tiba rute). Kedatangan di **stop terakhir** tidak mengirim push
  singgah: tiba di stop terakhir adalah tiba di tujuan, dan rute sudah
  mengirim push "Unit tiba" — jadi tidak ada push ganda.
- **Penyelesaian** (`completed`) **manual**, dari Driver View: tombol
  "Selesaikan stop ini" + kolom catatan kecil. Butuh server (maka tombolnya
  mati saat offline), dan hanya berlaku pada stop yang sedang dilayani.
- **Melewati** (`skipped`) juga manual, tombol "Lewati".
- **Stop terakhir** ditutup otomatis bersama rute: `completed` dengan
  `completed_at` = waktu akhir rute dan catatan "Otomatis: rute selesai".
- Menyelesaikan stop **bukan** akhir rute. Rute tetap ditutup oleh
  `completeRouteIfArrived`, seperti sebelumnya.

### Di mana status ini kelihatan

- **Driver View** — daftar stop di kartu trip: nomor, nama, chip status
  (Menuju / Sampai · jam / Selesai · jam / Dilewati), dan catatan
  penyelesaian. Frame WebSocket `route_stop` memperbarui tampilan tanpa
  muat ulang, jadi status yang dilihat server (atau perangkat lain) langsung
  terlihat.
- **Card armada** (tab Fleet) — progres stop dalam teks, mis. "Stop 2 dari 5 ·
  1 selesai".
- **Peta** — marker stop yang sudah `completed`/`skipped` tampil redup, dengan
  tanda centang pada labelnya. Cara menggambar garis rutenya tidak berubah.

### Endpoint-nya

| Method | Path | Keterangan |
| ------ | ---- | ---------- |
| `POST` | `/api/vehicles/:id/route/stops/:stop_id/complete` | selesaikan stop yang sedang dilayani; body `{"note":"…"}` opsional |
| `POST` | `/api/vehicles/:id/route/stops/:stop_id/skip` | lewati stop yang sedang dilayani; body `{"note":"…"}` opsional |

Hak akses: **admin** boleh untuk kendaraan mana pun, **driver** hanya untuk
unitnya sendiri (dan hanya pada rute yang sudah dimulai). **Staff** dan
**viewer** hanya membaca. Stop yang bukan stop aktif saat ini, atau yang
statusnya tidak memenuhi syarat, dijawab `409` dengan
`{"error":{"code":"conflict",…}}`; stop yang tidak ada dijawab `404`; driver
dari unit lain dijawab `403`.

### Cara mengetestnya

1. **Buat rute 2–3 stop** untuk unit yang punya driver: tab **Fleet** → unit →
   *Plan route* → *+ Add Stop* → pilih titik di peta → *Build route*. Pilih
   titik-titik yang **berdekatan** (satu–dua kilometer), supaya perjalanannya
   tidak lama.
2. **Tekan "Start trip"** di Driver View (login sebagai driver unit tersebut).
3. **Jalankan simulator dengan pace kecil**, supaya kedatangan terbaca
   satu-satu:

   ```bash
   go run ./cmd/simulator -api http://127.0.0.1:8080 \
     -email fleetadm@test.local -password Groundwork123 -count 1 -interval 2s -pace 10
   ```

   `-pace` adalah meter per tick: makin kecil, unit makin dekat mengikuti
   belokan jalannya.
4. **Stop pertama harus berubah jadi "Sampai"** dengan sendirinya (deteksi
   server, bukan tombol), dan push **"Unit singgah"** masuk ke operator.
5. **Selesaikan stop itu dari Driver View**: isi catatan (mis. "Paket
   diterima satpam") lalu tekan **Selesaikan stop ini**. Chipnya berubah jadi
   "Selesai · jam", catatan muncul, dan **stop kedua langsung jadi sasaran**
   (tombolnya aktif, chipnya "Menuju").
6. **Biarkan simulator tiba di tujuan akhir**: rute tertutup sendiri, dan
   **stop terakhirnya** jadi `completed` dengan catatan "Otomatis: rute
   selesai" — tanpa push singgah ganda di stop terakhir.

> Kalau arrival tidak terbaca padahal unitnya sudah lewat, jaraknya mungkin
> lebih dari 150 m: OSRM mem-*snap* titik yang dipilih ke jalan terdekat,
> sedangkan kedeteksian memakai koordinat titik yang asli. Pilih titik yang
> persis di jalan untuk tes yang mulus.

## Task Management (Work Orders)

Dulu kendaraan jalan tanpa tugas yang jelas: ada rute, ada unit, tapi tidak
ada yang tahu **kenapa** unit itu pergi. Sekarang tiap perjalanan lapangan
berawal dari satu tugas: apa yang harus dikerjakan, siapa yang mengerjakannya,
ke mana, dan sebelum kapan.

- **Papan tugas** (`TaskBoard.tsx`) — tab **Tasks** di sidebar. Tiga kolom:
  *Pending*, *In Progress*, *Done*. Tiap tugas adalah kartu: judul, tujuan
  (place), penerima tugas, dan tenggatnya. Peta disembunyikan di halaman ini
  supaya papan bisa memakai lebar penuh.
- **Form Create Task** (admin) — judul, deskripsi, dropdown **Assignee**
  (akun `driver` atau `staff`), dropdown **Place** (dari tabel `entities`),
  tenggat (kalender), dan status awal. Place memakai tabel `entities` karena
  di aplikasi ini *places* = lokasi tujuan pekerjaan lapangan.
- **Role baru `staff`** — orang lapangan yang mengerjakan tugas, tapi tidak
  menyentuh armada, zona, atau akun. `staff` hanya boleh memindahkan status
  tugas. Sekarang ada empat peran: `admin` (menulis semuanya), `staff`
  (mengerjakan tugas), `viewer` (hanya membaca), `driver` (di jalan).
- **Driver** — login sebagai `driver` membuka `/driver`, dan di sana ada
  bagian **Your tasks**: hanya tugas miliknya sendiri (server yang membatasi,
  bukan frontend), dengan tombol **Mark done** di tiap tugas yang belum
  selesai. Tugas yang ditutup pindah ke kolom *Done*.
- **Endpoint status** — `PUT /api/tasks/:id/status` menerima role `admin`,
  `staff`, dan `driver`. Driver hanya boleh mengubah tugas yang
  `assigned_user_id`-nya dia; tugas orang lain dijawab `404` (driver tidak
  boleh tahu tugas milik siapa).
- **Jejak aktivitas** — tiap perubahan status tercatat di **Activity Log**
  (`updated task "Antar 3 palet oli ke Monas"`), karena `tasks` ikut dikenali
  middleware audit.

### Cara mengetestnya

1. **Buat akun** dulu: admin → tab **Users** → *Add account*, user type
   `driver` (atau `staff`), lalu buat juga akun `staff` kalau mau menguji
   kedua jenis penerima tugas.
2. **Buat tugas** (admin): tab **Tasks** → *New task* → isi judul, pilih
   **Assignee** = akun driver tadi, pilih **Place** = salah satu place yang
   ada (tab **Places**), isi tenggat → *Create task*. Kartu langsung muncul
   di kolom *Pending*.
3. **Pindah status dari sisi staff** (opsional): login sebagai akun `staff`
   → tab **Tasks** → kartu tugas itu → *Start* (jadi *In Progress*).
4. **Login sebagai driver**: `http://localhost:5173/driver` → bagian
   **Your tasks** → tekan **Mark done**. Toast mengonfirmasi, dan tugas
   pindah ke kolom *Done* kalau dilihat dari akun admin.
5. **Cek lewat API**:

   ```bash
   # daftar tugas (driver: hanya miliknya)
   curl 'http://127.0.0.1:8091/api/tasks' -H "Authorization: Bearer $TOKEN"

   # buat tugas (admin)
   curl -X POST http://127.0.0.1:8091/api/tasks \
     -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d '{"title":"Antar 3 palet oli ke Monas","description":"Bawa dari gudang",
          "status":"pending","assigned_user_id":12,"place_id":47,"due_date":"2026-10-25"}'

   # tutup tugas (driver yang pemilik tugas, atau staff)
   curl -X PUT http://127.0.0.1:8091/api/tasks/7/status \
     -H "Authorization: Bearer $TOKEN" -H "Content-Type: application/json" \
     -d '{"status":"completed"}'
   ```

## Laporan & Export (Laporan Operasional)

Peta menunjukkan **sekarang**: di mana unit berada. Laporan menunjukkan
**sebelumnya**: berapa yang sebenarnya dikerjakan dalam satu periode, per
unit, per zona, dan per tugas. Semuanya dihitung di database, jadi angkanya
sama dengan yang dibaca Overview — bukan perkiraan di browser.

- **Tab Reports** di sidebar (`ReportsPanel.tsx`). Preset periode (**Hari ini**
  / **7 hari** / **30 hari**), rentang bebas dengan kalender yang sudah ada,
  dan dropdown unit (driver tidak melihat dropdown itu: server sudah
  membatasianya ke unitnya sendiri).
- **Kartu ringkasan** — jarak tempuh (km), rute selesai, task selesai, dan
  jumlah lintasan masuk/keluar zona. Gaya kartunya sama dengan Overview.
- **Dua tabel** — satu per kendaraan (jarak, rute, lintasan zona) dan satu
  per zona (masuk, keluar, total, jumlah unit yang melintasi).
- **Export CSV** — tombol mengunduh file yang isinya tabel per kendaraan.
  File diambil dengan `fetch` + header `Authorization` yang sama seperti
  request lain (link polos tidak membawa token, jadi server akan menjawab
  `unauthorized`), lalu dijadikan `Blob` dan diunduh dengan nama dari header
  `Content-Disposition`.

### Aturan periodenya

| Parameter | Arti |
| --------- | ---- |
| `from`, `to` | `YYYY-MM-DD` atau `RFC3339`. Tanpa keduanya: 7 hari terakhir termasuk hari ini. |
| — | `from` tidak boleh setelah `to`; rentang maksimum 92 hari. |
| `vehicle_id` | Batasi laporan ke satu unit (opsional). |

Batas periode yang lebih dari 92 hari dijawab `400` dengan
`{"error":{"code":"validation_failed", ...}}`, format error yang sama dengan
seluruh API.

### Cara hitung angkanya

- **Jarak tempuh** memakai logika yang persis sama dengan `GET /api/overview`:
  jumlah langkah antar posisi (`lag()` per kendaraan), diukur di bola
  (`ST_Distance` geography), langkah dari posisi terakhir sebelum periode
  ikut dihitung, dan langkah dengan kecepatan mustahil (> 200 km/h) dibuang.
- **Rute** — `routes_total` = rute yang berjalan di periode itu (dibuat
  sebelumnya dan belum selesai, atau selesai di dalam periode),
  `routes_completed` = yang benar-benar selesai di dalam periode. Rute yang
  dibatalkan tidak dihitung sebagai selesai.
- **Lintasan zona** — event `ENTER`/`EXIT` di dalam periode, per unit dan per
  zona. `unique_vehicles` = berapa unit berbeda yang melintasi zona itu.
- **Task** — `tasks_total` = tugas yang dibuat di periode itu,
  `tasks_completed` = yang sekarang berstatus `completed`.
- Unit tanpa satu pun aktivitas tetap muncul dengan angka `0`; periode tanpa
  aktivitas apa pun adalah laporan kosong, bukan error.

### Peran dan akses

`admin`, `staff`, dan `viewer` membaca laporan seluruh armada. `driver`
hanya membaca laporan unitnya sendiri: server memaksa cakupannya, dan
permintaan `vehicle_id` milik orang lain dijawab `403`.

### Cara mengetestnya

1. **Jalankan simulator** beberapa menit (`go run ./cmd/simulator`) supaya
   ada posisi, rute, dan lintasan zona.
2. **Buka tab Reports**, pilih periode **Hari ini**, lalu cocokkan angkanya
   dengan peta dan card unit di tab **Fleet**.
3. **Klik Export CSV**, buka filenya di spreadsheet, dan cocokkan isinya
   dengan tabel per kendaraan di layar.
4. **Login sebagai driver**: tab Reports hanya menampilkan unitnya sendiri.

```bash
# laporan sebagai JSON
curl "http://127.0.0.1:8091/api/reports?from=2026-10-16&to=2026-10-22" \
  -H "Authorization: Bearer $TOKEN"

# laporan sebagai CSV
curl -o report.csv "http://127.0.0.1:8091/api/reports/export.csv?from=2026-10-16&to=2026-10-22" \
  -H "Authorization: Bearer $TOKEN"
```

## Pengingat Servis

Setiap unit punya **odometer** (jarak tempuh seumur kendaraan) dan **jadwal
servis** yang dihitung dari odometer itu. Operator diberi tahu saat unit
mendekati jadwal servisnya dan saat unit sudah melewatinya; mencatat servis
mengembalikan jadwal ke awal.

### Odometer: satu cara hitung saja

Odometer disimpan di `vehicles.total_distance_m` (meter) dan **diakumulasi
server**, satu laporan posisi sekaligus — bukan dihitung ulang dari seluruh
histori posisi setiap kali dibaca.

Langkah yang ditambahkan sama persis dengan cara hitung jarak di tab Overview
dan di laporan operasional (`GET /api/overview`, `internal/handler/reports.go`):

- jarak antara dua posisi berurutan diukur di bola bumi
  (`ST_Distance` pada `geography`), bukan di peta datar;
- **langkah yang butuh kecepatan mustahil (> 200 km/h) dibuang** — lompatan
  posisi (misal unit dipindah ke titik awal rute) bukan jarak yang ditempuh;
- langkah pertama unit (belum ada posisi sebelumnya) menambah `0`;
- penjaganya adalah **kecepatan langkah hasil hitungan**, bukan klaim
  `speed_kmh` dari pelapor: laporan yang bilang "5 km/h" setelah lompatan
  100 km tetap lompatan.

Semua ini terjadi dalam **statement yang sama** dengan penulisan posisi
(`reportPositionSQL` di `internal/handler/vehicles.go`), jadi odometer tidak
pernah tertinggal satu laporan. Tab Reports dan Overview tetap menghitung
jarak **per periode** seperti sebelumnya — odometer adalah total seumur unit.

> Penjaga yang sama berlaku untuk Overview dan laporan, jadi sebuah lompatan
> posisi setelah jeda panjang bisa ikut terhitung kalau kecepatan rata-ratanya
> masih di bawah 200 km/h — persis seperti yang terjadi pada angka jarak di
> laporan. Kalau ingin angka yang lebih ketat, yang harus diubah adalah
> **satu** guard itu, di satu tempat, untuk ketiga pembacaannya.

Migrasi 029 mengisi ulang odometer dari riwayat posisi yang sudah ada,
memakai logika langkah yang sama persis. Angka backfill itu hanya sebaik
histori posisi yang tersimpan — posisi yang tidak pernah dilaporkan tetap
tidak terhitung.

### Tiga status servis

Jadwal berikutnya dihitung dari odometer:

```
batas berikutnya (km) = odometer servis terakhir (km) + interval (km)
sisa jarak (km)        = batas berikutnya − odometer sekarang (km)
```

| Status | Arti | Syarat |
| ------ | ---- | ------ |
| `ok` | Aman | sisa jarak > ambang peringatan |
| `due_soon` | Mendekati jadwal | 0 < sisa jarak ≤ ambang peringatan |
| `overdue` | Terlewat | sisa jarak ≤ 0 |

Semua angka km dibulatkan dua desimal, sama seperti laporan.

### Dua variabel `.env`

| Variabel | Bawaan | Arti |
| -------- | ------ | ---- |
| `SERVICE_INTERVAL_DEFAULT_KM` | `5000` | Interval servis yang dipakai unit yang tidak punya interval sendiri |
| `SERVICE_WARN_KM` | `500` | Jarak (km) dari jadwal saat unit sudah dianggap `due_soon` |

Keduanya harus di atas 0; nilai yang tidak bisa dibaca atau bukan positif
diganti bawaan, sama seperti parsing config yang lain. Satu unit boleh punya
interval sendiri (`vehicles.service_interval_km`, diisi lewat
`PUT /api/vehicles/:id` dengan field `service_interval_km`, harus > 0 dan
≤ 100.000 km).

### Cara mencatat servis

Tombol **Catat servis** ada di tab Fleet untuk admin, di bawah bagian
**Servis** unit yang dipilih. Form-nya meminta:

- **Tanggal servis** — bawaan hari ini, tidak boleh di masa depan;
- **Catatan** — opsional, maksimal 500 karakter.

Menekan tombolnya menulis satu baris di `vehicle_services` (dengan odometer
unit **saat itu** sebagai potret) dan mengembalikan jadwal unit: titik awal
jadwal berikutnya jadi odometer sekarang, sehingga **sisa jaraknya satu
interval penuh** lagi dan statusnya kembali `ok`. Penanda peringatan ikut
dihapus, sehingga siklus servis berikutnya bisa mengirim peringatan lagi dari
awal.

### Peringatan dikirim sekali per tingkat

Sweep `CheckServiceReminders` (`internal/handler/services.go`) berjalan tiap
menit, seperti sweep offline dan sweep idle. Untuk setiap unit ia menghitung
status servisnya, lalu:

- `ok` → tidak ada apa-apa, penandanya tidak diubah;
- tingkat yang lebih tinggi dari penanda terakhir (`''` < `due_soon` <
  `overdue`) → **satu** Web Push ke operator (admin + staff), lalu penandanya
  diisi dengan tingkat itu.

Jadi unit yang tetap di satu tingkat tidak diberi tahu berulang; unit yang
menaik ke `overdue` diberi tahu sekali lagi. Penanda hanya direset oleh
pencatatan servis. Tanpa pasangan kunci VAPID, sweep tetap memperbarui
penanda dan menulis log — hanya tidak mengirim apa pun.

| Event | Judul | Body | Tag | Tujuan |
| ----- | ----- | ---- | --- | ------ |
| Unit mendekati jadwal | `Servis sebentar lagi` | `TRK-001 sisa 320 km menuju servis 5000 km` | `service-<vehicle_id>-due_soon` | `/` |
| Unit lewat jadwal | `Servis terlewat` | `TRK-001 sudah lewat 150 km dari jadwal servis` | `service-<vehicle_id>-overdue` | `/` |

### Di mana terlihat

- **Fleet** — kartu unit menampilkan odometer (`1.577,5 km`) dan chip status
  servis: kuning `Servis sebentar lagi · sisa 320 km`, merah
  `Telat servis · lewat 150 km`, atau teks redup `Servis aman` kalau unitnya
  masih aman. Unit yang dipilih menampilkan bagian **Servis**: servis
  terakhir, jadwal berikutnya, riwayat servis, dan tombol **Catat servis**.
- **Overview** — satu baris ringkas di bawah kartu armada:
  `Servis: 2 mendekati jadwal · 1 terlewat` (atau `Servis: semua aman`).
- **Driver View** — tidak berubah: driver tidak mencatat servis, dan
  scheduler tidak pernah menolak membacanya.

### Cara mengetestnya manual

1. **Pilih interval dan ambang kecil** di `.env` lokal, supaya jadwalnya
   cepat tercapai:

   ```bash
   SERVICE_INTERVAL_DEFAULT_KM=2
   SERVICE_WARN_KM=1
   ```

2. **Restart backend** (`go build -o bin/server.exe ./cmd/server && ./bin/server.exe`)
   dan **buat unit baru** di tab Fleet (odometer unit baru mulai dari 0).
3. **Jalankan simulator** beberapa menit
   (`go run ./cmd/simulator -api http://127.0.0.1:8091 -email fleetadm@test.local -password Groundwork123 -count 5 -interval 2s`)
   sampai odometer unit itu **melewati 1 km**.
   - Chip kuning **Servis sebentar lagi · sisa … km** muncul di Fleet.
   - **Satu** push `Servis sebentar lagi` masuk. Sweep berikutnya tidak
     mengirim apa pun (tingkatnya sama).
4. **Biarkan unit berjalan sampai odometernya lewat 2 km.**
   - Push `Servis terlewat` masuk **sekali** (menaik satu tingkat).
   - Sweep berikutnya diam.
5. **Catat servis dari Fleet** sebagai admin.
   - Status kembali **Servis aman**, jadwal berikutnya bergeser
     (`next_due_km` = odometer sekarang + interval), riwayat servis berisi
     satu baris baru, dan penanda peringatan dikosongkan.
6. **Kembalikan dua variabel itu ke bawaan** kalau sudah selesai menguji:

   ```bash
   SERVICE_INTERVAL_DEFAULT_KM=5000
   SERVICE_WARN_KM=500
   ```

   (atau hapus kedua barisnya dari `.env` — server memakai bawaannya kalau
   variabelnya tidak ada).

> **Batas jujur:** odometer ini adalah akumulasi dari laporan posisi
> simulator dengan penjaga kecepatan mustahil — **bukan** pembacaan odometer
> fisik kendaraan. Angka backfill untuk data lama hanya sebaik riwayat
> posisi yang tersimpan.

## Notifikasi Desktop (Web Push)

Lonceng di pojok kanan atas top bar adalah **pusat notifikasi** aplikasi.
Panelnya masih kosong — daftar riwayat notifikasi belum diisi — tapi dua
lapis notifikasi sudah jalan: notifikasi desktop (saat tab masih hidup) dan
**Web Push** (VAPID), yang tetap sampai walau tab aplikasinya sudah ditutup.

Yang diteruskan lewat push:

| Event | Judul | Body | Tag | Tujuan |
| ----- | ----- | ---- | --- | ------ |
| Unit memulai perjalanan | `Unit berangkat` | `TRK-001 berangkat menuju Gudang Cisitu` | `route-started-<route_id>` | `/` |
| Unit tiba di tujuan | `Unit tiba` | `TRK-001 tiba di Gudang Cisitu` | `route-completed-<route_id>` | `/` |
| Tugas ditugaskan | `Tugas baru` | `Antar 3 palet oli ke Monas — tenggat 2026-10-25` | `task-assigned-<task_id>` | `/driver` (driver) / `/` (lainnya) |

Penerima event rute (berangkat / tiba) adalah semua akun berperan **admin**
atau **staff**, kecuali akun yang memicu event itu sendiri (driver yang
menekan *Start trip* tidak diberi tahu soal keberangkatannya sendiri). Push
tugas hanya sampai ke **satu** orang: yang ditugasi.

- **Yang dihapus** — fitur peringatan kecepatan dibuang seluruhnya:
  `internal/handler/telematics.go`, endpoint `GET /api/telematics-events`,
  tabel `telematics_events` (migrasi 023), event `OVERSPEED` di live feed,
  dan kartu armada yang merah saat melewati batas. Notifikasi lintasan zona
  (desktop) juga dihapus: crossing zona masih tercatat di riwayat panel
  **Fleet**, tapi tidak lagi jadi notifikasi.
- **Izin** — lonceng di pojok kanan atas top bar (setelah login) asking izin
  lewat `Notification.requestPermission()`. Permintaan hanya jalan dari klik
  pengguna: Firefox menolaknya tanpa user gesture, dan Chrome hanya
  menampilkan prompt-nya kalau permintaannya datang dari aksi nyata. Belum
  diminta -> lonceng punya titik kecil di pojoknya; sudah diizinkan -> lonceng
  menyala memakai warna tema.
- **Service worker** — `frontend/public/sw-notify.js`, ditulis tangan tanpa
  dependensi baru. Worker ini **tidak** menyentuh jaringan dan tidak melakukan
  cache: satu-satunya tugasnya menaikkan notifikasi. Notifikasi dibentuk lewat
  `ServiceWorkerRegistration.showNotification()` (jalur utama, tetap tampil
  saat tab di background), dengan `new Notification()` sebagai cadangan bila
  registrasi worker gagal. Worker itu diimpor ke worker PWA
  (`vite-plugin-pwa`), jadi aplikasi hanya punya satu service worker.
- **Web Push** — protokol terbuka, tanpa layanan pihak ketiga (tanpa
  Firebase / Pusher / OneSignal). Server punya pasangan kunci **VAPID**
  sendiri; browser berlangganan dengan kunci publiknya, dan server
  meneruskan pesan ke alamat push service browser tersebut. Handle `push`
  di service worker sudah ada dari awal — bentuk payloadnya
  `{"title","body","tag","url"}` dan tidak diubah.

### Cara membuat kunci VAPID

Kunci VAPID dibuat sekali jalan. Programnya hanya mencetak ke stdout; ia
tidak membaca dan tidak menulis `.env` sendiri:

```bash
go run ./cmd/gen-vapid
```

```
# Copy these two lines into your .env file.
# The private key is a secret: never commit it, never paste it anywhere.
VAPID_PUBLIC_KEY=BLxxxx...
VAPID_PRIVATE_KEY=xxxx...
```

Salin kedua baris ke `.env` (file itu tidak masuk Git), lalu restart backend:

```
VAPID_PUBLIC_KEY=BLxxxx...
VAPID_PRIVATE_KEY=xxxx...
VAPID_SUBJECT=mailto:admin@localhost
```

`VAPID_SUBJECT` adalah identitas pengirim yang dilihat push service
(mailto: atau URL https); bawaannya `mailto:admin@localhost`.

**Kalau kunci dikosongkan, fiturnya mati dengan rapi**: server tetap jalan
normal, `GET /api/push/vapid-public-key` membalas `enabled: false`, dan
pengiriman push dilewati (cuma ditulis di log sebagai peringatan). Server
tidak pernah gagal start hanya karena VAPID belum dikonfigurasi.

> Kunci privat tidak pernah masuk repo, log, respons API, atau README. Pasangan
> yang pernah dipublikasikan tidak bisa "dibatalkan" — buat yang baru dan
> biarkan browser berlangganan ulang.

### Endpoint push

| Method | Path | Keterangan |
| ------ | ---- | ---------- |
| `GET` | `/api/push/vapid-public-key` | `{"public_key":"…","enabled":true}` |
| `POST` | `/api/push/subscribe` | body = `PushSubscription` JSON browser; upsert per `endpoint` |
| `DELETE` | `/api/push/subscribe` | body `{"endpoint":"…"}`; hanya milik akun yang login |
| `POST` | `/api/push/test` | kirim push percobaan ke subscription milik pemanggil |

Penyimpanan: tabel `push_subscriptions` (migrasi 026) — satu baris per
browser (`endpoint` unik), satu akun boleh punya banyak baris (banyak
browser/perangkat). Subscription yang dijawab `404`/`410` oleh push service
dihapus otomatis.

### Cara mengetesnya

1. Buat kunci: `go run ./cmd/gen-vapid`, salin ke `.env`, restart backend.
   Di log server akan muncul `web push is on (subject …)`.
2. Build produksi frontend (service worker hanya ada di build produksi, bukan
   di `npm run dev`):

   ```bash
   cd frontend
   npm run build
   npm run preview
   ```

3. Masuk ke aplikasi, klik lonceng di pojok kanan atas top bar, lalu
   izinkan notifikasi saat browser meminta. Panel lonceng lalu menunjukkan
   **Web Push: On** — artinya browser sudah berlangganan ke server.
4. Klik **Send a Web Push test**. Notifikasi sistem harus muncul, dengan
   judul "Notifikasi tes Groundwork".
5. **Tutup tab aplikasi** (browser jangan dimatikan). Dari sesi lain
   (misalnya simulator atau akun driver), mulai sebuah rute sampai selesai.
   Notifikasi "Unit berangkat" dan "Unit tiba" harus tetap muncul, dan
   mengkliknya membuka kembali aplikasi.
6. Uji juga notifikasi lokal (tanpa push): minimize jendela browser (bukan
   tutup), lalu tekan *Send a test notification* di panel lonceng.

> **Batas yang jujur**: selama backend lokal tidak berjalan, tidak ada push
> yang bisa dikirim — tidak ada yang memegang alamat push service-nya. Untuk
> portofolio lokal ini memang disengaja begitu: backend dan browser ada di
> mesin yang sama, dan tidak ada layanan pihak ketiga yang menyimpan
> subscription. Aplikasi tetap hidup dan notifikasi desktop (tanpa push)
> tetap jalan.

## Playback Rute (History Perjalanan)

Live feed menjawab "di mana unit sekarang". Playback menjawab "di mana unit
**pernah**": server handing out posisi yang dilaporkan unit, dan peta
memutar jalannya kembali.

- **Backend** — `GET /api/vehicles/:id/history`. Tanpa parameter berarti hari
  ini; `?date=YYYY-MM-DD` memilih satu hari (waktu server), `?from=&to=`
  memilih rentang (RFC3339 atau tanggal), `?limit=` mengatur jumlah titik
  (bawaan 2.000, ceiling 20.000). Satu hari laporan tiap 2 detik berisi
  43.000 titik, jadi server mengambil sampel: setiap n-titik diambil, titik
  pertama dan terakhir tetap ada. Bentuk jawabannya:

  ```json
  [{"lat": -6.175, "lng": 106.830, "speed": 36, "heading": null,
    "timestamp": "2026-10-21T15:06:57.722308+07:00"}]
  ```

  Semua role boleh membacanya; driver hanya boleh membaca unitnya sendiri
  (`403` untuk unit orang lain), aturan yang sama dengan daftar armada.

- **Antarmuka** — di tab **Fleet**, pilih satu unit lalu klik **Playback
  history**
  (ikon jam) di kartunya. Dialog-nya mengambang di pojok peta (kiri bawah di
  layar lebar, memanjang di ponsel): pemilih tanggal,
  tombol **Load history**, tombol **Play / Pause**, slider waktu, dan pilihan
  kecepatan (0,5x - 8x). 1x memutar seluruh jejak dalam 40 detik. Dialog ini
  sengaja tidak memakai portal atas halaman: peta harus tetap terlihat, jadi
  ghost marker bisa watched.

- **Peta** — jejak digambar sebagai **garis abu-abu transparan** (dengan halo
  putih tipis di bawahnya, supaya tetap terbaca di atas peta dan zona), dan
  satu *ghost marker* (ikon kendaraan, 50% transparan) berjalan di sepanjang
  garis. Kamera menyesuaikan ke seluruh jejak begitu dimuat, dan mode
  **Follow** kamera live mundur selama playback berjalan. Slider yang digeser
  memindahkan ghost ke titik yang dipilih; saat jejak habis, animasi berhenti
  dan tombol kembali jadi **Play**.

## Keamanan

- **JWT dengan refresh.** Login memberi access token (default 24 jam) dan
  refresh token (default 30 hari). Client menukar refresh token untuk access
  token baru, jadi sesi tidak berakhir sendiri. Peran: `admin` (baca + tulis),
  `staff` (baca + memindahkan status tugas), `viewer` (baca), `driver` (baca +
  armada miliknya + tugas miliknya).
- **Rate limit login.** Lima password salah dari alamat yang sama dalam lima
  menit mengunci alamat itu selama lima menit (`429`).
- **Loopback secara bawaan.** `SERVER_HOST` default `127.0.0.1`: API
  memakai HTTP polos tanpa sertifikat, jadi ia tidak boleh terbuka ke
  jaringan. Mengisi `SERVER_HOST` dengan alamat publik tetap bisa, tapi
  server memberi peringatan di log — taruh di belakang reverse proxy
  dengan TLS.
- **Hash bcrypt.** Password tidak pernah disimpan apa adanya. `JWT_SECRET`
  wajib diisi lewat `.env` dan tidak pernah masuk repo.
- **Akun pertama.** `POST /api/auth/setup` hanya berhasil selama workspace
  belum punya akun, jadi ia tidak bisa dipakai untuk mengambil alih
  workspace yang sudah berjalan.

## Pengujian

```bash
gofmt -l .                                        # harus tanpa output
go vet ./... && go build ./...
go test ./internal/...                            # unit test
go test -tags integration ./internal/handler/     # butuh DB + migrasi
```

```bash
cd frontend
npx tsc -b        # typecheck
npm run lint      # oxlint
npm run build     # tsc -b && vite build → dist/
```

CI (`.github/workflows/ci.yml`): gofmt/vet/build/unit test + `tsc -b` + build.

## Struktur Proyek

```text
cmd/
  server/                 # entry point Gin + graceful shutdown
  simulator/              # sumber data live tracking (armada palsu)
  import-regions/         # importer COD-AB → admin_regions (bisa unduh sendiri)
internal/
  config/ db/             # .env + DSN, pgxpool + health
  entity/ vehicle/ task/  # model + validasi + unit test (vehicle juga model rute & jadwal servis)
  handler/                # entities, zones, vehicles, routes, regions, islands, tasks, reports, services, auth
  middleware/             # kompresi gzip
  routing/                # klien OSRM + fallback direct
  ws/                     # hub WebSocket
migrations/               # 001-029 up/down + README
scripts/empty-state.sql   # kembali ke layar "No entities yet."
frontend/src/
  lib/                    # api.ts, types.ts, vehicleStream.ts, session.ts, playback.ts
  components/             # map layers, panel, form, dialog, TaskBoard.tsx, ReportsPanel.tsx
  App.tsx                 # state, tab, toast, sheet mobile
  tasks.css               # papan tugas (3 kolom + form)
  reports.css             # tab laporan (kartu, tabel, toolbar periode)
  theme-red.css           # tema visual terakhir (warna rute, pin, kontrol)
AGENT.md                  # aturan kerja proyek
README.md                 # file ini
```
