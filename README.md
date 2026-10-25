# IF3110-07 Database Management Platform

Tugas Besar IF3110 Pengembangan Aplikasi Web 2026, Milestone 1.

Aplikasi web buat ngatur akses ke database PostgreSQL yang dipakai bareng. Admin mengatur user, group, dan koneksi database. User bisa lihat schema dan data, lalu bikin dan jalankan migration sesuai hak aksesnya. Semua migration jalan di dalam transaksi dan tercatat di riwayat.

## Anggota

| Nama | NIM | Bagian |
| --- | --- | --- |
| Yavie Azka Putra Araly | 13524077 | Authorization, admin console, bonus EXPLAIN |
| Rainaldi Pratama F Sembiring | 13524117 | Database viewer, migration |
| Daniel Putra Rywandi S | 13524143 | Autentikasi, session, keamanan |

## Stack

- Backend: Go 1.25 (`net/http`, `database/sql`, `lib/pq`)
- Frontend: Go template (bukan SPA) dan sedikit JavaScript
- Database: PostgreSQL 16 lewat Docker Compose

## Cara menjalankan

Butuh Go 1.25+, Docker, dan Git.

```bash
docker compose up -d
cd server
go run .
```

Buka `http://localhost:8080` lalu login pakai akun admin bawaan: `admin` / `Admin#Password123`.

Skema database platform dibuat otomatis waktu server start. Docker Compose juga menyiapkan database contoh `sample_db` (user dan password `sample_app`) yang bisa ditambahkan sebagai koneksi dari halaman admin.

Kalau mau isi user dan group contoh, jalankan `go run ./cmd/devseed` dari folder `server`. Password user contohnya muncul di akhir output.

Environment variable yang dipakai:

| Variabel | Default | Keterangan |
| --- | --- | --- |
| `PORT` | `8080` | Port server |
| `DATABASE_URL` | `postgres://postgres:postgres@localhost:5433/platform_db?sslmode=disable` | Database platform |
| `APP_ENV` | `development` | Isi `production` supaya cookie pakai `Secure` |
| `APP_ENCRYPTION_KEY` | kunci development | Kunci enkripsi password koneksi, wajib diisi di production (`openssl rand -base64 32`) |

## Session

| Pengaturan | Nilai |
| --- | --- |
| Idle timeout | 30 menit |
| Absolute expiration | 12 jam sejak login |
| Rotasi session ID | Tiap 15 menit, saat login, dan saat hak akses berubah |
| Grace period ID lama | 60 detik |

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

- SQL jalan dalam satu transaksi. Kalau ada statement yang gagal, semuanya di-rollback.
- `BEGIN`/`COMMIT` di dalam SQL ditolak, karena transaksinya sudah diatur platform.
- Nomor versi berurutan per database. Tiap record menyimpan SQL, siapa yang menjalankan, waktu, status, dan pesan error.
- Ada advisory lock supaya dua migration tidak jalan barengan di database yang sama.
- Kalau server mati di tengah migration, record yang masih `running` diubah jadi `failed` waktu server nyala lagi. Cek manual database target-nya untuk memastikan perubahannya sudah masuk atau belum.
- Bonus: `EXPLAIN` / `EXPLAIN ANALYZE` menampilkan jenis scan, index, join, cost, waktu, dan jumlah row. Mode `ANALYZE` selalu di-rollback dan dibatasi 10 detik per statement.

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

Smoketest ini mengecek alur dari login sampai migration dan viewer. Selain itu juga mengecek beberapa kasus yang harus gagal: password salah, token CSRF kosong atau palsu, user biasa buka endpoint admin, cookie session palsu, rollback migration yang error, dan EXPLAIN untuk DDL.

## Struktur folder

```
client/            template HTML, CSS, JS
server/
  cmd/             smoketest dan devseed
  internal/
    auth, session, csrf, user     login, session, CSRF, kelola user
    authz, admin, connmgr,        hak akses, group, koneksi database,
    secretbox, store              enkripsi password koneksi
    dbmanager                     viewer schema dan data
    migration                     migration dan EXPLAIN
    web, httpx                    render halaman dan helper HTTP
  migrations/      skema database platform
docker/            data database contoh
```
