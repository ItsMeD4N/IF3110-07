# API Contract — IF3110-07 Database Management Platform (Milestone 2)

Dokumen ini menjadi acuan bersama antara **Frontend (SPA)** dan **Backend (Go)** untuk integrasi Milestone 2.

- Bagian **1** menjelaskan konvensi HTTP, autentikasi, CSRF, dan format error.
- Bagian **2** adalah kontrak **Group Leader**, **Workflow & Approval**, 
- Bagian **3** adalah referensi ringkas endpoint yang **sudah tersedia di `main`**.

---

## 1. Konvensi Umum

### Base URL & Proxy
- Seluruh API berada di prefix `/api/*`.
- **Development:** Vite dev server mem-proxy `/api` ke `http://localhost:8080` agar tetap *same-origin* untuk cookie `HttpOnly` & `SameSite=Lax`.
- **Production / Demo:** Server Go men-serve hasil build SPA (`client/dist`) dan fallback `index.html` untuk semua route non-`/api/*`.

### Autentikasi & CSRF
- **Session Cookie:** `app_session_id` (`HttpOnly`, `SameSite=Lax`, `Secure` di production). Frontend selalu mengirim `credentials: "same-origin"` dan header `Accept: application/json`.
- **CSRF (Double-Submit Cookie):**
  - Cookie `csrf_token` (`HttpOnly: false`) diset otomatis pada request `GET` atau lewat `GET /api/csrf-token`.
  - Setiap request mutasi (`POST`, `PUT`, `PATCH`, `DELETE`) wajib menyertakan header `X-CSRF-Token` dengan nilai cookie `csrf_token` **terbaru** (wajib baca ulang cookie setelah `POST /api/auth/login` karena token dirotasi saat login).
- **Idempotency (Async Enqueue):**
  - Endpoint enqueue migration mendukung header `Idempotency-Key` atau field body `idempotency_key` untuk mencegah double submit.

### Format Error Standar
```json
{
  "error": "Pesan error yang ramah pengguna",
  "code": "validation_error"
}
```

| HTTP Status | `code` Umum | Penanganan di SPA |
|---|---|---|
| `401` | `unauthenticated` | Hapus state user, redirect ke `/login` |
| `403` | `forbidden`, `approval_required` | Tampilkan pesan akses ditolak / butuh approval leader |
| `404` | `not_found` | Tampilkan state tidak ditemukan (DB tidak ada / tidak punya akses sama-sama `404`) |
| `409` | `conflict`, `database_blocked`, `migration_immutable`, `not_latest_active`, `invalid_forward_order`, `already_active`, `invalid_state`, `idempotency_conflict` | Tampilkan banner/alert sesuai `code` (untuk `database_blocked`, payload tambahan: `lock_state`, `blocking_execution_id`) |
| `422` | `validation_error`, `migration_irreversible`, `unsafe_batch_revert`, `db_connection_failed` | Tampilkan error validasi pada form |
| `503` | `db_connection_failed` | Tampilkan peringatan koneksi DB target bermasalah |

---

## 2. Kontrak Milestone 2

Bagian ini merupakan dependensi Frontend SPA terhadap Backend untuk memenuhi spesifikasi Milestone 2.

### 2.1 Group Leader

#### 1) Tambahan Field pada `Group` (`GET /api/admin/groups`, `GET /api/admin/groups/{groupID}`)
```json
{
  "id": 1,
  "name": "Backend Team",
  "description": "Tim layanan inti",
  "leader_user_id": "a0000000-0000-0000-0000-000000000002",
  "leader_username": "alice",
  "member_count": 3,
  "grant_count": 2,
  "created_at": "2026-11-26T05:00:00Z",
  "updated_at": "2026-11-26T05:00:00Z"
}
```
> Catatan: `leader_user_id` dan `leader_username` boleh `null` / `""` jika belum ditetapkan, namun disarankan menetapkan leader saat/setelah menambah anggota (leader harus user aktif & menjadi anggota group).

#### 2) Menetapkan / Mengubah Leader Group
- **Endpoint:** `PUT /api/admin/groups/{groupID}/leader` *(Permission: Platform Admin / `platform.groups.manage`)*
- *(Alternatif: menerima `leader_user_id` opsional pada `PUT /api/admin/groups/{groupID}` juga diperbolehkan.)*
- **Request:**
```json
{
  "user_id": "a0000000-0000-0000-0000-000000000002"
}
```
- **Response `200 OK`:** Object `Group` terbaru (termasuk `leader_user_id` dan `leader_username`).
- **Error:** `422` jika user bukan anggota group / user nonaktif / `user_id` bukan UUID, `404` jika group/user tidak ditemukan.
- `user_id: null` atau `""` → mencabut leader. Jika leader dikeluarkan dari group (atau user dihapus), leader otomatis menjadi `null` (dijaga FK di database).

#### 3) Tambahan Info Leader pada `GET /api/authz/me`
Agar SPA dapat menampilkan menu **Pending Approvals** dan tombol **Approve/Reject** sesuai role user:
```json
{
  "id": "a0000000-0000-0000-0000-000000000002",
  "username": "alice",
  "is_platform_admin": false,
  "permissions": [],
  "led_groups": [
    { "id": 1, "name": "Backend Team" }
  ]
}
```

---

### 2.2 Migration Workflow & Multi-Group Leader Approval

Sesuai spesifikasi Milestone 2:
- Alur: `Draft → Pending Review → Approved → Pending (Queue) → Running → Success / Failed`.
- **Setiap perubahan database** — termasuk `up` / `batch_up`, `rollback`, `forward`, `batch_resume`, dan `batch_revert` — wajib disetujui oleh **leader setiap group** yang memiliki akses ke database tersebut (**baik `View Only` maupun `View + Edit`**).
- Kontrak ini dirancang selaras dengan interface `migration.ApprovalChecker` (`CheckApproval(ctx, databaseID, migrationID, action)`).

#### 1) Ajukan Review (Create Approval Request)
- **Endpoint:** `POST /api/databases/{dbID}/migrations/definitions/{migrationID}/approval-requests`
- **Permission:** `db.migrations.execute` (`View + Edit` pada database target).
- **Request:**
```json
{
  "action": "up",
  "target_execution_id": 0,
  "note": "Menambahkan index pada tabel orders"
}
```
  - `action`: salah satu dari `"up" | "batch_up" | "rollback" | "forward" | "batch_resume" | "batch_revert"`.
  - `target_execution_id`: opsional (diisi untuk `batch_resume` / `batch_revert` agar jelas execution mana yang di-resume/di-revert).
  - `note`: opsional, catatan dari pengaju untuk para reviewer/leader.
- **Perilaku Backend:**
  - Mengambil seluruh group yang memiliki grant (`view_only` atau `view_edit`) pada `dbID`.
  - Jika ada group yang belum memiliki leader → balas `422` (`code: "group_leader_missing"`).
  - Membentuk baris approval (`status: "pending"`) untuk setiap group leader yang relevan.
  - Untuk `up`/`batch_up`, mengubah `workflow_status` definisi menjadi `"pending_review"`.
  - Jika definisi yang belum immutable diedit (`PUT .../definitions/{migrationID}`), request review sebelumnya untuk definisi tersebut otomatis dibatalkan/di-invalidate dan `workflow_status` kembali ke `"draft"`.
- **Normalisasi aksi** (agar cocok dengan aksi yang dipakai engine saat enqueue): `up` pada definisi batch → `batch_up`; `rollback` pada definisi batch → `batch_revert`. `rollback` otomatis mengisi `target_execution_id` = execution aktif.
- **Validasi awal:** `409 already_active` (up/forward saat sudah aktif), `409 not_latest_active`, `409 invalid_forward_order`, `422 migration_irreversible`, `409 approval_already_open` (masih ada pengajuan `pending_review`/`approved` untuk migration + aksi yang sama), `422 group_leader_missing` (payload tambahan `groups: string[]`).
- Approval terikat pada `migration_revision`; jika definisi diedit, approval lama otomatis `superseded`.
- **Response `201 Created`:** Object `ApprovalRequest` (lihat struktur di bawah).

#### 2) Daftar Approval Request pada Suatu Database
- **Endpoint:** `GET /api/databases/{dbID}/approval-requests?status=pending_review&migration_id=12`
- **Permission:** `db.migrations.view` (`View Only` maupun `View + Edit`, agar leader dari group `View Only` tetap bisa melihat dan mereview).
- **Response `200 OK`:**
```json
{
  "approval_requests": [
    {
      "id": 10,
      "database_id": 1,
      "database_name": "sample_db",
      "migration_id": 5,
      "migration_version": 5,
      "migration_revision": 1,
      "migration_name": "backfill_order_status",
      "action": "batch_up",
      "target_execution_id": null,
      "requested_by": "bob",
      "note": "Backfill status order lama per batch 5000",
      "status": "pending_review",
      "required_approvals": 2,
      "approved_count": 1,
      "can_current_user_decide": true,
      "approvals": [
        {
          "group_id": 1,
          "group_name": "Backend Team",
          "access_level": "view_edit",
          "leader_user_id": "uuid-1",
          "leader_username": "alice",
          "decision": "approved",
          "comment": "SQL dan batch cursor aman",
          "decided_at": "2026-11-26T06:00:00Z"
        },
        {
          "group_id": 2,
          "group_name": "Analytics Team",
          "access_level": "view_only",
          "leader_user_id": "uuid-2",
          "leader_username": "charlie",
          "decision": "pending",
          "comment": "",
          "decided_at": null
        }
      ],
      "created_at": "2026-11-26T05:50:00Z",
      "updated_at": "2026-11-26T06:00:00Z"
    }
  ]
}
```
  - Nilai `status`: `"pending_review" | "approved" | "rejected" | "consumed" | "superseded"`.
  - `[TAMBAHAN]` field `job_id` & `consumed_at` terisi setelah approval dipakai untuk enqueue; `decided_by` pada tiap item `approvals`.
  - Query opsional: `status`, `migration_id`, `limit` (default 100, maks 500).

#### 3) Detail Approval Request
- **Endpoint:** `GET /api/databases/{dbID}/approval-requests/{requestID}`
- **Permission:** `db.migrations.view`.
- **Response `200 OK`:** Object `ApprovalRequest` tunggal, ditambah field `definition: { sql_up, sql_down, is_irreversible, irreversible_reason, execution_mode, batch_config, workflow_status, current_revision }` agar reviewer bisa membaca SQL.

#### 4) Inbox Approval Lintas Database (untuk Leader yang Sedang Login)
- **Endpoint:** `GET /api/approvals/pending`
- **Permission:** `RequireAuth` (mengembalikan seluruh `approval_requests` berstatus `pending_review` di mana user yang login adalah leader dari minimal satu group yang masih berstatus `"pending"`).
- **Response `200 OK`:** `{ "approval_requests": [ ... ] }`

#### 5) Approve / Reject oleh Group Leader
- **Endpoint Approve:** `POST /api/databases/{dbID}/approval-requests/{requestID}/approve`
- **Endpoint Reject:** `POST /api/databases/{dbID}/approval-requests/{requestID}/reject`
- **Permission:** User terautentikasi yang merupakan **leader dari minimal satu group yang punya akses ke `dbID`** (baik `View Only` maupun `View + Edit`). Jika bukan leader → `403 Forbidden`.
- **Request Body:**
```json
{
  "comment": "Aman untuk service analytics"
}
```
  - Untuk `approve`: `comment` opsional.
  - Untuk `reject`: `comment` wajib diisi (alasan penolakan).
- **Perilaku Backend:**
  - Jika user adalah leader dari beberapa group yang sama-sama mengakses `dbID`, keputusan langsung diterapkan untuk semua group yang ia pimpin pada request tersebut (atau terima `group_id` opsional).
  - Saat seluruh group berstatus `"approved"` → status `ApprovalRequest` menjadi `"approved"` (dan untuk `up`/`batch_up`, `workflow_status` definisi menjadi `"approved"`).
  - Saat salah satu leader melakukan `reject` → status `ApprovalRequest` menjadi `"rejected"` (dan jika definisi belum immutable, user `View + Edit` dapat mengedit definisi lalu mengajukan review ulang).
- **Error:** `403 forbidden` (bukan leader group terkait), `404` (tidak punya akses DB / request tidak ada), `409 already_decided`, `409 invalid_state` (request tidak lagi `pending_review`), `422 validation_error` (reject tanpa `comment`).
- **Response `200 OK`:** Object `ApprovalRequest` terbaru.

#### 6) Keterkaitan dengan Enqueue (`Execute` / `Rollback` / `Forward` / `Resume` / `Revert`)
- Saat `REQUIRE_APPROVAL=true` (aktif di production/demo M2):
  - Pemanggilan `POST .../execute`, `/rollback`, `/forward`, `/resume`, atau `/revert` memeriksa keberadaan `ApprovalRequest` berstatus `"approved"` untuk `(database_id, migration_id, action)`.
  - Jika belum approved → balas `403` dengan `code: "approval_required"`.
  - Saat job berhasil di-enqueue, `ApprovalRequest` ditandai `"consumed"` secara **atomik** (satu approval = satu job, aman terhadap request paralel) agar aksi rollback/forward/resume berikutnya tetap memerlukan approval baru.
  - Klaim approval diikat ke `idempotency_key` job: retry dengan key yang sama tetap dideduplikasi (bukan `403`), dan worker merevalidasi approval di bawah lock.
  - Endpoint legacy M1 yang mengeksekusi SQL secara sinkron (`POST /api/databases/{dbID}/migrations`, `POST .../migration-drafts/{id}/execute`) dibalas `403 approval_required`.
- Saat `REQUIRE_APPROVAL=false`, endpoint approval tetap berfungsi (informasional) tetapi enqueue tidak diblokir.

#### 7) `[TAMBAHAN]` Riwayat Approval & Timeline per Migration
- `GET /api/databases/{dbID}/migrations/definitions/{migrationID}/approval-requests` (`View`) → `{ approval_requests }` seluruh pengajuan migration tersebut.
- `GET /api/databases/{dbID}/migrations/definitions/{migrationID}/history?hide_rollback=true|false` (`View`) → `{ definition, hide_rollback, executions, approval_requests }` — seluruh attempt (termasuk gagal) + seluruh pengajuan approval.

#### 8) `[TAMBAHAN]` Filter Riwayat Eksekusi
`GET /api/databases/{dbID}/migrations/executions` kini menerima query opsional (filter diterapkan **setelah** `hide_rollback`, sehingga pasangan eksekusi–rollback tetap ditentukan dari riwayat lengkap):

| Query | Contoh | Keterangan |
|---|---|---|
| `hide_rollback` | `true` | Seperti sebelumnya |
| `migration_id` | `12` | |
| `status` | `success,failed` | `pending, running, success, failed, partial, partially_reverted, reverted, uncertain` |
| `action` | `up,rollback` | `up, rollback, forward, batch_up, batch_resume, batch_revert` |
| `actor` | `bob` | case-insensitive |
| `since` / `until` | `2026-11-26T05:00:00Z` | RFC3339 |
| `limit` / `offset` | `50` / `0` | maks 1000 |

Response: `{ hide_rollback, executions, total, limit, offset, summary: { total_attempts, by_status, by_action, active_version } }` (field lama tetap ada).

---

### 2.3 Penyempurnaan Minor & Integrasi SPA Serving

| No | Item | Detail | Prioritas |
|---|---|---|---|
| 1 | **SPA Static & Fallback** | Server Go men-serve `client/dist/` dan mengembalikan `index.html` untuk semua `GET` non-`/api/*`, menggantikan HTML template M1. Route `/api/*` tak dikenal tetap JSON `404`. | Wajib (sebelum demo) |
| 2 | **Delete Draft Definition** ✅ | `DELETE /api/databases/{dbID}/migrations/definitions/{migrationID}` (`View+Edit`; hanya jika `!is_locked_immutable` dan belum punya job/eksekusi, balas `204 No Content`, atau `409 migration_immutable`). | Opsional (UX) |
| 3 | **Self-demote Guard** ✅ | `PUT /api/admin/users/{id}` menolak (`400`) jika admin mencoba mencabut `is_platform_admin` miliknya sendiri. | Disarankan |
| 4 | **`via_groups` di Detail DB** ✅ | `GET /api/databases/{dbID}` menyertakan `via_groups` dan `access_groups: [{ group_id, group_name, access_level, leader_user_id, leader_username }]` (siapa saja yang harus approve). | Opsional |

---

## 3. Referensi Ringkas API Existing (Sudah Ada di `main`)

### 3.1 Auth, CSRF & Health
| Method & Path | Request / Query | Response Sukses |
|---|---|---|
| `GET /api/health` | — | `200` `{ status, timestamp }` |
| `GET /api/csrf-token` | — | `200` `{ csrf_token }` (+ set cookie `csrf_token`) |
| `POST /api/auth/login` | `{ username, password }` | `200` `{ message, user }` (+ set `app_session_id` & rotasi `csrf_token`) |
| `POST /api/auth/logout` | — | `200` `{ message }` (+ revoke sesi & clear cookie) |
| `GET /api/auth/me` | — | `200` `User` |
| `GET /api/authz/me` | — | `200` `{ id, username, is_platform_admin, permissions, led_groups }` |

### 3.2 Platform Admin — Users, Groups, Grants, Connections
*(Semua route `/api/admin/*` wajib Platform Admin)*

| Domain | Method & Path | Payload / Catatan | Response |
|---|---|---|---|
| **Users** | `GET /api/admin/users` | — | `200` `{ users: User[] }` |
| | `POST /api/admin/users` | `{ username, password, is_platform_admin }` | `201` `{ message, user }` |
| | `GET /api/admin/users/{id}` | — | `200` `{ user }` |
| | `PUT /api/admin/users/{id}` | `{ username, is_platform_admin, is_active }` | `200` `{ message, user }` |
| | `POST /api/admin/users/{id}/reset-password` | `{ new_password }` | `200` `{ message }` |
| | `POST /api/admin/users/{id}/revoke-sessions` | — | `200` `{ message }` |
| | `DELETE /api/admin/users/{id}` | — | `200` `{ message }` |
| **Groups** | `GET /api/admin/groups` | — | `200` `{ groups: Group[] }` |
| | `POST /api/admin/groups` | `{ name, description }` | `201` `Group` |
| | `GET /api/admin/groups/{groupID}` | — | `200` `{ ...Group, members: Member[], grants: Grant[] }` |
| | `PUT /api/admin/groups/{groupID}` | `{ name, description }` | `200` `Group` |
| | `DELETE /api/admin/groups/{groupID}` | — | `204` |
| **Members** | `GET /api/admin/groups/{groupID}/members` | — | `200` `{ members: Member[] }` |
| | `POST /api/admin/groups/{groupID}/members` | `{ user_id }` (UUID) | `201`/`200` `{ members }` |
| | `DELETE /api/admin/groups/{groupID}/members/{userID}` | — | `204` |
| **Grants** | `GET /api/admin/groups/{groupID}/grants` | — | `200` `{ grants: Grant[] }` |
| | `PUT /api/admin/groups/{groupID}/grants/{connectionID}` | `{ access_level: "view_only" \| "view_edit" }` | `200` `{ grants }` |
| | `DELETE /api/admin/groups/{groupID}/grants/{connectionID}` | — | `204` |
| **Connections** | `GET /api/admin/connections` | — | `200` `{ connections: Connection[] }` |
| | `POST /api/admin/connections` | `{ name, description, host, port, database_name, username, password, ssl_mode, test_before_save }` | `201` `Connection` |
| | `POST /api/admin/connections/test` | payload koneksi (+ `connection_id` opsional saat edit) | `200` `TestResult` |
| | `GET /api/admin/connections/{connectionID}` | — | `200` `Connection` *(tanpa password)* |
| | `PUT /api/admin/connections/{connectionID}` | sama seperti create (`password` opsional jika target tidak berubah) | `200` `Connection` |
| | `DELETE /api/admin/connections/{connectionID}` | — | `204` |
| | `POST /api/admin/connections/{connectionID}/test` | — | `200` `TestResult` |

### 3.3 Databases & DB Viewer
| Method & Path | Query | Response |
|---|---|---|
| `GET /api/databases` | — | `200` `{ databases: [{ id, name, description, database_name, access_level, access_label, permissions, via_groups }] }` |
| `GET /api/databases/{dbID}` | — | `200` `{ id, name, description, database_name, access_level, access_label, permissions }` |
| `GET /api/databases/{dbID}/status` | — | `200` `{ ok: true }` atau `503` |
| `GET /api/databases/{dbID}/schema` | `?schema=public` | `200` `{ database_id, schemas, selected_schema, tables: [{ name, type, columns, constraints, indexes }] }` |
| `GET /api/databases/{dbID}/tables/{table}/data` | `?schema=public&limit=50&offset=0` (`limit` 1..100) | `200` `{ schema, table, columns, rows, total, limit, offset }` |

### 3.4 Migration Definitions, Async Jobs, History & EXPLAIN
| Method & Path | Permission | Payload / Query | Response |
|---|---|---|---|
| `GET /api/databases/{dbID}/migrations/definitions` | `View` | — | `200` `{ migrations: Definition[] }` |
| `POST /api/databases/{dbID}/migrations/definitions` | `View+Edit` | `{ name, sql_up, sql_down, is_irreversible, irreversible_reason, execution_mode, batch_config }` | `201` `Definition` |
| `GET /api/databases/{dbID}/migrations/definitions/{migrationID}` | `View` | — | `200` `Definition` |
| `PUT /api/databases/{dbID}/migrations/definitions/{migrationID}` | `View+Edit` | `{ name, sql_up, sql_down, is_irreversible, irreversible_reason, execution_mode, batch_config }` | `200` `Definition` *(balas `409` jika immutable)* |
| `POST /api/databases/{dbID}/migrations/definitions/{migrationID}/execute` | `View+Edit` | `{ idempotency_key?, max_attempts? }` | `202` `{ message, job_id, deduplicated, job }` |
| `POST /api/databases/{dbID}/migrations/definitions/{migrationID}/rollback` | `View+Edit` | `{ idempotency_key?, max_attempts? }` | `202` `{ message, job_id, deduplicated, job }` |
| `POST /api/databases/{dbID}/migrations/definitions/{migrationID}/forward` | `View+Edit` | `{ idempotency_key?, max_attempts? }` | `202` `{ message, job_id, deduplicated, job }` |
| `GET /api/databases/{dbID}/migrations/executions` | `View` | `?hide_rollback=true\|false` | `200` `{ hide_rollback, executions: Execution[] }` |
| `GET /api/databases/{dbID}/migrations/executions/{executionID}` | `View` | — | `200` `Execution` *(termasuk `checkpoints[]`)* |
| `POST /api/databases/{dbID}/migrations/executions/{executionID}/resume` | `View+Edit` | `{ idempotency_key?, max_attempts? }` | `202` `{ message, job_id, deduplicated, job }` |
| `POST /api/databases/{dbID}/migrations/executions/{executionID}/revert` | `View+Edit` | `{ idempotency_key?, max_attempts? }` | `202` `{ message, job_id, deduplicated, job }` |
| `POST /api/databases/{dbID}/migrations/executions/{executionID}/reconcile` | `View+Edit` | — | `200` `{ message, execution }` |
| `GET /api/databases/{dbID}/jobs` | `View` | `?limit=50` | `200` `{ jobs: Job[] }` |
| `GET /api/databases/{dbID}/jobs/{jobID}` | `View` | — | `200` `{ job: Job, execution?: Execution }` |
| `GET /api/databases/{dbID}/lock` | `View` | — | `200` `LockInfo` |
| `POST /api/databases/{dbID}/migrations/explain` | `View` (`analyze: true` butuh `View+Edit`) | `{ sql, analyze }` | `200` `{ database_id, total_queries, results }` |

#### Catatan Field Penting pada `Definition` & `BatchConfig`
- `Definition` mengembalikan flag siap pakai untuk UI: `workflow_status`, `is_locked_immutable`, `is_active`, `active_execution_id`, `can_rollback`, `can_forward`.
- `BatchConfig`:
  ```json
  {
    "schema": "public",
    "table": "orders",
    "cursor_column": "id",
    "batch_size": 5000,
    "where_clause": "status = 'pending'",
    "capture_columns": ["status"],
    "batch_delay_ms": 0
  }
  ```
- Placeholder wajib pada SQL mode `batch`: minimal satu dari `{{batch_ids}}`, `{{cursor_start}}`, `{{cursor_end}}`, `{{last_cursor}}` (dan `{{recovery_json}}` didukung pada `sql_down`).
- Status terminal `Job`: `completed`, `failed`, `partial`, `partially_reverted`, `uncertain`, `cancelled` (hentikan polling saat mencapai salah satu status ini).
