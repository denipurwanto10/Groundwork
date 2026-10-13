# Entity Management — Take-Home Test

Aplikasi web sederhana untuk menampilkan dan mengelola entitas geografis
(nama, tipe, status, latitude, longitude) di atas peta Leaflet,
dengan REST API Go dan database PostgreSQL.

Fitur: tampilkan entitas pada peta · tambah entity · ubah data ·
hapus entity (dengan konfirmasi) · lihat detail ·
validasi data di frontend dan backend.

## Stack

| Lapisan  | Teknologi |
|----------|-----------|
| Backend  | Go, Gin, PostgreSQL, pgx (`jackc/pgx/v5`), `godotenv` |
| Frontend | React 19, TypeScript, Vite, Leaflet (`leaflet`, `react-leaflet`) |
| Database | PostgreSQL, migration sebagai plain SQL di `migrations/` (tanpa framework) |

## Arsitektur singkat

```
┌─────────────┐   HTTP/JSON    ┌──────────────┐   pgx (parameterized)  ┌────────────┐
│  frontend/  │ ─────────────▶ │  Go + Gin    │ ─────────────────────▶ │ PostgreSQL │
│ React + Vite│ ◀───────────── │  :8080       │ ◀───────────────────── │ entities   │
└─────────────┘  {"data": …}   └──────────────┘                        └────────────┘
```

- Backend: struktur flat `cmd/server` (entry), `internal/config`,
  `internal/db` (pgxpool + health check), `internal/entity`
  (model + `Normalize`/`Validate`), `internal/handler`
  (health + 5 handler CRUD). Semua query memakai parameter `$1…`,
  timeout 5 detik per query, error DB dilog di server
  (klien hanya menerima `internal_error`).
- Frontend: Fetch API + React hooks + state sederhana.
  `src/lib/api.ts` (client + `ApiError`), `src/lib/types.ts`
  (tipe + validasi mirror backend), komponen
  `EntityMap` / `EntityList` / `EntityDetail` / `EntityForm` /
  `ConfirmDialog`, state di `App.tsx`.
  Backend tidak punya CORS — dev memakai Vite proxy
  (`/api` + `/health` → `http://localhost:8080`).
  Produksi memakai `VITE_API_BASE_URL`.
- Kontrak JSON, validasi ganda (frontend + backend + CHECK di DB),
  dan keputusan desain tercatat di `AGENT.md`.

## Prasyarat

- Windows dengan Git (Git Bash untuk perintah di bawah).
- Go 1.27+ (`go version`).
- Node.js + npm (`node --version`).
- PostgreSQL 14+ berjalan di `localhost:5432`
  (klien `psql` diperlukan hanya untuk menerapkan migration).
  Di mesin pengembangan: Postgres port 5432 + database `entity_management`.

## Setup PostgreSQL (Windows)

```bash
# 1. Buat database sekali saja (lewat psql / pgAdmin / installer EDB):
CREATE DATABASE entity_management;

# 2. Salin konfigurasi (jangan commit file .env):
cp .env.example .env
# Isi DB_PASSWORD dan sesuaikan DB_HOST/DB_PORT/DB_USER/DB_NAME bila perlu.
```

## Penerapan migration

Migration adalah plain SQL idempotent (`IF NOT EXISTS` / `OR REPLACE`),
dijalankan sesuai urutan nama file. Detail: `migrations/README.md`.

```bash
# Dari root repo, dengan .env sudah terisi (Git Bash):
export $(grep -v '^#' .env | xargs)
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.up.sql

# Verifikasi: \d entities harus menunjukkan kolom, index,
# CHECK (name/type/status/latitude/longitude), dan trigger updated_at.
```

Rollback (menghancurkan tabel + data, hanya bila diperlukan):

```bash
PGPASSWORD="$DB_PASSWORD" psql \
  -h "$DB_HOST" -p "$DB_PORT" -U "$DB_USER" -d "$DB_NAME" \
  -v ON_ERROR_STOP=1 \
  -f migrations/001_create_entities.down.sql
```

## Konfigurasi `.env`

`.env` (gitignored) menampung kredensial lokal; `.env.example`
(tanpa rahasia) adalah acuannya. Frontend memakai `.env` terpisah
di `frontend/` (opsional di dev):

| Variable | Default | Keterangan |
|---|---|---|
| `SERVER_PORT` | `8080` | Port backend |
| `DB_HOST/DB_PORT/DB_USER/DB_NAME` | `localhost/5432/postgres/entity_management` | Koneksi Postgres |
| `DB_PASSWORD` | _(kosong)_ | **Wajib diisi lokal, jangan commit** |
| `VITE_API_BASE_URL` (di `frontend/.env`) | _(kosong = same origin)_ | Origin backend untuk production build; kosongkan saat dev (proxy Vite) |

## Cara menjalankan

Terminal 1 — backend (dari root repo):

```bash
go run ./cmd/server
# → server starting on port 8080
curl http://localhost:8080/health   # {"status":"ok","checks":{"database":"healthy"},…}
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
|---|---|---|---|
| `GET` | `/health` | `200 {"status","checks.database","timestamp"}` | `503` bila DB tidak sehat |
| `GET` | `/api/entities` | `200 {"data":[...]}` (`[]` bila kosong) | `500 internal_error` |
| `GET` | `/api/entities/:id` | `200 {"data":{…}}` | `400 invalid_id` · `404 not_found` |
| `POST` | `/api/entities` | `201 {"data":{…}}` | `400 invalid_json` · `400 validation_failed` + `fields` |
| `PUT` | `/api/entities/:id` | `200 {"data":{…}}` (full replace) | `400` … · `404 not_found` |
| `DELETE` | `/api/entities/:id` | `204` (tanpa body) | `400 invalid_id` · `404 not_found` |

Bentuk entity:
`{id, name, type, status, latitude, longitude, created_at, updated_at}`.

Aturan validasi: name/type wajib non-kosong (maks 255/100),
status `active`/`inactive`, latitude −90…90, longitude −180…180.

## Pengujian

Backend (dari root; Go harus di PATH):

```bash
gofmt -l .                          # harus tanpa output
go vet ./...                        # harus lolos
go build ./...                      # harus sukses
go test ./internal/entity/ -v       # unit: Normalize/Validate
go test -tags integration ./internal/handler/ -v   # butuh DB + migration
```

Frontend (dari `frontend/`):

```bash
npx tsc -b        # typecheck
npm run build     # build produksi → dist/
npm run lint      # oxlint
```

Hasil terakhir: `gofmt` bersih, `vet`/`build` OK, unit PASS,
integration PASS lawan PostgreSQL asli, `tsc` bersih,
`vite build` sukses, `lint` exit 0 (1 warning non-blocking
tentang fetch-on-mount di `App.tsx`).

Uji live yang dilakukan: siklus CRUD penuh via curl
(201/200/400/404/204) + interaksi UI headless (Chromium):
form diblokir untuk input kosong, create → marker + list,
detail, edit, delete dengan dialog, backend sinkron —
di viewport desktop dan mobile.
Interaksi peta 4/4: marker dari data backend → klik marker
membuka detail yang benar → klik peta mengisi input lat/lng
(form tetap terbuka) → cleanup sinkron ke backend.
Error-path terverifikasi: backend dimatikan → badge "API offline",
banner "Can't load entities" + tombol "Try again" muncul,
simulasi mati via route-abort → retry + toast error tampil;
backend dinyalakan → klik retry memulihkan list (0 JS error).
Skeleton terverifikasi via perlambatan `/api/entities` 2 detik:
4 baris skeleton + `aria-busy=true` saat loading,
hilang setelah data tampil.
Preview produksi (`vite preview` → `dist/`) 6/6: 5 marker
untuk 5 entity, klik marker membuka detail Monas,
create → edit → delete via UI sinkron ke backend (count 5),
0 JS error.
Clone bersih terverifikasi: 45 file hasil `git add -A --dry-run`
disalin ke direktori terpisah → `gofmt` bersih, `go vet`/`go build`
OK, unit PASS, `npm install` + `tsc -b` OK, `lint` exit 0
(1 warning fetch-on-mount yang sah), `vite build` sukses.

## Alasan pemilihan teknologi

Setiap teknologi di bawah benar-benar dipakai di proyek ini
(lihat `go.mod`, `frontend/package.json`, dan source terkait).
Prinsipnya: pilih yang paling sederhana untuk requirement,
tanpa dependensi yang tidak perlu — sesuai batasan dua hari.

**Backend**

- **Go** — biner tunggal tanpa runtime tambahan, kompilasi cepat,
  dan standard library-nya (`net/http`, `context`, timeout)
  sudah cukup untuk REST API kecil. Dipakai di seluruh `cmd/` + `internal/`.
- **Gin (`github.com/gin-gonic/gin`)** — router ringan dengan grouping
  (`r.Group("/api")`), binding JSON (`ShouldBindJSON`), dan middleware
  Recovery/Logger siap pakai. Dipakai di `cmd/server/main.go` dan
  `internal/handler/`. Alternatif stdlib murni dipertimbangkan,
  tetapi Gin menghemat boilerplate routing tanpa menambah kerumitan.
- **PostgreSQL** — requirement eksplisit take-home test. Tipe
  `DOUBLE PRECISION` + `CHECK` di `migrations/001_create_entities.up.sql`
  cukup untuk koordinat Leaflet; `TIMESTAMPTZ` + trigger menjaga
  `updated_at` otomatis.
- **pgx (`github.com/jackc/pgx/v5`)** — driver Postgres native untuk Go
  dengan connection pool (`pgxpool`, lihat `internal/db/db.go`).
  Dipilih atas `database/sql` generik karena performanya untuk Postgres
  dan dukungannya untuk tipe native; semua query memakai parameter
  `$1…` sehingga aman dari SQL injection.
- **`godotenv` (`github.com/joho/godotenv`)** — memuat `.env` lokal
  ke environment (`internal/config/config.go`), sehingga kredensial
  tidak tertulis di kode dan `.env.example` bisa dilacak Git
  tanpa rahasia.

**Frontend**

- **React** — requirement eksplisit. Component model-nya
  (`EntityMap`, `EntityList`, `EntityDetail`, `EntityForm`,
  `ConfirmDialog`) memetakan langsung ke fitur CRUD; hooks
  (`useState`, `useEffect`, `useCallback`) cukup untuk state
  sederhana tanpa state manager eksternal.
- **TypeScript** — tipe `Entity`/`EntityInput` di `src/lib/types.ts`
  mengikat kontrak JSON backend di level kompilasi; `tsc -b`
  dijalankan di setiap build sehingga mismatch field ketahuan
  sebelum runtime.
- **Vite** — requirement eksplisit; dev server cepat, proxy bawaan
  (`/api` + `/health` → `:8080` di `vite.config.ts`) menyelesaikan
  masalah CORS saat development tanpa mengubah backend,
  dan `VITE_API_BASE_URL` mengatur origin backend untuk production.
- **Leaflet** — library peta yang ringan dan matang, tanpa API key.
  Dipakai untuk render peta, marker per entity, dan popup
  (`src/components/EntityMap.tsx`). Ukuran bundle jauh lebih kecil
  dibanding alternatif peta komersial — penting untuk deadline singkat.
- **React-Leaflet** — binding React resmi untuk Leaflet
  (`MapContainer`, `Marker`, `Popup`, `useMapEvents`).
  Dipilih agar peta terintegrasi dengan lifecycle React
  (misalnya `ClickPicker` untuk klik-peta dan `FlyToSelected`
  untuk fokus ke entity terpilih) tanpa manipulasi DOM manual.
- **OpenStreetMap (tile standar)** — sumber tile peta default
  (`https://tile.openstreetmap.org/…`), gratis dan tanpa API key,
  sesuai kebutuhan proyek kecil. URL tile dapat diganti lewat
  `VITE_MAP_TILES_URL` bila reviewer memakai provider lain.
- **Fetch API** — requirement eksplisit; client tipis di
  `src/lib/api.ts` (`ApiError` dengan `code`/`status`/`fields`)
  sudah cukup untuk lima endpoint tanpa menambah axios
  atau library HTTP lain.

## Workflow Agentic AI

Pengembangan proyek ini memakai satu agen AI (saya) yang bekerja
langsung di dalam repository lokal, berdampingan dengan user
sebagai reviewer. Alurnya per tahap: agen membaca file yang ada
(`AGENT.md`, source, log sesi) → mengusulkan/menulis perubahan
terkecil → menjalankan verifikasi (`gofmt`, `go vet`, `go build`,
`go test`, `tsc`, `vite build`, `oxlint`, curl, dan skrip
Playwright headless) → melaporkan hasil aktual beserta
keterbatasannya → menunggu persetujuan user sebelum lanjut
ke tahap berikutnya (backend → frontend → audit → submission).

Peran konkret AI selama pengerjaan:

- **Implementasi** — menulis boilerplate dan kode CRUD: config
  (`internal/config`), pool pgx (`internal/db`), handler health +
  lima handler entity, model + validasi (`internal/entity`),
  migration SQL, API client + tipe + validasi mirror di frontend,
  lima komponen React, `App.tsx`, dan stylesheet.
- **Debugging nyata** — menelusuri akar masalah dari bukti,
  bukan tebakan. Contoh: run `go run` pertama exit tanpa output
  (diatasi dengan start ulang sebagai background process);
  port 5173 dipakai aplikasi lain (diidentifikasi via `netstat`,
  dialihkan ke 5174 tanpa mengganggu proses asing);
  selector Playwright salah setelah struktur form berubah
  (diperbaiki dengan membaca `EntityForm.tsx`);
  overflow grid lat/lon di mobile (akar: `min-width:auto` pada grid,
  diperbaiki dengan `minmax(0,1fr)` + stack satu kolom);
  dan bug klik-peta — overlay modal `position:fixed; inset:0`
  menelan klik Leaflet (dibuktikan dengan `elementFromPoint`,
  diperbaiki dengan `pointer-events:none` pada overlay
  + `auto` pada kartu form, lalu diverifikasi ulang 4/4 lolos).
- **Pengujian** — menjalankan semua yang diklaim: unit Go,
  integration lawan PostgreSQL asli, smoke curl
  (201/200/400/404/204), interaksi UI headless desktop + mobile
  (CRUD 7/7, interaksi peta 4/4), error-path (banner + retry + toast
  saat backend mati, lalu recovery), skeleton loading (via API
  yang dilambatkan 2 detik), preview produksi 6/6, verifikasi clone
  bersih 45 file, serta build/lint/typecheck kedua sisi. Tidak ada
  klaim tanpa bukti jalan.
- **Dokumentasi** — menulis `README.md` root dan `frontend`,
  `migrations/README.md`, dan pembaruan `AGENT.md` berdasarkan
  file dan perintah yang benar-benar ada (diverifikasi dengan
  membaca ulang sebelum menulis).

Batasan peran AI: keputusan arsitektur (struktur flat, tanpa
lapisan berlebih), kontrak API, dan persetujuan tiap tahap
tetap di tangan user. AI tidak melakukan commit/push
tanpa persetujuan eksplisit, tidak menebak password database
saat autentikasi gagal (dihentikan sesuai aturan),
dan tidak menyentuh proses milik aplikasi lain di mesin user.

## Keterbatasan yang diketahui

- Keputusan `migrations/*.sql` ikut repo: file SQL diperlukan
  reviewer untuk membuat database yang sama (lihat `migrations/README.md`).
- Data seed 5 landmark Jakarta hanya ada di database lokal;
  reviewer yang menjalankan migration dari nol mulai dengan tabel kosong
  (empty state "No entities yet." sudah terverifikasi).

## Struktur

```
├── cmd/server/            # entry point Gin + graceful shutdown
├── internal/
│   ├── config/            # config via .env + DSN builder
│   ├── db/                # pgxpool + HealthCheck
│   ├── entity/            # model + validasi + unit test
│   └── handler/           # /health + CRUD + integration test
├── migrations/            # 001 up/down SQL + README
├── frontend/              # React + TS + Vite + Leaflet
│   └── src/{lib,components}/  # api client, validasi, 5 komponen, App
├── AGENT.md               # aturan kerja + status proyek
└── README.md              # file ini
```
