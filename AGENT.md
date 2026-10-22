# AGENT.md — Entity Management (Backend + Frontend)

## Ruang Lingkup Proyek

Take-home test untuk posisi Software Developer (tenggat 2 hari). Backend Go + Gin + PostgreSQL + pgx, frontend React + TypeScript + Vite + Leaflet.

**Jangan** tambahkan: auth, Redis, Docker, microservices, deployment, atau abstraksi apa pun di luar kebutuhan proyek 2 hari. Migration berupa SQL polos di `migrations/` (tanpa framework). Frontend memakai Fetch API + React hooks + state sederhana; satu-satunya dependensi adalah `leaflet`, `react-leaflet`, `@types/leaflet`.

## Konvensi Kode

- **Struktur**: `cmd/server` (entry point), `internal/config`, `internal/db`, `internal/entity` (model + validasi), `internal/vehicle`, `internal/auth`, `internal/middleware`, `internal/push` (Web Push/VAPID), `internal/handler` — datar, tanpa layering yang berlebihan.
- **Penamaan**: Konvensi standar Go. Nama package = nama direktori. Tanpa pengulangan (`config.Config`, bukan `config.ConfigConfig`).
- **Dependensi**: Hanya `github.com/gin-gonic/gin`, `github.com/jackc/pgx/v5`, `github.com/joho/godotenv`, plus `github.com/coder/websocket`, `github.com/golang-jwt/jwt/v5`, `golang.org/x/crypto` (bcrypt), dan `github.com/SherClockHolmes/webpush-go` (Web Push/VAPID — satu-satunya dependensi tambahan yang disetujui). Tambah yang lain hanya bila benar-benar dibutuhkan.
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
- Unit test: `go test ./internal/...`; integration: `go test -tags integration ./internal/handler/`.

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
- ✅ Frontend `frontend/` (React + TS + Vite + Leaflet): `VITE_API_BASE_URL`, klien Fetch API, proxy Vite untuk `/api` + `/health`, EntityMap/List/Detail/Form/ConfirmDialog, CRUD penuh tersinkron dengan backend, state loading/empty/error/toast, indikator API 3-state
(checking/online/offline) + auto-retry tiap 15 dtk saat offline, layout responsif — terverifikasi live (Playwright headless, desktop + mobile)
- ✅ "Plan route" end-to-end: tombol Plan route mengisi titik awal dari posisi unit, pilihan titik tujuan langsung mengirim `POST /api/vehicles/:id/route`, polyline digambar di peta (warna tema) dan kamera menyesuaikan ke jalur, card kendaraan menampilkan "Following route to [tujuan]"
- ✅ Multi-stop routing (VRP): `POST /api/vehicles/:id/route` menerima array `destinations` (maks 12). Urutan kunjungan ditentukan server: **Trip service OSRM** (`/trip/v1/driving/`, didukung server publik) mencari tur optimal dan mengembalikan satu polyline lewat semua titik; **fallback** `OptimizeOrder` (greedy nearest neighbour) + route service multi-waypoint; **fallback terakhir** garis lurus. Urutan disimpan di `vehicle_routes.stops` (JSONB), peta menomori titik (1, 2, 3), form "Plan route" punya tombol "+ Add Stop" dan hapus per baris, dan `active_route` di `GET /api/vehicles` ikut membawa `stops`
- ✅ Backend rute: OSRM (`router.project-osrm.org`) dengan fallback garis lurus bila geometri degenerate, penyimpanan polyline di `vehicle_routes` (PostGIS), `GET /api/vehicles` menyertakan `active_route`, pembatalan rute lama + insert baru dalam satu transaksi
- ✅ Jarak tempuh + ETA: `distance`/`duration` dari OSRM disimpan di `vehicle_routes.distance_m`/`duration_s` dan dikembalikan lewat API; card kendaraan (tab Live) menampilkan "Distance X km" + jam tiba (waktu sekarang + sisa waktu tempuh) + progress bar, card unit terpilih menampilkan persen/sisa jarak/ETA/total jarak + lama tempuh
- ✅ Simulator (`cmd/simulator`): membaca rute aktif tiap tick, menyusuri polyline selangkah demi selangkah, menutup rute (`status='completed'`) saat tiba di ujung garis
- ✅ Peta: satu gaya saja — tile raster Google Maps (`mt0–mt3.google.com`, tanpa API key), kredit transparan sampai kursor menyentuh pojok, tema gelap membalik tile via filter CSS
- ✅ Auth lokal: JWT dengan refresh token, empat peran (`admin`/`staff`/`viewer`/`driver`), rate limit login, akun pertama dibuat lewat `POST /api/auth/setup` saat workspace masih kosong
- ✅ Status unit otomatis: unit yang diam lebih dari `SERVER_OFFLINE_AFTER` (bawaan 60 detik) ditandai `offline` oleh sweep tiap 10 detik (`MarkStaleOffline` di `internal/handler/vehicles.go`), dan **bangkit sendiri** begitu melapor lagi karena setiap laporan posisi menulis status + waktu baru; unit yang belum pernah melapor tidak ditandai. Semua tampilan ikut berubah (fleet list, frame WebSocket `vehicle_updated`, hitungan Overview, Driver View) karena statusnya disimpan di database
- ✅ **Deteksi idle & dwell time** (migrasi 027): server sekarang memutuskan **dua** status sendiri — `offline` (diam) dan `idle` (diam tapi masih melapor). Jangkar diam disimpan di `vehicles.stationary_since` + `stationary_anchor_lat/lng`; tiap laporan posisi memperbarui jangkar (jarak > `SERVER_IDLE_RADIUS_M` atau kecepatan > `SERVER_IDLE_MAX_SPEED_KMH` = bergerak, aturan murni `vehicle.ShouldMoveAnchor`), dan laporan yang memindahkan jangkar menutup sesi idle yang terbuka. Sweep `MarkStationaryIdle` (tiap 30 detik) membuka sesi di `idle_events` (paling banyak satu sesi terbuka per unit — logika aplikasi + unique index parsial), menghitung `zone_id` dari jangkar (`ST_Covers`, zona id terkecil kalau beberapa memuatnya) dan/FRAME `vehicle_updated`. **Idle ≠ offline**: offline selalu menang, ambang idle (bawaan 10 menit) selalu lebih besar dari ambang offline (60 detik). `GET /api/idle-events` (riwayat sesi, `?vehicle_id=&from=&to=&limit=`, driver hanya unitnya) + `GET /api/vehicles` sekarang membawa `idle_since`, `idle_duration_s` dan `current_zone` (`{zone_id, zone_name, since, dwell_s}`, dwell = ENTER terakhir yang belum ada EXIT-nya). Card armada menampilkan lencana "Idle 12 mnt" + "Di <zona> · 25 mnt" (jam lokal di komponen, tanpa polling API), Overview menampilkan "idle terlama: <unit> · <durasi>"
- ✅ Server hanya listen di loopback (`SERVER_HOST=127.0.0.1`) + peringatan kalau dibuka ke alamat publik
- ✅ Kompresi HTTP gzip (`internal/middleware`), respons besar menyusut sekitar 3x
- ✅ Zona murah dibaca: `zone_metrics` menyimpan salinan tampilan, luas, keliling, jumlah titik, dan hitungan entitas/kendaraan (tabel terpisah, bukan generated column, supaya counter tidak menulis ulang baris zona)
- ✅ Hitungan zona live: trigger di database + frame `zones` di WebSocket + `GET /api/zones/counts`
- ✅ Armada menyimpan `heading` (arah kompas) dan `route_color` (warna rute; satu warna untuk semua unit — merah primer tema, ditetapkan server)
- ✅ Arah marker = arah kompas yang benar (0 = utara, 90 = timur): simulator memakai `atan2(dLng, dLat)` dan bergerak ke utara/selatan/timur/barat sesuai arah hadapnya, jadi marker trypticus mengikuti garis rute, bukan tegak lurus padanya
- ✅ Batas wilayah dasar ikut repo (migrasi 016: negara + 34 provinsi, disederhanakan); `cmd/import-regions -url` mengunduh dataset COD-AB penuh sendiri
- ✅ Migrasi 015 mengisi 8 landmark; `scripts/empty-state.sql` mengembalikan layar "No entities yet."
- ✅ Playback rute: `GET /api/vehicles/:id/history?date=YYYY-MM-DD` (urut lama → baru, server menyampel titik supaya hari penuh tetap ringan), dialog "Playback history" mengambang di pojok peta (bukan portal atas halaman, supaya peta tetap terlihat), jejak digambar sebagai garis abu-abu transparan, satu ghost marker berjalan di sepanjang jejak (animasi `requestAnimationFrame`, posisi disimpan di ref supaya React tidak render ulang tiap frame), dan kamera live mundur selama playback berjalan
- ✅ Kerangka notifikasi: lonceng di top bar meminta izin lewat `Notification.requestPermission()` (hanya dari user gesture), service worker (kode notifikasi di `frontend/public/sw-notify.js`, diimpor ke worker PWA) menaikkan notifikasi lewat `ServiceWorkerRegistration.showNotification()` dengan fallback `new Notification()`. Notifikasi lintasan zona dan kecepatan dihapus; panel lonceng masih kosong (daftar riwayat notifikasi belum diisi) — isinya cuma status Web Push + tombol tes
- ✅ **Web Push (VAPID) terpasang**: protokol terbuka, tanpa layanan berbayar (tanpa Firebase/Pusher/OneSignal). Migrasi 026 `push_subscriptions` (satu baris per browser, `endpoint` unik, FK ke `users` ON DELETE CASCADE); package `internal/push` (config VAPID dari `.env`, interface `Sender` supaya logika penerima bisa dites dengan fake sender, penghapusan otomatis subscription yang dijawab 404/410); dependensi backend **satu** yang disetujui: `github.com/SherClockHolmes/webpush-go` (tanpa dependensi frontend baru). Endpoint `GET /api/push/vapid-public-key`, `POST/DELETE /api/push/subscribe`, `POST /api/push/test` (grup protected). Pemicu: **berangkat** hanya saat `POST /api/vehicles/:id/route/start` (bukan saat rute dibuat, bukan dari frame `route` ulang), **tiba** hanya saat `completeRouteIfArrived` menutup rute (150 m), **tugas** hanya ke `assigned_user_id` — push dikirim di goroutine terpisah dengan timeout sendiri, jadi kegagalan push tidak pernah menggagalkan request utama. Penerima event rute = admin + staff, kecuali pemicunya. Tanpa pasangan kunci VAPID fiturnya mati dengan rapi (`enabled: false`, server tetap start normal). `cmd/gen-vapid` mencetak pasangan kunci sekali jalan (stdout saja, tidak menyentuh `.env`); kunci privat tidak pernah masuk repo/log/API/README
- ✅ Audit log: tabel `audit_logs` (migrasi 017) + middleware `internal/audit` yang mencatat sendiri setiap `POST`/`PUT`/`DELETE` yang berhasil (tanpa(handler harus ingat) — nama objek diambil dari jawaban handler, atau dibaca lebih dulu untuk penghapusan; laporan posisi kendaraan dikecualikan karena itu telemetri); `GET /api/audit-logs` (admin, bisa filter action/entity_type/q) + tab **Activity Log** di sidebar (admin saja) dengan pencarian, filter aksi, dan pager
- ✅ Driver view: role `driver` login langsung ke `/driver` (mini-router di `lib/route.ts`, tanpa dependensi baru) — peta layar penuh (`DriverMap`: kamera menyesuaikan garis lalu hanya mendorong unit), kartu bawah yang bisa ditutup (unit, trip, perjalanan hari ini), tombol besar "Start trip" yang benar-benar memicu gerakan: `POST /api/vehicles/:id/route/start` (migrasi 018 menambah `vehicle_routes.started_at`) + simulator menunggu sinyal driver untuk unit yang punya driver
- ✅ Task management (work order): tabel `tasks` (migrasi 021: title, description, status `pending`/`in_progress`/`completed`, assigned_user_id, place_id, due_date, created_at) + CRUD `GET/POST/PUT/DELETE /api/tasks` dan `PUT /api/tasks/:id/status` (admin/staff/driver; driver hanya miliknya) + tab **Tasks** di sidebar (`TaskBoard.tsx`: Kanban 3 kolom Pending/In Progress/Done, form Create Task dengan dropdown Assignee (driver/staff) dan Place) + bagian **Your tasks** di DriverView dengan tombol "Mark done" (driver hanya melihat tugasnya sendiri)
- ✅ Laporan operasional: `GET /api/reports` (periode `from`/`to`/`vehicle_id`, maks. 92 hari) dan `GET /api/reports/export.csv` (unduhan CSV per kendaraan) + tab **Reports** di sidebar (kartu ringkasan, tabel per kendaraan & per zona, tombol Export CSV). Semua agregasi di SQL dengan logika jarak yang sama persis dengan `GET /api/overview` (jarak dihitung di database, bukan di browser); driver hanya membaca laporan unitnya sendiri
- ✅ Peran baru `staff` (migrasi 022): orang lapangan yang hanya boleh memindahkan status tugas, bukan menulis data workspace
- ✅ Peringatan kecepatan dihapus total: `internal/handler/telematics.go`, endpoint `/api/telematics-events`, event `OVERSPEED` di live feed, dan tabel `telematics_events` (migrasi 023) dibuang; lonceng notifikasi tetap ada sebagai tempat notifikasi berikutnya (unit berangkat/singgah) tapi tidak lagi menerima notifikasi lintasan zona atau kecepatan
- ✅ **Status per-stop + bukti singgah** (migrasi 028): tiap titik singgah di rute multi-stop adalah *langkah perjalanan* dengan status sendiri, bukan lagi sekadar titik di daftar. Tabel baru `route_stops` (route_id FK CASCADE, seq, name, lat/lng, status `pending`/`arrived`/`completed`/`skipped` + CHECK, arrived_at, completed_at, completed_by FK users, completion_note, completed_lat/lng; unique `(route_id, seq)`, index `(route_id, seq)` dan `(route_id, status)`) jadi sumber kebenaran; JSONB `vehicle_routes.stops` tetap ada untuk kompatibilitas tapi **tidak** dipakai untuk status lagi. Backfill di migrasi yang sama: rute multi-stop → satu baris per elemen JSONB (`jsonb_array_elements WITH ORDINALITY`), rute satu tujuan → satu baris dari `dest_*`; status mengikuti status rute (`completed` → completed dengan `completed_at = ended_at`, `cancelled` → skipped, `active` → pending). Rute yang dibuat menulis baris-baris `pending`-nya dalam transaksi yang sama dengan penyimpanan rute. Deteksi **arrived** performed server dari laporan posisi (`checkStopArrival`, radius 150 m yang sama dengan tiba rute, hanya untuk rute `active` yang sudah `started_at`): stop sasaran selalu stop `pending`/`arrived` dengan `seq` terkecil (alur berurutan), idempoten (tanpa push/frame ganda), push **"Unit singgah"** (`route-stop-arrived-<stop_id>`) ke operator — kecuali **stop terakhir**, yang arrival-nya milik `completeRouteIfArrived` (push "Unit tiba", tanpa push singgah ganda) dan ditutup otomatis jadi `completed` dengan `completion_note = "Otomatis: rute selesai"`. Endpoint baru `POST /api/vehicles/:id/route/stops/:stop_id/complete` dan `.../skip` (body `{note}` opsional, maks. 500 karakter): hanya stop aktif saat ini, `complete` hanya kalau statusnya `arrived`, `skip` boleh dari `pending`/`arrived`; admin untuk unit mana pun, driver hanya unitnya sendiri dan hanya rute yang sudah dimulai, staff/viewer hanya membaca; 409 `conflict` / 404 / 403 sesuai aturan. Penyelesaian stop bukan akhir rute. Audit log mencatatnya sendiri lewat middleware. Driver View menampilkan daftar stop (nomor, nama, chip Menuju/Sampai/Selesai/Dilewati + jam, catatan), tombol besar "Selesaikan stop ini" + kolom catatan + tombol "Lewati" (nonaktif saat offline), dan frame WebSocket `route_stop` memperbarui tampilan tanpa reload; card armada menampilkan "Stop 2 dari 5 · 1 selesai" dan marker peta yang sudah selesai tampil redup + tanda centang
- ✅ PWA offline-first: `vite-plugin-pwa` (satu dependensi baru) mendaftarkan service worker + menulis `manifest.webmanifest` (nama, ikon 192/512 + maskable, warna tema `#d0171e`, `display: standalone`, shortcut `/driver`). Shell aplikasi di-precache Workbox (`navigateFallback: index.html`), jadi `/driver` terbuka tanpa jaringan. **Tile peta Leaflet di-cache runtime** (`CacheFirst`, cache `groundwork-map-tiles`, 800 tile / 30 hari) untuk host Google + OpenStreetMap + host `VITE_MAP_TILES_URL`; API tidak di-cache (jawabannya milik satu akun — salinan offline disimpan di localStorage, bukan di cache bersama worker). Kode notifikasi lama (`frontend/public/sw.js` → `frontend/public/sw-notify.js`) kini **diimpor** ke worker yang dihasilkan (`workbox.importScripts`), jadi aplikasi hanya punya satu service worker (cache + notifikasi). `frontend/src/lib/offline.ts`: `useOnline()` mengikuti `navigator.onLine`, `useDriverSnapshot()` menyalin unit + rute + perjalanan hari ini ke `localStorage` (`groundwork:driver:snapshot`, ditulis throttle + flush saat keluar), dan `/driver` menampilkan badge "Anda sedang Offline. Rute disimpan di memori." saat `navigator.onLine === false` (tombol "Start trip" dinonaktifkan — starting trip butuh server). `npm run preview` (proxy ke backend ikut) adalah build yang dipakai untuk menguji PWA; dev server tidak punya worker. Instruksi uji lengkap (DevTools → Application, simulasi Offline, install) ada di `frontend/PWA.md`
