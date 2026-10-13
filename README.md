# Entity Management — Take-Home Test

Aplikasi web untuk menampilkan dan mengelola entitas geografis
(nama, tipe, status, latitude, longitude) di atas peta Leaflet,
dengan REST API Go dan database PostgreSQL.

## Daftar Isi

- [Fitur](#fitur)
- [Tech Stack](#tech-stack)
- [Arsitektur](#arsitektur)
- [Mulai Cepat](#mulai-cepat)
- [Endpoint API](#endpoint-api)
- [Pengujian](#pengujian)
- [Alasan Pemilihan Teknologi](#alasan-pemilihan-teknologi)
- [Workflow Agentic AI](#workflow-agentic-ai)
- [Keterbatasan](#keterbatasan)
- [Struktur Proyek](#struktur-proyek)

## Fitur

- Entitas tampil di peta (marker + popup)
- Tambah, ubah, hapus entitas (hapus pakai dialog konfirmasi)
- Lihat detail entitas
- Validasi di frontend dan backend
- Tombol "show all" untuk melihat semua marker sekaligus
- CI GitHub Actions (backend: gofmt/vet/build/test; frontend: tsc/build)

## Tech Stack

| Lapisan  | Teknologi                                             |
| -------- | ----------------------------------------------------- |
| Backend  | Go, Gin, PostgreSQL, pgx (`jackc/pgx/v5`), `godotenv` |
| Frontend | React 19, TypeScript, Vite, Leaflet + React-Leaflet   |
| Database | PostgreSQL, migration plain SQL di `migrations/`      |

## Arsitektur

```text
frontend/ (React + Vite) --HTTP/JSON--> Go + Gin (:8080) --pgx--> PostgreSQL (entities)
```

- **Backend** — struktur flat: `cmd/server` (entry), `internal/config`,
  `internal/db` (pgxpool + health check), `internal/entity`
  (model + `Normalize`/`Validate`), `internal/handler` (health + CRUD).
  Semua query berparameter `$1…`, timeout 5 detik per query,
  error DB hanya dilog di server (klien menerima `internal_error`).
- **Frontend** — Fetch API + React hooks + state sederhana:
  `src/lib/api.ts` (client + `ApiError`), `src/lib/types.ts`
  (tipe + validasi mirror backend), komponen `EntityMap` /
  `EntityList` / `EntityDetail` / `EntityForm` / `ConfirmDialog`,
  state di `App.tsx`.
- **Dev vs produksi** — backend tanpa CORS. Saat development,
  Vite mem-proxy `/api` + `/health` ke `http://localhost:8080`;
  saat produksi, origin backend diatur via `VITE_API_BASE_URL`.

## Mulai Cepat

### Prasyarat

- Windows + Git (perintah di bawah memakai Git Bash)
- Go 1.27+ (`go version`)
- Node.js + npm (`node --version`)
- PostgreSQL 14+ di `localhost:5432`

### 1. Siapkan database dan `.env`

```bash
# Buat database sekali saja (via psql / pgAdmin):
CREATE DATABASE entity_management;

# Salin konfigurasi (jangan commit file .env):
cp .env.example .env
# Isi DB_PASSWORD, sesuaikan DB_HOST/DB_PORT/DB_USER/DB_NAME bila perlu.
```

| Variable | Default | Keterangan |
| -------- | ------- | ---------- |
| `SERVER_PORT` | `8080` | Port backend |
| `DB_HOST` / `DB_PORT` | `localhost` / `5432` | Koneksi Postgres |
| `DB_USER` / `DB_NAME` | `postgres` / `entity_management` | Koneksi Postgres |
| `DB_PASSWORD` | _(kosong)_ | Wajib diisi lokal, jangan commit |
| `VITE_API_BASE_URL` (`frontend/.env`) | _(kosong)_ | Origin backend produksi; kosongkan saat dev (proxy Vite) |

### 2. Terapkan migration

Migration adalah plain SQL idempotent (`IF NOT EXISTS` / `OR REPLACE`),
dijalankan sesuai urutan nama file. Detail: `migrations/README.md`.

```bash
# Dari root repo, dengan .env sudah terisi (Git Bash):
export $(grep -v '^#' .env | xargs)
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.up.sql

# Verifikasi: \d entities menampilkan kolom, index,
# CHECK (name/type/status/latitude/longitude), dan trigger updated_at.
```

Rollback (menghancurkan tabel + data, hanya bila diperlukan):

```bash
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.down.sql
```

### 3. Jalankan backend dan frontend

Terminal 1 — backend (dari root repo):

```bash
go run ./cmd/server
# → server starting on port 8080
curl http://localhost:8080/health
```

Terminal 2 — frontend (dari `frontend/`):

```bash
npm install
npm run dev            # default http://localhost:5173
npm run dev -- --port 5174   # bila 5173 sudah dipakai aplikasi lain
```

Buka URL dev di browser. Indikator hijau `Backend connected`
di header berarti frontend mencapai backend via proxy.

## Endpoint API

| Method | Path | Sukses | Error |
| ------ | ---- | ------ | ----- |
| `GET` | `/health` | `200` status + checks | `503` bila DB tidak sehat |
| `GET` | `/api/entities` | `200 {"data":[...]}` (`[]` bila kosong) | `500 internal_error` |
| `GET` | `/api/entities/:id` | `200 {"data":{…}}` | `400 invalid_id` · `404 not_found` |
| `POST` | `/api/entities` | `201 {"data":{…}}` | `400 invalid_json` · `400 validation_failed` + `fields` |
| `PUT` | `/api/entities/:id` | `200 {"data":{…}}` | `400 invalid_id` · `400 invalid_json` · `400 validation_failed` · `404 not_found` |
| `DELETE` | `/api/entities/:id` | `204` tanpa body | `400 invalid_id` · `404 not_found` |

Bentuk entity:

```json
{"id": 1, "name": "…", "type": "…", "status": "active",
 "latitude": -6.1, "longitude": 106.8,
 "created_at": "…", "updated_at": "…"}
```

Aturan validasi: name/type wajib non-kosong (maks 255/100),
status `active`/`inactive`, latitude −90…90, longitude −180…180.
`PUT` adalah full replace (kirim semua field).

Contoh request/response:

```bash
# Buat entitas
curl -X POST http://localhost:8080/api/entities \
  -H 'Content-Type: application/json' \
  -d '{"name":"Monas","type":"monument","status":"active","latitude":-6.1754,"longitude":106.8272}'
# → 201 {"data":{"id":1,"name":"Monas","type":"monument","status":"active",
#     "latitude":-6.1754,"longitude":106.8272,"created_at":"…","updated_at":"…"}}

# Validasi gagal
curl -X POST http://localhost:8080/api/entities \
  -H 'Content-Type: application/json' -d '{"name":"","type":"x","status":"active","latitude":0,"longitude":0}'
# → 400 {"error":{"code":"validation_failed","message":"validation failed",
#     "fields":{"name":"…"}}}

# ID bukan angka / tidak ada
curl http://localhost:8080/api/entities/abc   # → 400 {"error":{"code":"invalid_id",…}}
curl http://localhost:8080/api/entities/9999  # → 404 {"error":{"code":"not_found",…}}
```

## Pengujian

Backend (dari root; Go harus di PATH):

```bash
gofmt -l .                                        # harus tanpa output
go vet ./...                                      # harus lolos
go build ./...                                    # harus sukses
go test ./internal/entity/ -v                     # unit: Normalize/Validate
go test -tags integration ./internal/handler/ -v  # butuh DB + migration
```

Frontend (dari `frontend/`):

```bash
npx tsc -b        # typecheck
npm run build     # build produksi → dist/
npm run lint      # oxlint
```

<details>
<summary>Hasil yang terverifikasi (klik untuk membuka)</summary>

| Area | Hasil |
| ---- | ----- |
| Backend (static + unit) | `gofmt` bersih, `vet`/`build` OK, unit PASS, integration PASS lawan PostgreSQL asli |
| Frontend (static) | `tsc` bersih, `vite build` sukses, `lint` exit 0 (1 warning non-blocking fetch-on-mount di `App.tsx`) |
| API via curl | Siklus CRUD penuh: 201/200/400/404/204 |
| UI headless (desktop + mobile) | CRUD 7/7 sinkron ke backend; interaksi peta 4/4 |
| Error-path | Backend mati → badge offline + banner + retry + toast; backend nyala → retry memulihkan list (0 JS error) |
| Skeleton loading | API dilambatkan 2 dtk → 4 baris skeleton + `aria-busy=true`, hilang setelah data tampil |
| Preview produksi (`dist/`) | 6/6: 5 marker, klik marker buka detail, create → edit → delete sinkron, 0 JS error |
| Clone bersih | 45 file disalin terpisah → `gofmt`/`vet`/`build` OK, unit PASS, `tsc`/`lint`/`build` OK |

</details>

## Alasan Pemilihan Teknologi

Setiap teknologi di bawah benar-benar dipakai (lihat `go.mod`,
`frontend/package.json`, dan source terkait). Prinsipnya: pilih yang
paling sederhana untuk requirement, tanpa dependensi tak perlu.

**Backend**

| Teknologi | Kenapa dipakai | Dipakai di |
| --------- | -------------- | ---------- |
| Go | Biner tunggal, kompilasi cepat, stdlib (`net/http`, `context`) cukup untuk REST kecil | `cmd/` + `internal/` |
| Gin | Router ringan: grouping `/api`, binding JSON, middleware Recovery/Logger | `cmd/server/main.go`, `internal/handler/` |
| PostgreSQL | Requirement take-home test; `CHECK` + trigger menjaga validitas koordinat dan `updated_at` | `migrations/001_create_entities.up.sql` |
| pgx | Driver Postgres native + pool `pgxpool`; semua query berparameter `$1…` (aman dari SQL injection) | `internal/db/db.go` |
| godotenv | Memuat `.env` lokal agar kredensial tidak tertulis di kode | `internal/config/config.go` |

**Frontend**

| Teknologi | Kenapa dipakai | Dipakai di |
| --------- | -------------- | ---------- |
| React | Requirement; komponen (`EntityMap`, `EntityList`, `EntityDetail`, `EntityForm`, `ConfirmDialog`) memetakan langsung ke CRUD | `frontend/src/components/` |
| TypeScript | Tipe `Entity`/`EntityInput` mengikat kontrak JSON backend di level kompilasi | `src/lib/types.ts` |
| Vite | Requirement; dev server cepat + proxy `/api` + `/health` menyelesaikan CORS saat dev | `vite.config.ts` |
| Leaflet | Peta ringan tanpa API key; render peta, marker, popup | `src/components/EntityMap.tsx` |
| React-Leaflet | Binding React resmi (`MapContainer`, `Marker`, `Popup`, `useMapEvents`) tanpa DOM manual | `src/components/EntityMap.tsx` |
| OpenStreetMap | Tile default gratis tanpa API key; kode mendukung `VITE_MAP_TILES_URL` untuk provider lain | `src/components/EntityMap.tsx` |
| Fetch API | Requirement; client tipis `ApiError{code,status,fields}` cukup untuk 5 endpoint | `src/lib/api.ts` |

## Workflow Agentic AI

Satu agen AI bekerja di repository lokal, user sebagai reviewer.
Alur per tahap:

1. Agen membaca file yang ada (`AGENT.md`, source, log sesi)
2. Menulis perubahan terkecil yang layak
3. Menjalankan verifikasi (`gofmt`, `vet`, `build`, `test`, `tsc`, `vite build`, `oxlint`, curl, Playwright headless)
4. Melaporkan hasil aktual + keterbatasannya
5. Menunggu persetujuan user (backend → frontend → audit → submission)

Peran konkret AI:

- **Implementasi** — config, pool pgx, handler health + 5 handler CRUD,
  model + validasi, migration SQL, API client + tipe + validasi mirror,
  5 komponen React, `App.tsx`, stylesheet.
- **Debugging dari bukti** — mis. overlay modal menelan klik Leaflet
  (dibuktikan `elementFromPoint`, diperbaiki via `pointer-events`),
  overflow grid mobile (akar `min-width:auto`), port 5173 bentrok
  (terdeteksi via `netstat`), selector Playwright usang.
- **Pengujian** — semua klaim di tabel di atas dijalankan, bukan asumsi.
- **Dokumentasi** — README root + `frontend` + `migrations` +
  `AGENT.md`, ditulis dari file/perintah yang benar-benar ada.

Batasan: arsitektur, kontrak API, dan persetujuan tiap tahap di tangan
user. AI tidak commit/push tanpa izin, tidak menebak password DB,
tidak menyentuh proses milik aplikasi lain.

## Keterbatasan

- `migrations/*.sql` ikut repo — diperlukan reviewer untuk membuat database yang sama.
- `migrations/seed.sql` opsional (5 landmark Jakarta, dijalankan manual sekali;
  sengaja bukan `002_*` agar migration dari nol tetap kosong dan empty state
  "No entities yet." bisa direproduksi reviewer).

## Struktur Proyek

```text
cmd/server/              # entry point Gin + graceful shutdown
internal/
  config/                # config via .env + DSN builder
  db/                    # pgxpool + HealthCheck
  entity/                # model + validasi + unit test
  handler/               # /health + CRUD + integration test
migrations/              # 001 up/down SQL + seed.sql (opsional, manual) + README
frontend/src/
  lib/                   # api.ts (client), types.ts (tipe + validasi)
  components/            # EntityMap/List/Detail/Form/ConfirmDialog
  App.tsx                # state, toast, error banner
AGENT.md                 # aturan kerja + status proyek
README.md                # file ini
```
