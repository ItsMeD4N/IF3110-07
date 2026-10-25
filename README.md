# Database Management Platform (DB Platform)
> **Tugas Besar IF3110 Pengembangan Aplikasi Web 2026 — Milestone 1**  
> **Kelompok IF3110-07**

Platform manajemen database terpusat berbasis web untuk mengelola akses multi-pengguna, mengintrospeksi struktur dan data tabel, serta menjalankan migrasi skema database secara terkontrol dengan jaminan transaksi atomik (*commit/rollback*). Dilengkapi dengan fitur analisis rencana eksekusi query (*Query Execution Plan Analysis* menggunakan `EXPLAIN` dan `EXPLAIN ANALYZE`).

---

## Anggota Kelompok & Pembagian Tugas

| No | Nama | NIM | Peran Utama | Lingkup Tanggung Jawab |
| :---: | :--- | :---: | :---: | :--- |
| 1 | Yavie Azka Putra Araly | *13524077* | **Orang B** | **Authorization & Admin Console, Bonus (Query Execution Plan Analysis)** |
| 2 | Rainaldi Pratama F Sembiring | 13524117 | **Orang C** | **Database Viewer, Migration** |
| 3 | Daniel Putra Rywandi S | 13524143 | **Orang A** | **Identity, Session & Security Foundation** |


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
