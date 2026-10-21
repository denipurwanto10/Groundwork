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
  sebagai sumber data.
- **Rute** — jalur dari–ke per kendaraan (OSRM), ditutup otomatis saat tiba.

## Daftar Isi

- [Tech Stack](#tech-stack)
- [Basemap](#basemap)
- [Arsitektur](#arsitektur)
- [Mulai Cepat](#mulai-cepat)
- [Data Wilayah](#data-wilayah)
- [Endpoint API](#endpoint-api)
- [Perilaku Zona](#perilaku-zona)
- [Pengujian](#pengujian)
- [Keterbatasan](#keterbatasan)
- [Struktur Proyek](#struktur-proyek)

## Tech Stack

| Lapisan  | Teknologi |
| -------- | --------- |
| Backend  | Go 1.27, Gin, pgx/v5 (`pgxpool`), `coder/websocket`, `godotenv` |
| Frontend | React 19, TypeScript, Vite 8, Leaflet 1.9 + React-Leaflet 5, Leaflet-Geoman |
| Database | PostgreSQL 18 + PostGIS (`geometry(MultiPolygon,4326)`, `geography(Point,4326)`) |
| Routing  | OSRM publik (`router.project-osrm.org`), fallback garis lurus bila dimatikan |
| Migrasi  | SQL polos di `migrations/`, tanpa framework |

## Basemap

Tile raster OpenStreetMap apa adanya, lalu didesaturasi di CSS:

- URL: `https://tile.openstreetmap.org/{z}/{x}/{y}.png` (tanpa API key)
- Kelas `tiles-mono` — tema terang `grayscale(1) contrast(1.06)`,
  tema gelap `grayscale(1) invert(1)` (lihat `frontend/src/App.css`)
- Provider lain lewat `VITE_MAP_TILES_URL` di `frontend/.env`
- Atribusi "© OpenStreetMap contributors" wajib tetap tampil (sudah ada di peta)

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
| `cmd/import-regions` | importer dataset COD-AB (zip/geojson) → `admin_regions` |
| `cmd/simulator` | pengirim posisi palsu untuk armada (sumber data live tracking) |
| `internal/config`, `internal/db` | `.env` + DSN, `pgxpool` + health check |
| `internal/entity`, `internal/vehicle` | model + `Normalize`/`Validate` + unit test |
| `internal/handler` | entities, zones, vehicles, routes, regions, islands, zone-from-region, health |
| `internal/routing` | klien OSRM (`OSRM_BASE_URL`, `off` = fallback `direct`) |
| `internal/ws` | hub fan-out untuk stream posisi kendaraan |

**Frontend** (`frontend/src/`)

`App.tsx` memegang state dan tiga tab (Places / Zones / Live);
`EntityMap` menyusun layer peta: `RegionLayer` (batas administratif),
`ZoneLayer` (polygon + editor Geoman), `VehicleLayer` (puck armada),
`RouteLayer` (jalur). Panel: `EntityList`/`EntityDetail`/`EntityForm`/
`ZonePanel`/`RegionZonePicker`/`VehiclePanel`. Kontrak API di `lib/types.ts`,
client di `lib/api.ts`, stream di `lib/vehicleStream.ts`.

REST untuk snapshot, WebSocket untuk stream posisi. Tanpa auth — aplikasi
lokal satu pengguna (lihat `AGENT.md`).

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

cp .env.example .env      # lalu isi DB_PASSWORD
```

| Variable | Default | Keterangan |
| -------- | ------- | ---------- |
| `SERVER_PORT` | `8080` | Port backend (di mesin ini dipakai `8091`) |
| `DB_HOST` / `DB_PORT` | `localhost` / `5432` | Koneksi Postgres |
| `DB_USER` / `DB_NAME` | `postgres` / `entity_management` | Koneksi Postgres |
| `DB_PASSWORD` | _(kosong)_ | Wajib diisi lokal, jangan commit |
| `DB_SSL_MODE` | `disable` | `require` untuk server ber-TLS |
| `OSRM_BASE_URL` | `https://router.project-osrm.org` | Set `off` untuk rute garis lurus |
| `VITE_BACKEND` (`frontend/`) | `http://localhost:8091` | Target proxy Vite saat dev |
| `VITE_API_BASE_URL` (`frontend/`) | _(kosong)_ | Origin backend untuk build produksi |
| `VITE_MAP_TILES_URL` (`frontend/`) | tile OSM | Provider tile lain |

### 2. Migrasi

Plain SQL, dijalankan berurutan. Detail per file: `migrations/README.md`.

```bash
export $(grep -v '^#' .env | xargs)
for f in migrations/0*.up.sql; do
  PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
    -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -f "$f" || break
done

# Opsional: 8 landmark se-Indonesia untuk review (manual, sekali)
PGPASSWORD="$DB_PASSWORD" psql -h "$DB_HOST" -p "$DB_PORT" \
  -U "$DB_USER" -d "$DB_NAME" -v ON_ERROR_STOP=1 -f migrations/seed.sql
```

Rollback: jalankan `migrations/*.down.sql` dengan urutan terbalik — menghapus
tabel beserta datanya.

### 3. Data wilayah (sekali saja)

Lihat [Data Wilayah](#data-wilayah). Tanpa langkah ini aplikasi tetap jalan,
hanya layer batas administratif dan "zona dari wilayah" yang kosong.

### 4. Backend

```bash
go build -o bin/server.exe ./cmd/server
SERVER_PORT=8091 ./bin/server.exe        # :8080 bila .env tidak diubah
curl http://localhost:8091/health
```

Simulator armada (terminal lain) — membuat kendaraan yang belum ada, lalu
mengirim satu posisi per interval:

```bash
go run ./cmd/simulator -api http://localhost:8091 -count 5 -interval 2s
```

#### Alur "Plan route" (Fleet)

1. Admin pilih unit → **Plan route**. Titik awal otomatis diisi dengan posisi
   terakhir unit tersebut.
2. Admin klik ikon crosshair pada baris **B** (tujuan), lalu klik peta.
   Permintaan langsung dikirim ke `POST /api/vehicles/:id/route`.
3. Backend memanggil OSRM. Bila engine tidak/snapping kedua titik ke simpul
   yang sama, rute memakai garis lurus (`source: "direct"`), bukan gagal.
4. Peta menggambar polyline biru tebal dan kamera menyesuai ke jalur baru.
   Card kendaraan menampilkan *"Following route to [nama tujuan]"*.
5. Simulator membaca `GET /api/routes` tiap tick: unit tanpa rute bergerak
   bebas, unit dengan rute menyusuri polyline dan menutup rute (status
   `completed`) ketika tiba di ujung garis.

### 5. Frontend

```bash
cd frontend
npm install
npm run dev            # http://localhost:5173
```

Vite mem-proxy `/api` dan `/health` ke `VITE_BACKEND` (default `:8091`),
termasuk upgrade WebSocket (`ws: true`). Badge di header menunjukkan
`API online` / `API checking` / `API offline`; saat offline frontend mencoba
lagi otomatis tiap 15 detik.

## Data Wilayah

Batas administratif berasal dari **HDX COD-AB Indonesia** (`cod-ab-idn`,
CC BY-IGO, sumber BPS). Arsipnya ±456 MB dan **tidak ikut repo** (`/data`
ada di `.gitignore`) — unduh ulang dari HDX bila perlu.

```bash
# Probe dulu (tanpa menulis), lalu impor per level
go run ./cmd/import-regions -file data/idn_admin_boundaries.geojson.zip -probe
go run ./cmd/import-regions -file data/idn_admin_boundaries.geojson.zip -levels 0,1,2
go run ./cmd/import-regions -file data/idn_admin_boundaries.geojson.zip -levels 3

# Hasil di mesin ini: 7.626 baris (L0=1, L1=34, L2=522, L3=7.069), 0 orphan
```

## Endpoint API

| Method | Path | Keterangan |
| ------ | ---- | ---------- |
| `GET` | `/health` | `200` bila DB sehat, `503` bila tidak |
| `GET` | `/api/entities` | daftar aset; `?zone=<id>` memfilter yang di dalam zona (`ST_Covers`) |
| `GET` `POST` | `/api/entities`, `/api/entities/:id` | baca satu / buat |
| `PUT` `DELETE` | `/api/entities/:id` | ubah penuh / hapus (`204`) |
| `GET` | `/api/regions` | batas administratif; wajib `?level=0..4`, opsional `parent`, `code`, `q` |
| `GET` | `/api/regions/count`, `/api/regions/:code` | jumlah / satu wilayah |
| `GET` `POST` | `/api/zones` | daftar (GeoJSON FeatureCollection) / buat (geometry wajib) |
| `GET` `PUT` `DELETE` | `/api/zones/:id` | baca / ubah / hapus |
| `POST` | `/api/zones/import` | impor GeoJSON FeatureCollection → banyak zona |
| `POST` | `/api/zones/from-region` | buat zona dari kode wilayah administratif |
| `GET` | `/api/islands` | gugus pulau yang bisa dijadikan zona |
| `POST` | `/api/zones/from-island` | buat zona dari gugus pulau |
| `GET` `POST` | `/api/vehicles` | daftar armada + posisi terakhir + rute aktif (`active_route`) / daftar unit baru |
| `POST` | `/api/vehicles/:id/position` | lapor posisi (`lat`, `lng`, `speed_kmh`, `status` opsional) → disiar ke WS |
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

Validasi: `name`/`type` wajib (maks 255/100), `status` `active`/`inactive`,
latitude −90…90, longitude −180…180. `PUT` adalah full replace.
Error selalu `{"error":{"code":"…","message":"…"}}`, dengan `fields` untuk
`validation_failed`.

```bash
curl -X POST http://localhost:8091/api/entities \
  -H 'Content-Type: application/json' \
  -d '{"name":"Monas","type":"monument","status":"active","latitude":-6.1754,"longitude":106.8272}'

curl 'http://localhost:8091/api/entities?zone=11'
```

## Perilaku Zona

- Kategori: `operational`, `restricted`, `planned`, `other`; warna bebas
  (`#rrggbb`), dipakai di peta dan kartu zona.
- Geometry di respons adalah **salinan tampilan**: polygon di atas 2.000 titik
  disederhanakan (`ST_SimplifyPreserveTopology`, toleransi `0.0005°`) supaya
  respons tidak membengkak. Terukur: zona 227 ribu titik turun dari ±5 MB ke
  ±540 KB. Data di database tetap utuh.
- Editor vertex hanya aktif sampai `MAX_EDITABLE_VERTICES = 2.000`; di atas itu
  polygon tidak bisa diedit (menghindari freeze browser).
- `PUT` **tanpa** `geometry` hanya mengubah atribut — bentuk tersimpan tidak
  ditulis ulang. Frontend membandingkan geometry saat load vs saat simpan dan
  hanya mengirim geometry bila benar-benar berubah.

## Pengujian

```bash
gofmt -l .                                        # harus tanpa output
go vet ./... && go build ./...
go test ./internal/entity/ ./internal/vehicle/ ./internal/ws/
go test -tags integration ./internal/handler/     # butuh DB + migrasi
```

```bash
cd frontend
npx tsc -b        # typecheck
npm run lint      # oxlint
npm run build     # tsc -b && vite build → dist/
```

CI (`.github/workflows/ci.yml`): gofmt/vet/build/unit test + `tsc -b` + build.

## Keterbatasan

- **Tanpa auth** — untuk satu pengguna di mesin lokal; jangan diekspos ke
  internet apa adanya.
- **Tanpa kompresi HTTP** — respons JSON dikirim apa adanya. Yang besar hanya
  geometry zona, dan itu sudah diringkas di SQL.
- **`GET /api/zones/:id` untuk zona sangat besar masih lambat** (±5 detik untuk
  polygon 227 ribu titik) meski query yang sama di `psql` ±0,8 detik. Akar
  penyebabnya belum ditemukan; penyederhanaan geometry memangkas payload, bukan
  latensi query.
- `heading` kendaraan tidak disimpan; hanya `lat`, `lng`, `speed_kmh`, waktu.
- Warna rute masih tetap (`#2563eb`), belum bisa diatur per kendaraan.
- Hitungan entitas/kendaraan per zona tidak auto-refresh setelah mutasi entitas
  — daftar zona perlu dimuat ulang.
- Dataset COD-AB (456 MB) tidak ikut repo; tanpa impor, layer wilayah kosong.
- `migrations/seed.sql` sengaja tidak bernomor agar migrasi dari nol tetap
  kosong dan empty state "No entities yet." bisa direproduksi.

## Struktur Proyek

```text
cmd/
  server/                 # entry point Gin + graceful shutdown
  simulator/              # sumber data live tracking (armada palsu)
  import-regions/         # importer COD-AB → admin_regions
internal/
  config/ db/             # .env + DSN, pgxpool + health
  entity/ vehicle/        # model + validasi + unit test
  handler/                # entities, zones, vehicles, routes, regions, islands
  routing/                # klien OSRM + fallback direct
  ws/                     # hub WebSocket
migrations/               # 001–007 up/down + seed.sql (opsional) + README
frontend/src/
  lib/                    # api.ts, types.ts, vehicleStream.ts
  components/             # map layers, panel, form, dialog
  App.tsx                 # state, tab, toast, sheet mobile
AGENT.md                  # aturan kerja proyek
README.md                 # file ini
```
