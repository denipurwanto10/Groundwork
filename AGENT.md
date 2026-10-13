# AGENT.md — Entity Management (Backend + Frontend)

## Ruang Lingkup Proyek

Take-home test untuk posisi Software Developer (tenggat 2 hari). Backend Go + Gin + PostgreSQL + pgx, frontend React + TypeScript + Vite + Leaflet.

**Jangan** tambahkan: auth, Redis, Docker, microservices, deployment, atau abstraksi apa pun di luar kebutuhan proyek 2 hari. Migration berupa SQL polos di `migrations/` (tanpa framework). Frontend memakai Fetch API + React hooks + state sederhana; satu-satunya dependensi adalah `leaflet`, `react-leaflet`, `@types/leaflet`.

## Konvensi Kode

- **Struktur**: `cmd/server` (entry point), `internal/config`, `internal/db`, `internal/entity` (model + validasi), `internal/handler` — datar, tanpa layering yang berlebihan.
- **Penamaan**: Konvensi standar Go. Nama package = nama direktori. Tanpa pengulangan (`config.Config`, bukan `config.ConfigConfig`).
- **Dependensi**: Hanya `github.com/gin-gonic/gin`, `github.com/jackc/pgx/v5`, `github.com/joho/godotenv`. Tambah yang lain hanya bila benar-benar dibutuhkan.
- **Penanganan error**: Bungkus dengan konteks (`fmt.Errorf("...: %w", err)`). Jangan menelan error. Kembalikan error ke pemanggil; main yang menangani logging/exit.
- **Context**: Teruskan `context.Context` ke semua pemanggilan DB. Pakai timeout (5 detik health, 10 detik connect, timeout server yang bisa dikonfigurasi).
- **Lingkungan**: Semua konfigurasi via `.env` (diabaikan Git). `.env.example` ikut repo. Tanpa secret yang di-hardcode.

## Standar Validasi

- `gofmt -l .` tidak boleh menampilkan output apa pun.
- `go build ./...` harus sukses.
- `go vet ./...` harus lolos.
- Endpoint health mengembalikan 200 hanya bila ping DB berhasil; 503 bila gagal.
- Jangan pernah mengklaim test lolos tanpa menjalankannya.

## Keamanan

- `.env` tidak pernah di-commit. `.gitignore` ditegakkan.
- Tanpa password, token, API key di source maupun dokumentasi.
- Kredensial DB hanya via environment variable.
- Mode SSL bisa dikonfigurasi (default `disable` untuk pengembangan lokal).

## Kewajiban Pengujian

- Laporkan output perintah yang sebenarnya, bukan ekspektasi.
- Bila PostgreSQL tidak tersedia, nyatakan — jangan memalsukan keberhasilan.
- Uji endpoint health: curl `GET /health` dengan DB menyala → 200, DB mati → 503.

## Alur Kerja

1. Periksa file yang ada sebelum membuat yang baru.
2. Buat perubahan terkecil yang layak.
3. Jalankan `gofmt`, `go build`, `go vet` setelah mengedit.
4. Verifikasi perilaku secara manual (curl / UI headless) sebelum melapor selesai.
5. Tanpa deployment tanpa persetujuan eksplisit user.

## Status Saat Ini (Backend + Frontend Selesai)

- ✅ Modul Go diinisialisasi (`entity-management`)
- ✅ Pemuatan konfigurasi dengan godotenv
- ✅ Connection pool pgx dengan timeout
- ✅ Server Gin dengan graceful shutdown
- ✅ `GET /health` memeriksa konektivitas DB
- ✅ `.env.example`, `.gitignore`, `AGENT.md`
- ✅ Migration 001: tabel `entities` sebagai SQL polos (`migrations/`)
- ✅ CRUD backend: `GET/POST /api/entities`, `GET/PUT/DELETE /api/entities/:id`
- ✅ Validasi di `internal/entity` (mencerminkan CHECK di DB) + query pgx berparameter
- ✅ Unit test (`internal/entity`) + integration test (`internal/handler`, tag `integration`)
- ✅ Frontend `frontend/` (React + TS + Vite + Leaflet): `VITE_API_BASE_URL`, klien Fetch API, proxy Vite untuk `/api` + `/health`, EntityMap/List/Detail/Form/ConfirmDialog, CRUD penuh tersinkron dengan backend, state loading/empty/error/toast, layout responsif — terverifikasi live (Playwright headless, desktop + mobile)