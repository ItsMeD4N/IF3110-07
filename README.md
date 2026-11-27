# IF3110-07 Database Management Platform

Tugas Besar IF3110 Pengembangan Aplikasi Web 2026 (Milestone 1 & Milestone 2).

Aplikasi web buat ngatur akses ke database PostgreSQL yang dipakai bareng. Admin mengatur user, group (+ leader group), dan koneksi database. User bisa lihat schema dan data, menyusun definisi migration (`up`/`down` atau `irreversible`, mode normal & batch), menjalankan `EXPLAIN`/`EXPLAIN ANALYZE`, mengajukan review approval, menjalankan migration/rollback/forward secara asynchronous lewat queue & worker, serta memantau progres batch dan riwayat lengkap (dengan filter `Hide rollback`).

## Anggota

| Nama                         | NIM      | Bagian                                      |
| ---------------------------- | -------- | ------------------------------------------- |
| Yavie Azka Putra Araly       | 13524077 | Authorization, admin console, bonus EXPLAIN |
| Rainaldi Pratama F Sembiring | 13524117 | Database viewer, migration                  |
| Daniel Putra Rywandi S       | 13524143 | Autentikasi, session, keamanan              |

## Stack

- Backend & Worker: Go 1.25 (`net/http`, `database/sql`, `lib/pq`, `log/slog`, OpenTelemetry)
- Frontend: **Single-Page Application (SPA) responsif**: React 19 + TypeScript + Vite + Tailwind CSS v4 (`client/`)
- Database: PostgreSQL 16 lewat Docker Compose
- Observability (bonus): OpenTelemetry Collector, Tempo (trace), Loki (log), Prometheus (metrik), Grafana (dashboard)

## Cara menjalankan

Butuh Go 1.25+, Node.js 20+, Docker, dan Git.

### Mode Production / Single-Port (`http://localhost:8080`)

Build SPA terlebih dahulu lalu jalankan server Go. Server mendeteksi `client/dist/index.html` lalu men-serve SPA beserta fallback deep-link:

```bash
docker compose up -d
cd client
npm install
npm run build
cd ../server
go run .
```

Buka `http://localhost:8080` lalu login pakai akun admin bawaan: `admin` / `Admin#Password123`.

Tampilan aplikasi hanya lewat SPA. Kalau `client/dist` belum ada, server tetap jalan dalam mode **API-only**: semua halaman non-`/api` dibalas `503` berisi petunjuk build, dan log mencatat event `server.spa_not_built`. Go template M1 tidak pernah dipakai lagi kecuali diminta eksplisit dengan `SERVE_SPA=false` (hanya untuk test legacy).

### Mode Development (Hot Reload SPA)

Jalankan backend Go di terminal 1 (`cd server && go run .`, otomatis API-only kalau SPA belum di-build) dan Vite dev server di terminal 2:

```bash
cd client
npm install
npm run dev
```

Buka `http://localhost:5173`. Seluruh request `/api/*` otomatis di-proxy ke `http://localhost:8080` supaya cookie sesi `HttpOnly` dan `csrf_token` tetap same-origin.

### Data contoh

Skema database platform dibuat otomatis waktu server start. Docker Compose juga menyiapkan dua database contoh, `sample_db` dan `sample_db2` (user dan password `sample_app`), yang bisa ditambahkan sebagai koneksi dari halaman admin.

Kalau mau isi user, group, dan koneksi contoh sekaligus, jalankan dari folder `server`:

```bash
SEED_TARGET_URL="postgres://sample_app:sample_app@localhost:5433/sample_db?sslmode=disable" \
SEED_TARGET_URL_2="postgres://sample_app:sample_app@localhost:5433/sample_db2?sslmode=disable" \
go run ./cmd/devseed
```

Password user contoh (`alice`, `bob`, `carol`) muncul di akhir output. Devseed juga menetapkan leader: `alice` (Analyst, View Only) dan `bob` (Backend Dev, View + Edit), jadi setiap perubahan pada `Sample DB` dan `Sample DB 2` butuh approval keduanya.

> `sample_db2` hanya dibuat otomatis pada volume Postgres yang masih baru. Untuk volume lama, jalankan `docker exec -i if3110_postgres psql -U postgres < docker/sample_db2.sql`.

### Environment variable

| Variabel                      | Default                                                                   | Keterangan                                                                                         |
| ----------------------------- | ------------------------------------------------------------------------- | -------------------------------------------------------------------------------------------------- |
| `PORT`                        | `8080`                                                                    | Port server                                                                                        |
| `DATABASE_URL`                | `postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable` | Database platform                                                                                  |
| `APP_ENV`                     | `development`                                                             | Isi `production` supaya cookie pakai `Secure`                                                      |
| `APP_ENCRYPTION_KEY`          | kunci development                                                         | Kunci enkripsi password koneksi, wajib diisi di production (`openssl rand -base64 32`)             |
| `EMBEDDED_WORKER`             | `true`                                                                    | Jalankan worker queue di dalam proses server (`false` jika menjalankan `cmd/worker`)               |
| `WORKER_CONCURRENCY`          | `4`                                                                       | Jumlah goroutine worker paralel                                                                    |
| `REQUIRE_APPROVAL`            | `true`                                                                    | Kewajiban approval leader setiap group sebelum enqueue migration (`false` = informasional)         |
| `LEGACY_SYNC_MIGRATION`       | `false`                                                                   | Endpoint eksekusi sinkron M1. Default mati, sehingga **semua** eksekusi wajib lewat queue          |
| `SPA_DIST_DIR`                | `../client/dist`                                                          | Direktori hasil build SPA yang di-serve server Go                                                  |
| `SERVE_SPA`                   | otomatis                                                                  | `false` hanya untuk test legacy Go template M1                                                     |
| `LOG_LEVEL`                   | `info`                                                                    | `debug`, `info`, `warn`, `error`                                                                   |
| `LOG_FILE`                    | kosong                                                                    | Selain ke stdout, tulis log JSON ke file ini (dipakai OpenTelemetry Collector, contoh `../logs/server.log`) |
| `OTEL_TRACES_EXPORTER`        | `stdout`                                                                  | `stdout` (console exporter), `otlp` (ke Collector), atau `noop`                                    |
| `OTEL_EXPORTER_OTLP_ENDPOINT` | `localhost:4318`                                                          | Endpoint OTLP/HTTP Collector                                                                       |
| `OTEL_SERVICE_NAME`           | `if3110-db-platform`                                                      | Nama service pada trace (worker terpisah memakai akhiran `-worker`)                                |
| `METRICS_ENABLED`             | `true`                                                                    | Ekspos metrik Prometheus di `GET /metrics`                                                         |
| `WORKER_METRICS_ADDR`         | `:9091`                                                                   | Alamat endpoint `/metrics` untuk proses `cmd/worker`                                               |

## Session

| Pengaturan           | Nilai                                                 |
| -------------------- | ----------------------------------------------------- |
| Idle timeout         | 30 menit                                              |
| Absolute expiration  | 12 jam sejak login                                    |
| Rotasi session ID    | Tiap 15 menit, saat login, dan saat hak akses berubah |
| Grace period ID lama | 60 detik                                              |

Beberapa catatan:

- Rotasi tidak memperpanjang absolute expiration.
- Session ID berupa token acak 32 byte, dan yang disimpan di database cuma hash SHA-256-nya.
- Cookie pakai `HttpOnly` dan `SameSite=Lax`, plus `Secure` di production.
- Password user di-hash pakai bcrypt, sedangkan password koneksi database dienkripsi pakai NaCl secretbox.
- Request yang mengubah data harus bawa token CSRF (double submit cookie). Token ini dibuat ulang tiap kali login.
- Logout dan penonaktifan user langsung mematikan session di backend.

## Hak akses

Akses database selalu lewat group, tidak pernah langsung ke user. Platform Admin mengurus user, group, koneksi, dan pemberian akses, tapi tidak otomatis punya akses ke database mana pun.

- **View Only**: lihat schema, data, riwayat migration, dan draft. Bisa juga `EXPLAIN` tanpa `ANALYZE`, yang dijalankan di transaksi read-only.
- **View + Edit**: semua akses View Only, ditambah bikin, ubah, dan jalankan migration, serta `EXPLAIN ANALYZE`.

Kalau satu user ada di beberapa group yang punya akses ke database yang sama, yang dipakai level paling tinggi. Misalnya user ada di group A (View Only) dan group B (View + Edit) untuk database X, maka aksesnya ke X jadi View + Edit. Kodenya ada di `server/internal/authz/effective.go`.

Hal lain yang perlu diketahui:

- Kalau anggota group atau akses group berubah, session user yang kena dirotasi di request berikutnya.
- Kalau user dinonaktifkan atau status admin-nya diubah, semua session-nya langsung dicabut.
- Database yang tidak ada dan database yang tidak boleh diakses sama-sama dibalas 404.

## Migration

### Definisi, rollback, dan forward

- Setiap definisi migration punya `sql_up` dan `sql_down`, atau ditandai `irreversible` beserta alasannya (dijaga `CHECK` constraint di database).
- Begitu pernah dieksekusi, definisi dikunci (`is_locked_immutable`) dan trigger `enforce_platform_migration_immutability` menolak perubahan SQL. Perbaikan dilakukan dengan migration baru.
- Rollback hanya bisa untuk migration aktif terakhir (untuk A → B → C, C harus di-rollback dulu sebelum B) dan memakai `sql_down` asli dari revisi yang dieksekusi. Forward menjalankan `sql_up` lagi setelah rollback, mengikuti urutan linear, dan tidak otomatis menjalankan migration berikutnya.
- `BEGIN`/`COMMIT` di dalam SQL ditolak, karena transaksinya sudah diatur platform.

### Riwayat eksekusi

- Setiap attempt (berhasil maupun gagal, termasuk rollback, forward, retry, dan attempt yang gagal validasi) dicatat di `platform_migration_executions`: migration ID, execution ID, versi & revisi, actor, waktu, status, SQLSTATE, `trace_id`, dan **salinan SQL yang benar-benar dijalankan**.
- Tabel riwayat bersifat append-only: trigger `enforce_execution_history_append_only` menolak penimpaan kolom inti. Rollback yang berhasil menyimpan `reverted_execution_id` ke execution yang dibatalkannya.
- Checkbox **Hide rollback** di halaman riwayat menyembunyikan pasangan eksekusi yang dibatalkan dan rollback sukses yang membatalkannya. Pasangan ditentukan dari execution ID (`FilterHideRollback`), sedangkan attempt gagal dan rollback gagal tetap tampil.

### Eksekusi asynchronous

- Semua eksekusi (`up`, `batch_up`, `rollback`, `forward`, `batch_resume`, `batch_revert`) dipublikasikan sebagai job ke queue `platform_migration_jobs`. API langsung membalas `202` berisi `job_id`, lalu SPA memantau statusnya.
- Endpoint sinkron M1 (`POST /api/databases/{id}/migrations` dan `.../migration-drafts/{id}/execute`) dibalas `403 approval_required` saat approval wajib, atau `403 queue_required` saat `LEGACY_SYNC_MIGRATION=false` (default).
- Worker mengklaim job dengan lease (`locked_by`, `lease_expires_at`) dan memperbarui lease lewat heartbeat. `idempotency_key` mencegah job ganda, dan receipt di database target mencegah double commit kalau pesan yang sama terkirim dua kali.
- Kegagalan sementara di-retry dengan exponential backoff + jitter, maksimal `max_attempts` (default 3).
- Job dan checkpoint disimpan di database, jadi tetap ada setelah server/worker restart.

### Mode batch

- Definisi batch memakai `batch_config` (`schema`, `table`, `cursor_column`, `batch_size`, `where_clause`, `capture_columns`, `batch_delay_ms`) dan placeholder `{{cursor_start}}`, `{{cursor_end}}`, `{{last_cursor}}`, `{{batch_ids}}`, atau `{{recovery_json}}`.
- Worker memproses data dengan cursor (`WHERE id > last_id ORDER BY id LIMIT batch_size`) tanpa memuat seluruh dataset ke memori. Cakupan target (`scope_max_cursor`) ditentukan sejak awal.
- Setiap batch berjalan di transaction sendiri. Receipt dan checkpoint batch (execution, nomor batch, cursor, row ID, data pemulihan) ditulis ke schema `_if3110_meta` di database target **dalam transaction yang sama** dengan perubahan batch, lalu disalin ke `platform_migration_batch_checkpoints`.
- Kalau satu batch gagal, hanya batch itu yang di-rollback. Execution menjadi `partial`, SPA menampilkan jumlah batch/row yang sudah committed, dan database ditahan (`blocked_partial`) sampai di-resume atau di-revert.
- **Resume** melanjutkan dari checkpoint terakhir tanpa mengulang batch yang sudah committed. **Revert** menjalankan `sql_down` per batch dengan urutan terbalik. Kalau revert gagal di tengah, statusnya `partially_reverted` dan bisa dilanjutkan. Keduanya tetap butuh permission, validasi, dan approval.

### Lock dan pemulihan saat worker gagal

Satu database hanya boleh menjalankan satu migration, sedangkan database berbeda bisa diproses paralel. Ada dua lapis lock:

1. **Lease lock di database platform** (`platform_db_locks`, satu baris per database): berisi `lock_token`, `fencing_version`, `owner_id`, dan `expires_at` dengan TTL 30 detik, diperpanjang heartbeat tiap 10 detik selama job berjalan, termasuk di antara commit batch. Lock ini berlaku lintas proses worker.
2. **Advisory lock di database target** (`pg_try_advisory_lock`), dipegang di koneksi yang mengeksekusi SQL.

Sebelum setiap commit, worker memanggil `Verify(lease)`. Kalau lease sudah hilang atau diambil worker lain (fencing version berubah), transaction di-rollback dan job dijadwalkan ulang. Job yang menemukan lock sedang dipegang worker lain dikembalikan ke queue (`worker.job_contended`) tanpa menghabiskan jatah retry.

Kalau worker mati (crash, kill, atau mesin mati):

1. Heartbeat berhenti, sehingga lease lock dan lease job kedaluwarsa paling lama dalam 30 detik. Advisory lock di database target ikut terlepas karena koneksi worker terputus.
2. Reclaimer di worker lain (tiap 3 detik) menemukan job `running` yang lease-nya kedaluwarsa, lalu memanggil `RecoverExpiredJob`. Proses ini mengambil alih lock dengan `fencing_version` baru, jadi worker lama yang ternyata masih hidup tidak bisa commit lagi.
3. Recovery memeriksa **bukti eksekusi** di database target sebelum memutuskan:
   - receipt untuk `idempotency_key` job ada → perubahan sudah ter-commit, execution ditandai `success`;
   - job batch dengan checkpoint committed → execution `partial`, database ditahan `blocked_partial` sampai di-resume atau di-revert;
   - tidak ada bukti commit → execution `failed`, job di-retry dengan backoff (atau `failed` kalau jatah retry habis);
   - database target tidak bisa dihubungi untuk verifikasi → execution `uncertain`, database ditahan `blocked_uncertain`.
4. Kalau commit gagal di tengah jalan dengan error yang hasilnya tidak pasti (koneksi putus saat `COMMIT`), engine langsung mengecek receipt. Kalau tetap tidak bisa dipastikan, status menjadi `uncertain`.
5. Selama database `blocked_partial` atau `blocked_uncertain`, job lain untuk database itu ditolak `409 database_blocked`. User View + Edit bisa menjalankan `POST .../executions/{id}/reconcile` untuk mengulang pengecekan bukti setelah database target bisa dihubungi lagi.

### Dataset uji (lebih dari 1 detik tanpa batching)

Dataset dibuat dan diukur dengan `cmd/benchdataset`. Tool ini mengisi tabel `public.bench_events` (default 1.000.000 row) memakai `generate_series` di sisi server, lalu menjalankan `UPDATE` seluruh row tanpa batching di dalam transaction yang di-rollback (dataset tidak berubah). Pengukuran diulang 3 kali.

```bash
cd server
go run ./cmd/benchdataset -rows 1000000
```

Kalau UPDATE tercepat masih di bawah 1 detik, tool memberi peringatan. Naikkan `-rows` (misalnya `-rows 3000000 -reset`).

Hasil pengukuran di lingkungan pengujian (PostgreSQL 16 di Docker, mesin lokal). UPDATE satu statement atas 1.000.000 row butuh sekitar 5 detik, jauh di atas batas 1 detik, sehingga dataset ini layak untuk membandingkan eksekusi tanpa batch dengan eksekusi per batch:

<!-- BENCHMARK_RESULT -->
| Tanggal | PostgreSQL | Tabel | Jumlah row | Query | Run | Min | Rata-rata | Max |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| 2026-11-27 09:32 | 16.15 | `public.bench_events` | 1000000 | `UPDATE` seluruh row tanpa batching (di-rollback) | 3 | 5.057s | 5.184s | 5.399s |
<!-- /BENCHMARK_RESULT -->

Dataset yang sama dipakai untuk menguji migration per batch. Daftarkan `sample_db` sebagai koneksi, lalu buat definisi batch:

- `batch_config`: schema `public`, table `bench_events`, cursor column `id`, batch size `50000`
- `sql_up`: `UPDATE public.bench_events SET processed = TRUE, updated_at = now() WHERE id >= {{cursor_start}} AND id <= {{cursor_end}};`
- `sql_down`: `UPDATE public.bench_events SET processed = FALSE, updated_at = now() WHERE id >= {{cursor_start}} AND id <= {{cursor_end}};`

Test integrasi `TestEngine_BatchMigration_LargeDatasetOverOneSecond` menjalankan skenario yang sama secara otomatis (UPDATE tanpa batch > 1 detik, lalu batch migration pada data yang sama) dan mencetak jumlah row serta durasinya di output `go test -v`.

## Workflow & Approval (Milestone 2)

Alur: `Draft → Pending Review → Approved → Pending (Queue) → Running → Success / Failed`.

- Setiap group punya **leader** (ditetapkan admin lewat `PUT /api/admin/groups/{id}/leader`; leader wajib anggota aktif). Kalau leader dikeluarkan dari group, leader otomatis kosong.
- Setiap perubahan database (`up`/`batch_up`, `rollback`, `forward`, `batch_resume`, `batch_revert`) diajukan lewat `POST .../definitions/{id}/approval-requests` dan wajib di-approve **leader setiap group** yang punya akses ke database itu, baik View Only maupun View + Edit. Kalau ada group tanpa leader, pengajuan ditolak (`group_leader_missing`).
- Satu leader yang memimpin beberapa group cukup approve sekali. Satu reject (wajib alasan) langsung menolak pengajuan.
- Approval terikat ke revisi definisi. Kalau definisi diedit, approval lama jadi `superseded` dan workflow kembali ke `draft`.
- Approval dipakai sekali: saat enqueue, approval di-*consume* secara atomik (aman terhadap request paralel) dan diikat ke `idempotency_key` job. Rollback/forward berikutnya butuh approval baru. Worker merevalidasi approval di bawah lock.
- Semua kejadian approval dicatat sebagai log JSON (`approval.requested`, `approval.decided`, `approval.consumed`, `approval.superseded`) lengkap dengan `trace_id`, `actor`, dan `database_id`.

Riwayat (Data History):

- `GET /api/databases/{id}/migrations/executions` mendukung filter `migration_id`, `status`, `action`, `actor`, `since`, `until`, `limit`, `offset`, plus `summary`. Filter diterapkan setelah `hide_rollback` sehingga pasangan eksekusi–rollback tetap ditentukan dari execution ID.
- `GET /api/databases/{id}/migrations/definitions/{mid}/history` menampilkan timeline satu migration: definisi, semua attempt (termasuk gagal), dan semua pengajuan approval.

## Logging dan tracing

### Structured log

Log ditulis sebagai JSON lewat `log/slog` ke stdout (dan ke `LOG_FILE` kalau diisi). Log tidak disimpan di database platform. Setiap baris memuat `time`, `level`, `event`, `actor`, `database_id`, `status`, `correlation_id`, serta `trace_id`/`span_id` kalau ada span aktif.

Setiap eksekusi SQL, termasuk query read-only dari viewer, batch, retry, dan kegagalan, menghasilkan event terpisah: `sql.start`, `sql.result` (query, parameter, durasi, `rows_affected`, `sqlstate`, error), lalu `sql.commit` atau `sql.rollback`. Untuk migration ditambah `migration_id`, `job_id`, `execution_id`, `attempt`, dan `batch_number`. Credential, session ID, token, dan nilai yang tampak seperti password disamarkan oleh `telemetry/mask.go` sebelum ditulis.

Contoh format satu eksekusi migration normal (dipersingkat, ID dan waktu ilustratif):

```json
{"time":"2026-11-27T05:12:44.101Z","level":"INFO","msg":"queue.enqueued","event":"queue.enqueued","job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","database_id":1,"migration_id":12,"action":"up","deduplicated":false,"status":"pending","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"00f067aa0ba902b7","correlation_id":"c0a8012e-7d1f-4f0a-9b2c-6e5d4c3b2a19","actor":"bob"}
{"time":"2026-11-27T05:12:44.152Z","level":"INFO","msg":"worker.job_claimed","event":"worker.job_claimed","worker_id":"laptop-3fa9c1-2","job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","migration_id":12,"action":"up","attempt":1,"status":"running","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"a3ce929d0e0e4736","correlation_id":"c0a8012e-7d1f-4f0a-9b2c-6e5d4c3b2a19","actor":"bob","database_id":1}
{"time":"2026-11-27T05:12:44.203Z","level":"INFO","msg":"sql.start","event":"sql.start","query":"CREATE INDEX orders_status_idx ON orders (status)","migration_id":12,"execution_id":41,"job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","attempt":1,"action":"up","status":"started","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"53995c3f42cd8ad8","correlation_id":"c0a8012e-7d1f-4f0a-9b2c-6e5d4c3b2a19","actor":"bob","database_id":1}
{"time":"2026-11-27T05:12:44.217Z","level":"INFO","msg":"sql.result","event":"sql.result","query":"CREATE INDEX orders_status_idx ON orders (status)","migration_id":12,"execution_id":41,"job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","attempt":1,"action":"up","duration_ms":13.8,"rows_affected":0,"status":"success","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"53995c3f42cd8ad8","correlation_id":"c0a8012e-7d1f-4f0a-9b2c-6e5d4c3b2a19","actor":"bob","database_id":1}
{"time":"2026-11-27T05:12:44.221Z","level":"INFO","msg":"sql.commit","event":"sql.commit","query":"COMMIT","migration_id":12,"execution_id":41,"job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","attempt":1,"action":"up","duration_ms":2.9,"status":"success","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"8ad853995c3f42cd","correlation_id":"c0a8012e-7d1f-4f0a-9b2c-6e5d4c3b2a19","actor":"bob","database_id":1}
```

Untuk mengambil contoh asli dari mesin sendiri: jalankan server dengan `LOG_FILE=../logs/server.log`, eksekusi satu migration, lalu `grep '"job_id":"<id>"' logs/server.log`.

### Tracing OpenTelemetry

Trace menyambung dari request API sampai query database:

```
HTTP POST /api/databases/1/migrations/definitions/12/execute   (server)
└── queue.publish                                              (producer, trace context disimpan di kolom trace_context job)
    └── worker.process_job                                     (consumer, trace context di-extract dari job)
        ├── lock.acquire
        ├── migration.revalidate
        ├── migration.execute_normal / migration.batch_execute / migration.batch_revert
        │   ├── db.exec / db.query
        │   └── db.commit / db.rollback
        └── lock.release
```

Setiap span mencatat durasi, atribut (`db.database_id`, `migration.id`, `execution.id`, `job.id`, `migration.attempt`, `migration.batch_number`, `db.statement` yang sudah disamarkan, `db.sqlstate`), serta error dan status `Error` kalau gagal. Dengan `OTEL_TRACES_EXPORTER=stdout` (default), span dicetak oleh console exporter. Contoh satu span (dipersingkat):

```json
{
  "Name": "db.exec",
  "SpanContext": {"TraceID": "4bf92f3577b34da6a3ce929d0e0e4736", "SpanID": "53995c3f42cd8ad8", "TraceFlags": "01"},
  "Parent": {"TraceID": "4bf92f3577b34da6a3ce929d0e0e4736", "SpanID": "c3f42cd8ad853995"},
  "SpanKind": 3,
  "StartTime": "2026-11-27T05:12:44.203Z",
  "EndTime": "2026-11-27T05:12:44.217Z",
  "Attributes": [
    {"Key": "db.system", "Value": {"Type": "STRING", "Value": "postgresql"}},
    {"Key": "db.statement", "Value": {"Type": "STRING", "Value": "CREATE INDEX orders_status_idx ON orders (status)"}},
    {"Key": "db.database_id", "Value": {"Type": "INT64", "Value": 1}},
    {"Key": "migration.id", "Value": {"Type": "INT64", "Value": 12}},
    {"Key": "execution.id", "Value": {"Type": "INT64", "Value": 41}},
    {"Key": "migration.action", "Value": {"Type": "STRING", "Value": "up"}},
    {"Key": "db.duration_ms", "Value": {"Type": "FLOAT64", "Value": 13.8}}
  ],
  "Status": {"Code": "Unset", "Description": ""},
  "Resource": [{"Key": "service.name", "Value": {"Type": "STRING", "Value": "if3110-db-platform"}}]
}
```

`trace_id` yang sama muncul di log (`"trace_id":"4bf92f…"`), di kolom `trace_id` riwayat eksekusi, dan di header response `X-Trace-ID`, jadi satu eksekusi bisa ditelusuri dari SPA sampai query.

## Bonus: Observability (Collector, Prometheus, Grafana)

Endpoint `GET /metrics` (format Prometheus, tanpa dependency tambahan) mengekspos:

| Metrik | Jenis | Label |
| --- | --- | --- |
| `if3110_migration_jobs` | gauge, jumlah job per status (`pending`, `running`, `retrying`, `completed`, `failed`, `partial`, `partially_reverted`, `uncertain`, `cancelled`) | `status` |
| `if3110_migration_jobs_enqueued_total`, `if3110_migration_jobs_finished_total` | counter | `action`, `status` |
| `if3110_migration_job_retries_total`, `if3110_migration_batch_retries_total`, `if3110_migration_lock_contended_total` | counter | `action` |
| `if3110_migration_queue_wait_seconds` | histogram, sejak job siap sampai diklaim worker | `action` |
| `if3110_migration_lock_wait_seconds` | histogram, waktu memperoleh lock database | `result` |
| `if3110_migration_job_duration_seconds`, `if3110_migration_job_end_to_end_seconds` | histogram | `action`, `status` |
| `if3110_migration_batches_processed_total`, `if3110_migration_rows_processed_total` | counter | `action` |
| `if3110_worker_jobs_in_flight` | gauge | `action` |
| `if3110_db_pool_active_connections`, `..._idle_connections`, `..._open_connections`, `..._max_open_connections` | gauge | `pool` (`platform`, `target`) |
| `if3110_db_pool_wait_count_total`, `if3110_db_pool_wait_duration_seconds_total` | counter, waktu tunggu koneksi | `pool` |
| `if3110_http_requests_total`, `if3110_http_request_duration_seconds` | counter, histogram | `method`, `route`, `code` |

Semua label punya himpunan nilai terbatas: action dan status yang tidak dikenal dipetakan ke `other`, dan route dikelompokkan per prefix (`databases`, `databases_enqueue`, `admin`, …). User ID, job ID, dan SQL tidak pernah dijadikan label.

Menjalankan stack observability:

```bash
docker compose --profile observability up -d
cd server
OTEL_TRACES_EXPORTER=otlp LOG_FILE=../logs/server.log go run .
```

| Komponen | URL | Fungsi |
| --- | --- | --- |
| OpenTelemetry Collector | `localhost:4317` (gRPC), `localhost:4318` (HTTP) | Menerima trace via OTLP, membaca log JSON dari `logs/*.log` (filelog receiver, `trace_id`/`span_id` di-parse ke trace context log), meneruskan trace ke Tempo dan log ke Loki |
| Tempo | `http://localhost:3200` | Penyimpanan trace |
| Loki | `http://localhost:3100` | Penyimpanan log |
| Prometheus | `http://localhost:9090` | Scrape `host.docker.internal:8080/metrics` dan `:9091/metrics` (worker) tiap 5 detik |
| Grafana | `http://localhost:3000` | Dashboard **IF3110 - Queue, Migration & DB Pool** (folder IF3110) |

Konfigurasi ada di `observability/` (`otel-collector.yaml`, `tempo.yaml`, `loki.yaml`, `prometheus.yml`), dan provisioning Grafana (datasource + dashboard JSON) di `observability/grafana/`, jadi bisa dijalankan ulang tanpa setup manual.

Korelasi log, trace, dan metrik di Grafana:

- Di panel log (Loki), field `trace_id` menjadi tautan **Buka trace di Tempo**.
- Di Tempo, tombol **Logs for this span** menjalankan query Loki `{service_name="if3110-db-platform"} | trace_id="<trace>"`.
- Dari span di Tempo tersedia tautan ke metrik durasi job p95 di Prometheus.

## Bonus: Stress testing dengan k6

Script: [`loadtest/k6/stress.js`](loadtest/k6/stress.js). Script menjalankan dua skenario dengan timeline tahap beban yang sama:

| Skenario | Isi | VU per tahap (default) |
| --- | --- | --- |
| `api_read` | Login sekali per VU, lalu `GET /api/databases`, schema, riwayat eksekusi, daftar job, dan status lock | warmup → normal 10 → x2 20 → x5 50 → x10 100 → recovery 10 → 0 |
| `migration_jobs` | Buat definisi (`CREATE TABLE` + `INSERT` 5.000 row), ajukan review, approve oleh semua leader, `execute`, lalu polling `GET /jobs/{id}` sampai status terminal | 2 → 4 → 10 → 20 → 2 → 0 |

Beban normal yang ditetapkan tim adalah 10 VU baca + 2 VU pengirim migration. Setiap tahap naik selama 30 detik lalu ditahan 1 menit, kemudian beban diturunkan kembali ke level normal untuk mengamati pemulihan. Job diarahkan bergantian ke database di `DB_IDS`, sehingga ada job bersamaan di database yang sama maupun berbeda.

Respons `202` dari `execute` tidak dihitung sebagai keberhasilan. Keberhasilan diukur dari job yang benar-benar mencapai `completed` (`migration_job_success`), lengkap dengan waktu tunggu queue (`started_at - created_at`) dan durasi penyelesaian.

Threshold:

| Metrik | Threshold |
| --- | --- |
| `http_req_failed{kind:api}` | `rate < 1%` |
| `http_req_duration{kind:api}` | `p(95) < 800 ms` |
| `http_req_duration{kind:api,stage:normal}` | `p(95) < 300 ms` |
| `http_req_failed{kind:submit}` | `rate < 5%` |
| `migration_job_success` | `rate > 95%` |
| `migration_job_completion_seconds` | `p(95) < 120 s` |

Menjalankan (dari root repo, setelah `devseed` dengan dua target dan server sudah jalan):

```bash
k6 run loadtest/k6/stress.js
```

Saat setup, script login sebagai `EDITOR_USER` lalu memakai semua database yang ia akses dengan level View + Edit (dicetak di awal output, misalnya `database target: 3=Sample DB, 4=Sample DB 2`). `-e DB_IDS=3,4` bisa dipakai untuk membatasi target; ID yang tidak bisa diakses membuat test langsung berhenti dengan daftar database yang tersedia.

Variabel lain: `BASE_URL`, `DB_IDS`, `NORMAL_VUS`, `NORMAL_JOB_VUS`, `HOLD`, `RAMP`, `EDITOR_USER`, `LEADERS`, `SEED_PASSWORD`, `JOB_TIMEOUT_S`. Ringkasan per tahap beban (RPS, p50/p95, error rate, job selesai per detik, queue wait p95, job sukses) dicetak ke stdout dan disimpan di `loadtest/k6/results/summary-*.md` dan `.json`. Jalankan bersama stack observability untuk melihat queue, lock, dan pool di Grafana selama pengujian.

Hasil:

<!-- K6_RESULT -->
| Tingkat beban | API req | API RPS | API p50 (ms) | API p95 (ms) | API error rate | Job selesai | Job/detik | Queue wait p95 (s) | Job selesai p95 (s) | Job sukses |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| _belum dijalankan_ | | | | | | | | | | |
<!-- /K6_RESULT -->

Analisis per tingkat beban:

| Tingkat beban | Analisis |
| --- | --- |
| normal | _isi setelah run_ |
| stress_x2 | _isi setelah run_ |
| stress_x5 | _isi setelah run_ |
| stress_x10 | _isi setelah run_ |
| recovery | _isi setelah run_ |

Hal yang perlu dibandingkan antar tahap: kapan p95 API mulai melewati threshold, apakah error rate naik, apakah `if3110_migration_queue_wait_seconds` bertambah karena worker (`WORKER_CONCURRENCY`) dan lock per database menjadi bottleneck, apakah `if3110_db_pool_wait_duration_seconds_total` naik (pool koneksi habis), dan berapa lama queue kembali kosong saat tahap recovery.

## Bonus: SAST dengan CodeQL

Workflow CodeQL menganalisis kode Go (`server/`, build manual `go build ./...`) dan TypeScript (`client/src`) dengan query suite `security-extended` dan `security-and-quality`. Workflow berjalan setiap push/PR ke `main`, terjadwal tiap Senin, dan bisa dijalankan manual. `client/dist`, `client/static`, `loadtest/`, dan file test Go tidak ikut dianalisis.

Sumber workflow ada di `security/github/` dan perlu disalin ke `.github/` sebelum push:

```bash
mkdir -p .github/workflows .github/codeql
cp security/github/workflows/codeql.yml .github/workflows/
cp security/github/codeql/codeql-config.yml .github/codeql/
```

Hasil scan dilihat di **Security → Code scanning**. Untuk repository private, CodeQL butuh GitHub Code Security.

| Scan | Tanggal | Commit | Jumlah temuan |
| --- | --- | --- | --- |
| Awal | | | |
| Setelah perbaikan | | | |

| # | Rule ID | Severity | Lokasi | Dampak | Keputusan | Alasan |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | | | | | | |

Keputusan setiap temuan: diperbaiki, false positive, atau risiko diterima. Eksekusi SQL dari definisi migration (`go/sql-injection` di engine migration dan `EXPLAIN`) adalah fungsi utama platform. Eksekusinya dibatasi ke user View + Edit, wajib approval leader setiap group, dan hanya berjalan di database target, sehingga dicatat sebagai risiko diterima.

## Testing

Unit test dan integration test butuh PostgreSQL yang sudah jalan (`docker compose up -d`):

```bash
cd server
go test -p 1 ./...
```

Di Windows, kalau build-nya sering kehabisan memori, pindahkan folder temp ke drive lain dulu, misalnya `$env:TMP='D:\tmp'; $env:GOCACHE='D:\tmp\gocache'`.

Ada juga smoketest end-to-end yang dites ke server yang lagi jalan. Jalankan `devseed` dulu sebelum menjalankannya:

```bash
go run ./cmd/smoketest
```

Smoketest memakai endpoint migration sinkron M1, jadi jalankan server dengan `REQUIRE_APPROVAL=false LEGACY_SYNC_MIGRATION=true` saat menjalankan smoketest. Smoketest ini mengecek alur dari login sampai migration dan viewer. Selain itu juga mengecek beberapa kasus yang harus gagal: password salah, token CSRF kosong atau palsu, user biasa buka endpoint admin, cookie session palsu, rollback migration yang error, dan EXPLAIN untuk DDL.

## Struktur folder

```
client/
  src/             SPA React + TypeScript + Vite + Tailwind (pages, components, services, context)
  dist/            hasil build SPA produksi (di-serve langsung oleh server Go)
  templates/       template legacy M1 (hanya untuk test legacy, SERVE_SPA=false)
server/
  cmd/             worker, smoketest, devseed, dan benchdataset
  internal/
    auth, session, csrf, user     login, session, CSRF, kelola user
    authz, admin, connmgr,        hak akses, group, koneksi database,
    secretbox, store              enkripsi password koneksi
    dbmanager                     viewer schema dan data
    migration, queue, dblock      engine migration, batching, async queue, lock, filter riwayat
    approval                      workflow & multi-group leader approval
    telemetry                     structured JSON logging (slog) & OpenTelemetry tracing
    metrics                       endpoint Prometheus /metrics
    web, httpx                    SPA static/fallback handler dan helper HTTP
  migrations/      skema database platform
docker/            data database contoh
observability/     konfigurasi OpenTelemetry Collector, Tempo, Loki, Prometheus, Grafana
loadtest/k6/       script dan hasil stress test k6
security/github/   sumber workflow CodeQL (salin ke .github/)
```
