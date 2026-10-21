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
  sebagai sumber data; setiap unit punya arah tempuh dan warna rutenya.
- **Rute** — jalur jalan raya dari–ke per kendaraan (OSRM), digambar di peta
  dan ditutup otomatis saat unit tiba.

## Daftar Isi

- [Tech Stack](#tech-stack)
- [Basemap](#basemap)
- [Arsitektur](#arsitektur)
- [Mulai Cepat](#mulai-cepat)
- [Data Wilayah](#data-wilayah)
- [Endpoint API](#endpoint-api)
- [Perilaku Zona](#perilaku-zona)
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
| `internal/handler` | entities, zones, vehicles, routes, regions, islands, zone-from-region, health, auth |
| `internal/middleware` | kompresi gzip + `Vary`/`Content-Encoding` |
| `internal/routing` | klien OSRM (`OSRM_BASE_URL`, `off` = fallback `direct`) |
| `internal/ws` | hub fan-out untuk stream posisi kendaraan |

**Frontend** (`frontend/src/`)

`App.tsx` memegang state dan lima tab (Overview / Places / Zones / Fleet /
Users); `EntityMap` menyusun layer peta: `RegionLayer` (batas administratif),
`ZoneLayer` (polygon + editor Geoman), `VehicleLayer` (puck armada),
`RouteLayer` (jalur). Panel: `EntityList`/`EntityDetail`/`EntityForm`/
`ZonePanel`/`RegionZonePicker`/`VehiclePanel`. Kontrak API di `lib/types.ts`,
client di `lib/api.ts`, stream di `lib/vehicleStream.ts`. Tema visual terakhir
(`theme-red.css`) menentukan warna pin dan kontrol peta.

REST untuk snapshot, WebSocket untuk stream posisi. Autentikasi JWT lokal
(`admin` / `viewer` / `driver`); hanya admin yang boleh menulis.

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
  -email fleetadm@test.local -password <password> -count 5 -interval 2s
```

#### Alur "Plan route" (Fleet)

1. Admin pilih unit → **Plan route**. Titik awal otomatis diisi dengan posisi
   terakhir unit tersebut.
2. Admin klik ikon crosshair pada baris **B** (tujuan), lalu klik peta.
   Permintaan langsung dikirim ke `POST /api/vehicles/:id/route`. (Kalau
   titik awal dipetik manual, request menunggu tombol **Build route**.)
3. Backend memanggil OSRM dan menyimpan polyline-nya di
   `vehicle_routes.geom` (LineString, SRID 4326). Bila OSRM meny snap kedua
   ujung ke simpul yang sama — garis yang tidak bisa disimpan PostGIS — rute
   memakai garis lurus (`source: "direct"`), bukan gagal. Rute lama dibatalkan
   dan rute baru disimpan dalam satu transaksi, jadi satu unit selalu punya
   paling banyak satu rute aktif.
4. Peta menggambar polyline dalam **warna unit tersebut**, kamera menyesui
   ke jalur baru, dan card kendaraan menampilkan *"Following route to [nama
   tujuan]"*. Rute aktif ikut dibawa oleh `GET /api/vehicles`
   (`active_route`).
5. Simulator membaca `GET /api/routes` tiap tick: unit tanpa rute bergerak
   bebas, unit dengan rute menyusuri polyline selangkah demi selangkah, lalu
   menutup rute (status `completed`) ketika tiba di ujung garis.

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
| `GET` `POST` | `/api/vehicles` | daftar armada + posisi terakhir + rute aktif (`active_route`) / daftar unit baru |
| `PUT` | `/api/vehicles/:id` | ubah unit (nama, tipe, driver, **warna rute**) |
| `POST` | `/api/vehicles/:id/position` | lapor posisi (`lat`, `lng`, `speed_kmh`, `heading`, `status` opsional) → disiar ke WS |
| `GET` | `/api/vehicles/stream` | WebSocket: frame `snapshot` saat connect, lalu `position` |
| `GET` | `/api/routes` | semua rute |
| `GET` | `/api/vehicles/:id/routes` | riwayat rute satu kendaraan |
| `POST` | `/api/vehicles/:id/route` | rencanakan rute `{origin:{lat,lng,name}, destination:{…}}` |
| `DELETE` | `/api/vehicles/:id/route` | batalkan rute aktif |

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
 "heading": 353.2, "route_color": "#2563eb",
 "last_seen_at": "…", "active_route": {…}}
```

`heading` adalah arah kompas unit dalam derajat (0 = utara, 90 = timur),
`null` kalau feed tidak melaporkannya. `route_color` adalah warna jalur
rute unit tersebut; setiap unit punya warnanya sendiri.

Bentuk rute (yang di `GET /api/routes` dan di `active_route`):

```json
{"id": 32, "vehicle_id": 1,
 "origin": {"lat": -6.929, "lng": 107.636, "name": "Gudang"},
 "destination": {"lat": -6.95, "lng": 107.61, "name": "Cisitu"},
 "geometry": {"type": "LineString", "coordinates": [[107.636, -6.929], "…"]},
 "distance_m": 14187, "duration_s": 1016, "source": "osrm",
 "status": "active", "progress": 0.05, "remaining_m": 13478,
 "created_at": "…", "ended_at": null}
```

`source` = `osrm` (jalur jalan raya) atau `direct` (garis lurus). GeoJSON
berisi koordinat `[lng, lat]`. `progress` dihitung PostGIS dari posisi
terakhir unit (`ST_LineLocatePoint`).

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

## Keamanan

- **JWT dengan refresh.** Login memberi access token (default 24 jam) dan
  refresh token (default 30 hari). Client menukar refresh token untuk access
  token baru, jadi sesi tidak berakhir sendiri. Peran: `admin` (baca + tulis),
  `viewer` (baca), `driver` (baca + armada miliknya).
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
  entity/ vehicle/        # model + validasi + unit test (vehicle juga model rute)
  handler/                # entities, zones, vehicles, routes, regions, islands, auth
  middleware/             # kompresi gzip
  routing/                # klien OSRM + fallback direct
  ws/                     # hub WebSocket
migrations/               # 001-016 up/down + README
scripts/empty-state.sql   # kembali ke layar "No entities yet."
frontend/src/
  lib/                    # api.ts, types.ts, vehicleStream.ts, session.ts
  components/             # map layers, panel, form, dialog
  App.tsx                 # state, tab, toast, sheet mobile
  theme-red.css           # tema visual terakhir (warna rute, pin, kontrol)
AGENT.md                  # aturan kerja proyek
README.md                 # file ini
```
