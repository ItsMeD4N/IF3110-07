# IF3110-07 Database Management Platform

Tugas Besar IF3110 Pengembangan Aplikasi Web 2026 (Milestone 1 & 2).

Platform web buat ngatur akses ke database PostgreSQL yang dipakai bareng. Admin ngatur user, group, leader group, dan koneksi database. User bisa lihat schema dan data, bikin migration, minta approval ke leader, terus jalanin migration, rollback, atau forward lewat queue dan worker.

## Anggota

| Nama                         | NIM      | Bagian                                      |
| ---------------------------- | -------- | ------------------------------------------- |
| Yavie Azka Putra Araly       | 13524077 | Authorization, admin console, bonus EXPLAIN |
| Rainaldi Pratama F Sembiring | 13524117 | Database viewer, migration                  |
| Daniel Putra Rywandi S       | 13524143 | Autentikasi, session, keamanan              |

## Stack

- Backend dan worker: Go 1.25 (`net/http`, `database/sql`, `lib/pq`, `log/slog`, OpenTelemetry)
- Frontend: SPA React 19 + TypeScript + Vite + Tailwind
- Database: PostgreSQL 16 (Docker Compose)

## Cara jalanin

Butuh Go 1.25+, Node.js 20+, dan Docker.

```bash
docker compose up -d
cd client && npm install && npm run build
cd ../server && go run .
```

Buka `http://localhost:8080`, login pakai `admin` / `Admin#Password123`.

Kalau `client/dist` belum di-build, server tetap jalan tapi cuma API-nya aja. Buat development, jalanin `go run .` di `server` dan `npm run dev` di `client`, lalu buka `http://localhost:5173`.

Mau langsung ada user, group, dan dua koneksi contoh? Jalanin ini dari folder `server`:

```bash
SEED_TARGET_URL="postgres://sample_app:sample_app@localhost:5433/sample_db?sslmode=disable" \
SEED_TARGET_URL_2="postgres://sample_app:sample_app@localhost:5433/sample_db2?sslmode=disable" \
go run ./cmd/devseed
```

User contohnya `alice`, `bob`, `carol` dengan password `Password#123`. Alice leader group Analyst (View Only), Bob leader group Backend Dev (View + Edit).

Environment variable yang penting:

| Variabel                | Default                                                                   | Fungsi                                          |
| ----------------------- | ------------------------------------------------------------------------- | ----------------------------------------------- |
| `DATABASE_URL`          | `postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable` | Database platform                               |
| `APP_ENV`               | `development`                                                             | Isi `production` biar cookie pakai `Secure`     |
| `APP_ENCRYPTION_KEY`    | kunci dev                                                                 | Wajib di production (`openssl rand -base64 32`) |
| `EMBEDDED_WORKER`       | `true`                                                                    | `false` kalau worker dijalanin terpisah         |
| `WORKER_CONCURRENCY`    | `4`                                                                       | Jumlah worker paralel                           |
| `REQUIRE_APPROVAL`      | `true`                                                                    | Wajib approval leader sebelum eksekusi          |
| `LEGACY_SYNC_MIGRATION` | `false`                                                                   | Endpoint eksekusi sinkron M1, cuma buat smoketest |
| `LOG_FILE`              | kosong                                                                    | Tulis log JSON ke file juga                     |
| `OTEL_TRACES_EXPORTER`  | `stdout`                                                                  | `stdout`, `otlp`, atau `noop`                   |

## Session

| Pengaturan          | Nilai                                    |
| ------------------- | ---------------------------------------- |
| Idle timeout        | 30 menit                                 |
| Absolute expiration | 12 jam sejak login                       |
| Rotasi session ID   | Tiap 15 menit, saat login, saat akses berubah |
| Grace period ID lama | 60 detik                                |

Session ID itu token acak 32 byte, yang disimpan di database cuma hash-nya. Cookie pakai `HttpOnly`, `SameSite=Lax`, dan `Secure` di production. Rotasi nggak memperpanjang absolute expiration. Password di-hash pakai bcrypt, password koneksi database dienkripsi. Request yang ngubah data wajib bawa token CSRF. Logout atau user dinonaktifkan langsung matiin session di backend.

## Hak akses

Akses ke database selalu lewat group. Admin ngatur user, group, dan koneksi, tapi nggak otomatis punya akses ke database mana pun.

- View Only: lihat schema, data, riwayat, dan `EXPLAIN` biasa.
- View + Edit: semua yang di atas, plus bikin dan jalanin migration, dan `EXPLAIN ANALYZE`.

Kalau satu user ada di beberapa group yang aksesnya beda ke database yang sama, yang dipakai level paling tinggi. Kodenya di `server/internal/authz/effective.go`. Database yang nggak ada sama yang nggak boleh diakses sama-sama dibalas 404.

## Migration

**Definisi.** Tiap migration punya SQL `up` dan `down`, atau ditandai irreversible plus alasannya. Begitu pernah dijalanin, SQL-nya dikunci dan nggak bisa diubah lagi.

**Rollback dan forward.** Rollback cuma bisa buat migration aktif paling akhir, jadi kalau ada A, B, C, yang di-rollback C dulu. Forward jalanin `up` lagi habis rollback, tetap urut, dan nggak ikut jalanin migration berikutnya.

**Riwayat.** Semua attempt dicatat, termasuk yang gagal: migration ID, execution ID, revisi, user, waktu, status, dan salinan SQL yang beneran dijalanin. Catatannya nggak bisa ditimpa (dijaga trigger database). Checkbox **Hide rollback** nyembunyiin pasangan eksekusi dan rollback-nya berdasarkan execution ID.

**Async.** Semua eksekusi masuk queue dulu. API langsung balikin `job_id`, SPA yang mantau statusnya. Retry dibatasi dan pakai backoff. Pesan duplikat nggak bikin double commit karena tiap job punya `idempotency_key` dan receipt di database target. Endpoint sinkron M1 sekarang ditolak (`queue_required`).

**Batch.** Data diproses per batch pakai cursor, nggak di-load sekaligus ke memori. Tiap batch satu transaction, dan checkpoint-nya disimpan di transaction yang sama. Kalau satu batch gagal, batch yang udah commit tetap ada dan statusnya jadi `partial`. Dari situ bisa **resume** dari checkpoint atau **revert** per batch dengan urutan terbalik.

**Lock.** Satu database cuma boleh jalanin satu migration, database beda boleh paralel. Lock-nya berupa lease di database platform (TTL 30 detik, diperpanjang heartbeat tiap 10 detik) plus advisory lock di database target. Sebelum commit, worker ngecek dulu lease-nya masih punya dia atau nggak.

**Kalau worker mati.** Heartbeat berhenti, jadi lease habis paling lama 30 detik dan advisory lock ikut lepas karena koneksinya putus. Worker lain bakal ambil alih job itu dan ngecek bukti di database target:

- receipt job ada: berarti udah commit, ditandai `success`
- ada checkpoint batch yang udah commit: jadi `partial`, database ditahan sampai di-resume atau di-revert
- nggak ada bukti commit: dianggap gagal, terus di-retry
- database target nggak bisa dicek: jadi `uncertain`, database ditahan sampai di-reconcile

Selama ditahan, job lain ke database itu ditolak `409 database_blocked`. Setelah database target bisa diakses lagi, jalanin `POST .../executions/{id}/reconcile`.

### Dataset uji

Datasetnya dibikin pakai `go run ./cmd/benchdataset -rows 1000000`. Tool ini ngisi tabel `bench_events`, terus ngukur `UPDATE` seluruh row tanpa batching (di-rollback, jadi datanya nggak berubah).

| Jumlah row | PostgreSQL | Run | Min    | Rata-rata | Max    |
| ---------- | ---------- | --- | ------ | --------- | ------ |
| 1.000.000  | 16.15      | 3   | 5,06 s | 5,18 s    | 5,40 s |

Contoh migration batch di dataset ini (batch size `50000`, cursor `id`):

```sql
UPDATE public.bench_events SET processed = TRUE WHERE id >= {{cursor_start}} AND id <= {{cursor_end}};
```

## Approval

Alurnya: Draft, Pending Review, Approved, Pending (queue), Running, lalu Success atau Failed.

Tiap group punya leader. Setiap perubahan (termasuk rollback, forward, resume, revert) wajib di-approve leader dari **semua** group yang punya akses ke database itu. Kalau ada group yang belum punya leader, pengajuannya ditolak. Satu reject langsung nolak pengajuan. Approval cuma kepakai sekali dan hangus kalau definisinya diedit.

## Logging dan tracing

Log ditulis sebagai JSON lewat `slog` ke stdout. Tiap eksekusi SQL ngeluarin event terpisah: `sql.start`, `sql.result`, terus `sql.commit` atau `sql.rollback`. Password, token, dan session ID disamarkan dulu sebelum ditulis.

```json
{"time":"2026-11-27T05:12:44.217Z","level":"INFO","msg":"sql.result","query":"CREATE INDEX orders_status_idx ON orders (status)","migration_id":12,"execution_id":41,"job_id":"5b0f3c1e-9a2d-4c51-8f0e-2d1b7a9c4e10","attempt":1,"action":"up","duration_ms":13.8,"rows_affected":0,"status":"success","trace_id":"4bf92f3577b34da6a3ce929d0e0e4736","span_id":"53995c3f42cd8ad8","actor":"bob","database_id":1}
```

Trace OpenTelemetry nyambung dari request API sampai query:

```
HTTP POST .../execute
  queue.publish
    worker.process_job
      lock.acquire
      migration.execute_normal
        db.exec
        db.commit
      lock.release
```

`trace_id` yang sama muncul di log, di riwayat eksekusi, dan di header `X-Trace-ID`. Contoh span dari console exporter:

```json
{"Name":"db.exec","SpanContext":{"TraceID":"4bf92f3577b34da6a3ce929d0e0e4736","SpanID":"53995c3f42cd8ad8"},"Attributes":[{"Key":"db.statement","Value":{"Type":"STRING","Value":"CREATE INDEX orders_status_idx ON orders (status)"}},{"Key":"migration.id","Value":{"Type":"INT64","Value":12}},{"Key":"db.duration_ms","Value":{"Type":"FLOAT64","Value":13.8}}],"Status":{"Code":"Unset"}}
```

## Bonus: Observability

Server punya endpoint `GET /metrics` (format Prometheus) buat jumlah job per status, waktu tunggu queue dan lock, durasi migration, retry, batch dan row yang diproses, serta koneksi pool aktif, idle, dan waktu tunggunya.

```bash
docker compose --profile observability up -d
cd server && OTEL_TRACES_EXPORTER=otlp LOG_FILE=../logs/server.log go run .
```

Log dan trace dikumpulin lewat OpenTelemetry Collector ke Loki dan Tempo, metrik di-scrape Prometheus. Dashboard-nya udah siap di Grafana (`http://localhost:3000`, folder IF3110). Dari log di Loki bisa langsung klik `trace_id` buat buka trace-nya di Tempo. Semua konfigurasi ada di folder `observability/`.

## Bonus: Stress test k6

```bash
k6 run loadtest/k6/stress.js
```

Ada dua skenario jalan bareng. Yang pertama baca API (list database, schema, riwayat, job, lock). Yang kedua alur migration lengkap: bikin definisi, approval, execute, terus nunggu sampai job beres. Beban naik bertahap dari normal (10 VU baca + 2 VU migration) ke 2x, 5x, 10x, lalu turun lagi buat lihat pemulihannya. Job dihitung sukses kalau statusnya beneran `completed`, bukan cuma dapat `202`.

Hasil run 27 November 2026:

| Tahap      | API RPS | API p50 | API p95  | Error API | Job selesai | Queue wait p95 | Job selesai p95 |
| ---------- | ------- | ------- | -------- | --------- | ----------- | -------------- | --------------- |
| normal     | 14,4    | 11,7 ms | 255 ms   | 1,2%      | 14          | 20 s           | 58 s            |
| stress x2  | 7,3     | 8,3 ms  | 60 s     | 10,3%     | 5           | 54 s           | 108 s           |
| stress x5  | 24,3    | 8,6 ms  | 5,0 s    | 10,3%     | 2           | 165 s          | 166 s           |
| stress x10 | 30,5    | 8,4 ms  | 5,0 s    | 15,4%     | 6           | 112 s          | 178 s           |
| recovery   | 5,5     | 7,8 ms  | 5,0 s    | 16,6%     | 5           | 55 s           | 103 s           |

Tiap run juga nyimpen ringkasannya ke `loadtest/k6/summary-<waktu>.md`.

Yang kelihatan dari hasilnya:

- **Di beban normal** API masih cepat, tapi job udah mulai antre (queue wait 20 detik, padahal eksekusi SQL-nya cuma sekitar 0,1 detik).
- **Mulai 2x**, error naik ke 10 sampai 16%. Semua error-nya dari endpoint `schema`. Endpoint lain yang cuma baca database platform aman terus sampai 10x.
- **Pas recovery** belum pulih, soalnya antrean job dari tahap sebelumnya masih numpuk.

Penyebabnya:

1. Endpoint `schema` jalanin 3 query per tabel, dan tiap job k6 bikin tabel baru, jadi makin lama makin lambat.
2. Pool koneksi ke database target cuma 5 dan dipakai bareng sama viewer dan worker. Pas pool penuh, request nunggu sampai timeout 5 detik (itu kenapa p95-nya mentok di 5 detik), dan worker juga ikut nunggu.
3. Satu database cuma boleh jalanin satu migration, jadi job antre.

Catatan: 46 pengajuan approval di run ini ditolak karena ada group (`DAN`) yang belum punya leader. Itu emang perilaku yang benar, tapi efeknya job cuma jalan di satu database.

Rencana perbaikan: gabungin query `schema` jadi per schema (bukan per tabel), naikin dan pisahin pool koneksi buat viewer dan worker, plus kasih timeout di query viewer.

## Bonus: CodeQL

Workflow di `.github/workflows/codeql.yml` nge-scan kode Go (`server/`) dan TypeScript (`client/src`) pakai query `security-extended` tiap push atau PR ke `main`, plus tiap Senin. Hasilnya ada di tab Security, bagian Code scanning.

| Rule | Severity | Lokasi | Keputusan | Alasan |
| ---- | -------- | ------ | --------- | ------ |
| -    | -        | -      | -         | -      |

Eksekusi SQL dari migration bakal ke-flag sebagai SQL injection. Itu emang fitur utamanya, dan udah dibatasi: cuma user View + Edit, wajib approval semua leader, dan cuma jalan di database target. Jadi dicatat sebagai risiko yang diterima.

## Testing

Pastikan Postgres udah nyala (`docker compose up -d`), lalu:

```bash
cd server
go test -p 1 ./...
```

Ada juga smoketest end-to-end ke server yang lagi jalan: `go run ./cmd/smoketest`. Jalanin `devseed` dulu, dan nyalain servernya pakai `REQUIRE_APPROVAL=false LEGACY_SYNC_MIGRATION=true`.

## Struktur folder

```
client/          SPA (src/) dan template lama M1 (templates/, static/) yang masih dipakai test
server/
  cmd/           worker, devseed, smoketest, benchdataset
  internal/      auth, session, authz, admin, dbmanager, migration, queue, dblock,
                 approval, telemetry, metrics
  migrations/    skema database platform
docker/          database contoh
observability/   config Collector, Tempo, Loki, Prometheus, Grafana
loadtest/k6/     script dan hasil stress test
```
