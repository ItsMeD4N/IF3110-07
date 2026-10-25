# Database Management Platform (DB Platform)
> **Tugas Besar IF3110 Pengembangan Aplikasi Web 2026 — Milestone 1**  
> **Kelompok IF3110-07**

Platform manajemen database terpusat berbasis web untuk mengelola akses multi-pengguna, mengintrospeksi struktur dan data tabel, serta menjalankan migrasi skema database secara terkontrol dengan jaminan transaksi atomik (*commit/rollback*). Dilengkapi dengan fitur analisis rencana eksekusi query (*Query Execution Plan Analysis* menggunakan `EXPLAIN` dan `EXPLAIN ANALYZE`).

---

## Anggota Kelompok & Pembagian Tugas

| No | Nama | NIM | Peran Utama | Lingkup Tanggung Jawab |
| :---: | :--- | :---: | :---: | :--- |
| 1 | **DAN** (ItsMeD4N) | *[Isi NIM]* | **Orang A** | **Identity, Session & Security Foundation** |
| 2 | **YavieAzka** | *13524077* | **Orang B** | **Authorization & Admin Console, Bonus (Query Execution Plan Analysis)** |
| 3 | **R27-04** (Raynaldi) | *[Isi NIM]* | **Orang C** | **Database Viewer, Migration** |

### Detail Pembagian Tugas (Work Breakdown Structure)

#### 1. Orang A — Identity, Authentication & Session Handling
- **Database & Model**:
  - Perancangan dan migrasi tabel metadata `users` dan `sessions` (`server/migrations/001_orang_a_users_sessions.sql`).
  - Repository abstraksi pengguna dan sesi di backend (`internal/auth`, `internal/session`).
- **Autentikasi & Keamanan Kredensial**:
  - Hashing kata sandi menggunakan algoritma **Bcrypt** dengan cost factor 12 (`internal/auth/hash.go`).
  - Pencegahan timing-attack pada perbandingan hash password.
- **Manajemen Sesi Pengguna**:
  - Implementasi *opaque session token* 32-byte cryptographically secure (`crypto/rand`).
  - Penyimpanan token di database hanya dalam bentuk digest **SHA-256** (mencegah kebocoran sesi jika database terkompromi).
  - Penegakan **Idle Timeout**: Sesi kedaluwarsa jika tidak ada aktivitas selama **30 menit**.
  - Penegakan **Absolute Expiration**: Sesi hangus mutlak setelah **12 jam** sejak login pertama kali.
  - **Rotasi Token Berkala**: Token sesi dirotasi setiap **15 menit** dengan **Grace Period 60 detik** untuk mencegah race condition request paralel.
  - **Pencabutan Sesi (Revocation)**: Logout, reset password, dan deaktivasi akun mencabut (*force logout*) seluruh sesi aktif terkait.
- **Proteksi Anti-CSRF**:
  - Implementasi Double-Submit Cookie CSRF protection (`internal/csrf`).
  - Penegakan verifikasi token anti-CSRF pada seluruh request *state-changing* (`POST`, `PUT`, `DELETE`).
- **Antarmuka & Fitur Admin**:
  - Halaman login web (`client/templates/auth/login.html`).
  - Layout master Go template (`client/templates/layouts/base.html`, `navbar.html`, alert flash messages).
  - Manajemen Pengguna oleh Platform Admin (`/admin/users`): Pembuatan user baru, edit status aktif/nonaktif, edit privilege admin, reset kata sandi, cabut sesi paksa, serta proteksi pencegahan self-deactivation dan self-deletion.

#### 2. Orang B — Authorization, Groups & Database Connections
- **Database & Model**:
  - Perancangan dan migrasi tabel `groups`, `group_members`, `connections`, dan `group_database_grants` (`server/migrations/002_orang_b_groups_connections.sql`).
- **Mesin Otorisasi (RBAC Engine)**:
  - Implementasi pengecekan izin granular (*Permission Evaluation Engine*) pada context pengguna (`internal/authz`).
  - Penegakan hak akses berbasis peran (Platform Admin vs Standard User; izin `View Only` vs `View + Edit` per database).
- **Enkripsi Kredensial Database Target**:
  - Enkripsi password koneksi database target *at-rest* menggunakan **NaCl Secretbox** (kriptografi authenticated symmetric encryption) (`internal/secretbox`).
  - Kunci enkripsi dikelola melalui environment variable `APP_ENCRYPTION_KEY`.
- **Manajemen Koneksi Database**:
  - CRUD koneksi database target (`/admin/connections`).
  - Fitur pengujian koneksi database (*Ping test*) secara live dengan estimasi latensi koneksi (ms) dan sanitasi error koneksi database.
  - Pengelolaan connection pool dinamis per target database (`internal/connmgr`).
- **Manajemen Group & Hak Akses**:
  - CRUD group dan pengelolaan anggota group (`/admin/groups`).
  - Pemberian izin akses database per group (`View Only` atau `View + Edit`).
  - Dashboard eksplorasi database yang dapat diakses pengguna (`/databases`).

#### 3. Orang C — Database Viewer & Migration Platform
- **Database & Model**:
  - Perancangan dan migrasi tabel riwayat migrasi `migrations` (`server/migrations/003_orang_c_migrations.sql`).
- **Introspeksi Skema Database (Database Viewer)**:
  - Introspeksi skema database target (`internal/dbmanager`): menampilkan daftar tabel, tipe objek, detail kolom, tipe data, nullable, default value, constraint (primary key, foreign key, unique, check), dan index.
- **Pembaca Data Tabel (Data Viewer)**:
  - Pembaca data baris tabel interaktif dengan paginasi aman (*limit* & *offset*).
- **Mesin Eksekusi Migrasi (Migration Runner)**:
  - Editor query SQL migration (`client/templates/migration/editor.html`).
  - Eksekusi migrasi berurutan di dalam transaksi database atomik: otomatis melakukan **commit** jika berhasil dan **rollback penuh 100%** jika terjadi kegagalan/sintaks error.
  - Pencatatan riwayat migrasi linear per database (nomor versi, query SQL, pelaksana/executor, timestamp eksekusi, status).
  - Pembatasan hak eksekusi migrasi: hanya pengguna dengan izin `View + Edit` yang dapat menjalankan migrasi, sedangkan `View Only` hanya dapat melihat riwayat.

#### 4. Fitur Bonus — Query Execution Plan Analysis (`EXPLAIN` & `EXPLAIN ANALYZE`)
- **Backend (`internal/migration/explain.go`)**:
  - Endpoint `POST /api/databases/{id}/migrations/explain`.
  - Mendukung dua mode: **EXPLAIN** (estimasi biaya rencana query) dan **EXPLAIN ANALYZE** (eksekusi riil terukur).
  - **Jaminan Keamanan Rollback**: Seluruh pemanggilan `EXPLAIN ANALYZE` (termasuk pada statement manipulasi data seperti `INSERT`, `UPDATE`, `DELETE`) dieksekusi di dalam transaksi yang di-*rollback* paksa (`defer tx.Rollback()`), menjamin **0 efek samping/data sampah** tertinggal di database target.
  - Parser ekstraksi ringkasan metrik: tipe scan (Seq Scan, Index Scan), penggunaan index, strategi join (Hash Join, Nested Loop, Merge Join), startup/total cost, actual execution time (ms), dan perbandingan jumlah baris *planned* vs *processed*.
- **Frontend (`client/templates/migration/editor.html`)**:
  - Antarmuka visual interaktif dengan toggle mode analisis.
  - Kartu metrik performa (*Execution Time, Total Cost, Rows Processed, Scan Types, Index Status*).
  - Visualisasi pohon hirarki execution plan node.
  - Collapsible view untuk raw ASCII execution plan dari engine PostgreSQL.

---

## Arsitektur & Teknologi

```text
┌────────────────────────────────────────────────────────┐
│                   Browser Client                       │
│    (HTML5, CSS3 Custom Theme, Vanilla JavaScript)      │
└──────────────────────────┬─────────────────────────────┘
                           │ HTTP / HTTPS (SSR & REST API)
┌──────────────────────────▼─────────────────────────────┐
│                 Server Backend (Go 1.22+)              │
│                                                        │
│  Global Middleware:                                    │
│   ├── Security Headers (CSP, X-Frame, X-Content-Type)  │
│   ├── Double Submit Cookie Anti-CSRF Middleware        │
│   ├── CORS Middleware & Request Logger                 │
│   └── Authenticate & Context Injector (CurrentUser)    │
│                                                        │
│  Modul Aplikasi:                                       │
│   ├── internal/auth     -> Autentikasi Bcrypt          │
│   ├── internal/session  -> Session Lifecycle Manager   │
│   ├── internal/authz    -> RBAC Permission Engine      │
│   ├── internal/admin    -> Group, Connection, Crypto   │
│   ├── internal/dbmanager-> Schema & Table Viewer       │
│   └── internal/migration-> Migration & EXPLAIN Runner  │
└────────────┬─────────────────────────────┬─────────────┘
             │                             │
┌────────────▼─────────────┐ ┌─────────────▼─────────────┐
│   PostgreSQL (Port 5433) │ │   Target Database (DB)    │
│    Metadata Platform DB  │ │   Koneksi Kelolaan User   │
└──────────────────────────┘ └───────────────────────────┘
```

- **Backend**: Go (Golang) 1.22+ menggunakan standar pustaka `net/http` router terbaru, `database/sql`, dan driver PostgreSQL `github.com/lib/pq`.
- **Frontend**: Server-Side Rendered (SSR) Go HTML Templates dipadukan dengan vanilla JavaScript dan sistem desain CSS modern (dark slate & glassmorphism).
- **Keamanan**: `golang.org/x/crypto/bcrypt`, `golang.org/x/crypto/nacl/secretbox`.
- **Database**: PostgreSQL 16 (diorkestrasi melalui Docker Compose).

---

## Parameter Konfigurasi & Keamanan

Sesuai spesifikasi teknis Milestone 1, sistem mengonfigurasi parameter sesi dan keamanan sebagai berikut:

| Parameter | Nilai Konfigurasi | Deskripsi / Penjelasan |
| :--- | :---: | :--- |
| **Idle Timeout** | `30 Menit` | Sesi otomatis tidak valid jika tidak ada interaksi pengguna selama 30 menit. |
| **Absolute Expiration** | `12 Jam` | Batas hidup mutlak sesi sejak login pertama kali (sesi hangus meski pengguna terus aktif). |
| **Session Rotation Interval** | `15 Menit` | Token opaque diganti secara berkala setiap 15 menit tanpa mereset batas 12 jam. |
| **Rotation Grace Period** | `60 Detik` | Token lama tetap diterima selama 60 detik pasca rotasi untuk menghindari race condition. |
| **Token Entropy** | `32 Bytes (256-bit)` | Dibuat menggunakan `crypto/rand` berkekuatan kriptografi. |
| **Session Storage Hashing** | `SHA-256` | Hanya digest SHA-256 yang tersimpan di DB metadata; token asli hanya ada di cookie client. |
| **Cookie Attributes** | `HttpOnly, SameSite=Lax` | Mencegah pembacaan cookie sesi via XSS dan eksfiltrasi lintas situs. |
| **Password Hashing** | `Bcrypt Cost 12` | Standar keamanan tinggi terhadap brute-force dan rainbow table. |
| **Kredensial Koneksi Target** | `NaCl Secretbox` | Password database target dienkripsi menggunakan authenticated symmetric cipher. |

---

## Panduan Menjalankan Aplikasi

### 1. Prasyarat
- **Go**: Versi 1.22 atau lebih baru.
- **Docker & Docker Compose**: Untuk menjalankan instance PostgreSQL platform.
- **Git**

### 2. Persiapan Database Platform
Jalankan container PostgreSQL platform melalui docker compose:
```powershell
docker compose up -d
```
Container database akan aktif di port host `5433` dengan database `platform_db`.

### 3. Migrasi Skema Database
Migrasi skema database dieksekusi secara otomatis saat server pertama kali dijalankan, atau dapat diinjeksi manual:
```powershell
docker exec -i if3110_postgres psql -U postgres -d platform_db < server/migrations/001_orang_a_users_sessions.sql
docker exec -i if3110_postgres psql -U postgres -d platform_db < server/migrations/002_orang_b_groups_connections.sql
docker exec -i if3110_postgres psql -U postgres -d platform_db < server/migrations/003_orang_c_migrations.sql
```

### 4. Menjalankan Server Backend & Web
Buka terminal dan masuk ke direktori `server`:
```powershell
cd server
go run main.go
```
Server akan aktif di: **`http://localhost:8080`**

### 5. Akun Awal (Default Seeding)
Aplikasi telah dilengkapi dengan akun bawaan untuk pengujian awal:
- **Username**: `admin`
- **Password**: `Admin#Password123`
- **Peran**: `Platform Admin`

---

## Panduan Menjalankan Automated Tests

Seluruh modul telah dilengkapi pengujian otomatis (*unit test*, *integration test*, dan *E2E live smoketest*).

> **Tips Windows PowerShell**: Sertakan `$env:TMP='D:\tmp'` dan `-p 1` untuk menjamin stabilitas memori compiler pada lingkungan Windows.

### 1. Menjalankan Seluruh Unit & Integration Test
```powershell
cd server
$env:TMP='D:\tmp'; $env:TEMP='D:\tmp'; $env:GOTMPDIR='D:\tmp'; $env:GOCACHE='D:\tmp\gocache'; go test -p 1 ./internal/...
```

### 2. Menjalankan Test Spesifik Per Modul
- **Modul Orang A (Identity & Session)**:
  ```powershell
  go test -v -p 1 ./internal/auth/... ./internal/session/... ./internal/user/... ./internal/csrf/... ./internal/web/...
  ```
- **Modul Orang B (Authorization & Connections)**:
  ```powershell
  go test -v -p 1 ./internal/authz/... ./internal/admin/... ./internal/connmgr/...
  ```
- **Modul Orang C (Viewer, Migration & EXPLAIN Bonus)**:
  ```powershell
  go test -v -p 1 ./internal/dbmanager/... ./internal/migration/...
  ```

### 3. Menjalankan Skrip Live E2E Smoketest (Happy Path & 9 Edge Cases)
Pastikan server sedang berjalan di `http://localhost:8080`, lalu jalankan:
```powershell
go run ./cmd/smoketest
```

Skrip ini akan menguji secara live:
1. **Bagian I (Happy Path)**: Health check, CSRF exchange, Admin login, Introspeksi user, Target DB connection ping, Migration SQL runner, Introspeksi skema, dan Pembacaan data tabel.
2. **Bagian II (9 Skenario Kasus Batas & Error)**:
   - `[E1]` Login dengan password salah ditolak & cookie sesi tidak terbit.
   - `[E2]` Request POST tanpa CSRF token ditolak HTTP 403.
   - `[E3]` Request POST dengan CSRF token palsu/dimanipulasi ditolak HTTP 403.
   - `[E4]` Pembatasan RBAC: User biasa ditolak HTTP 403 saat mengakses endpoint admin.
   - `[E5]` Integritas Akun: Admin dilarang menonaktifkan akunnya sendiri (HTTP 400).
   - `[E6]` Validasi input password lemah (< 8 karakter) ditolak HTTP 400.
   - `[E7]` Modifikasi / tampering cookie sesi palsu ditolak HTTP 401.
   - `[E8]` Atomic Rollback: Migrasi dengan sintaks SQL salah di-rollback 100% dan tabel parsial tidak tersimpan di database.
   - `[E9]` Query Plan DDL: Penanganan graceful informatif pada EXPLAIN statement non-DML.

---

## Struktur Direktori Proyek

```text
IF3110-07/
├── client/                              # Kode & Aset Antarmuka (Frontend)
│   ├── static/                          # Aset statis yang di-serve langsung oleh server Go
│   │   ├── css/
│   │   │   ├── app.css                  # Stylesheet terpadu (Modern Dark Theme & Layout)
│   │   │   └── style.css                # Komponen CSS & tokens
│   │   └── js/
│   │       └── main.js                  # Skrip interaktif modal dialog & DOM helper
│   └── templates/                       # Server-Side Rendered (SSR) Go HTML Templates
│       ├── layouts/                     # Master layout (base.html, navbar, alerts)
│       ├── auth/                        # Halaman autentikasi (login.html)
│       ├── admin/                       # Halaman Platform Admin (users, groups, connections)
│       ├── databases/                   # Dashboard database pengguna (overview.html)
│       ├── dbmanager/                   # Introspeksi skema & data viewer (viewer.html)
│       └── migration/                   # Migration SQL editor & EXPLAIN visualizer (editor.html)
├── server/                              # Seluruh Kode Backend (Go 1.22+)
│   ├── cmd/
│   │   └── smoketest/
│   │       └── main.go                  # Skrip automated test E2E live (Happy path & 9 Edge cases)
│   ├── internal/
│   │   ├── config/                      # Manajemen environment & konfigurasi server
│   │   ├── database/                    # Connection pool platform metadata DB
│   │   ├── auth/                        # [Orang A] Login, Bcrypt hashing, CurrentUser context
│   │   ├── session/                     # [Orang A] Session store, rotasi, timeout, grace period
│   │   ├── csrf/                        # [Orang A] Double submit cookie anti-CSRF protection
│   │   ├── user/                        # [Orang A] REST API & CRUD akun Platform Admin
│   │   ├── authz/                       # [Orang B] RBAC permission engine & middleware
│   │   ├── admin/                       # [Orang B] Manajemen group, member, koneksi DB target
│   │   ├── secretbox/                   # [Orang B] Enkripsi kredensial koneksi at-rest
│   │   ├── connmgr/                     # [Orang B] Connection pool & ping tester DB target
│   │   ├── dbmanager/                   # [Orang C] Introspeksi skema tabel & pembaca data
│   │   ├── migration/                   # [Orang C] Transactional SQL migration & EXPLAIN runner
│   │   ├── view/                        # Engine renderer HTML template & flash cookie
│   │   └── web/                         # Handler web MPA & routing halaman HTML
│   ├── migrations/                      # Skrip SQL migrasi database platform
│   │   ├── 001_orang_a_users_sessions.sql
│   │   ├── 002_orang_b_groups_connections.sql
│   │   └── 003_orang_c_migrations.sql
│   ├── go.mod                           # Modul dependensi Go
│   ├── go.sum                           # Checksum dependensi Go
│   └── main.go                          # Entry point aplikasi utama
├── docs/                                # Dokumen spesifikasi tugas besar & panduan
├── docker-compose.yml                   # Orkestrasi Docker container PostgreSQL platform
└── README.md                            # Dokumentasi utama proyek
```

---

## Lisensi & Integritas Akademik
Proyek ini dikembangkan untuk memenuhi tugas besar mata kuliah **IF3110 Pengembangan Aplikasi Web 2026** di Institut Teknologi Bandung. Seluruh kode sumber mematuhi aturan integritas akademik yang berlaku.
