IF3110-07/  
├── client/                      # Seluruh kode dan aset antarmuka (Frontend)  
│   ├── public/                  # Aset statis publik (favicon, icons, dll)  
│   ├── src/                     # Source frontend (UI components, pages, styles)  
│   │   ├── components/          # [ORANG A] Layout dasar, Navbar, Alert, CSRF Field  
│   │   └── pages/               # [ORANG A] Login, Admin Users  
│   ├── package.json             # Dependensi frontend  
│   └── vite.config.ts           # Konfigurasi build Vite  
├── server/                      # Seluruh kode backend Go  
│   ├── cmd/  
│   │   └── server/  
│   │       └── main.go          # Entry point utama (hanya memanggil RegisterRoutes per modul)  
│   ├── internal/  
│   │   ├── config/              # Konfigurasi env (DB URL, Port, Session Secret, AppEnv)  
│   │   ├── database/            # Koneksi database platform & pool  
│   │   ├── auth/                # [ORANG A] Login, logout, middleware CurrentUser, hashing  
│   │   ├── session/             # [ORANG A] Engine session, store DB, rotasi, grace period  
│   │   ├── csrf/                # [ORANG A] Middleware CSRF & token generator  
│   │   ├── user/                # [ORANG A] CRUD user oleh Platform Admin  
│   │   ├── authz/               # [ORANG B] Can(ctx, ...), RequirePlatformAdmin  
│   │   ├── admin/               # [ORANG B] CRUD group, group access, DB connections  
│   │   ├── dbmanager/           # [ORANG C] Schema introspection, data viewer  
│   │   ├── migration/           # [ORANG C] SQL runner, rollback, migration logs  
│   │   └── fakes/               # [BERSAMA] Mock / stub sementara untuk development paralel  
│   ├── migrations/              # File SQL migrasi platform DB  
│   │   ├── 001_orang_a_users_sessions.sql  
│   │   ├── 002_orang_b_groups_connections.sql  
│   │   └── 003_orang_c_migrations.sql  
│   ├── go.mod                   # Modul Go backend  
│   └── main.go                  # Entry point (atau di cmd/server/main.go)  
└── docs/                        # Dokumentasi & panduan  
    ├── IF3110 - Milestone 1 - Pengembangan Aplikasi Web.md  
    └── PANDUAN_DAN_CHECKLIST_ORANG_A.md  
   
  
Frontend:  
client/  
├── static/                      # Aset statis (di-serve langsung oleh server Go)  
│   ├── css/                     # File style / CSS kustom  
│   ├── js/                      # Script JS vanilla client-side (jika diperlukan)  
│   └── images/                  # Gambar, ikon, SVG, dan favicon  
└── templates/                   # Go HTML Templates (SSR)  
    ├── layouts/                 # base.html, navbar, alerts flash message  
    ├── auth/                    # Halaman login (login.html)  
    ├── admin/                   # Halaman kelola user (users.html, form user)  
    ├── dbmanager/               # Scaffold untuk Orang C (database viewer)  
    └── migration/               # Scaffold untuk Orang C (migration runner)  
