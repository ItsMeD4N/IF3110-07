import { Navigate, Route, Routes } from 'react-router-dom';
import { PublicOnly, RequireAdmin, RequireAuth } from '@/components/RouteGuards';
import { LoginPage } from '@/pages/LoginPage';
import { DatabasesPage } from '@/pages/DatabasesPage';
import { ModulePlaceholderPage } from '@/pages/ModulePlaceholderPage';
import { NotFoundPage } from '@/pages/NotFoundPage';

export function App() {
  return (
    <Routes>
      <Route element={<PublicOnly />}>
        <Route path="/login" element={<LoginPage />} />
      </Route>

      <Route element={<RequireAuth />}>
        <Route path="/" element={<Navigate to="/databases" replace />} />
        <Route path="/databases" element={<DatabasesPage />} />
        <Route
          path="/databases/:dbId"
          element={
            <ModulePlaceholderPage
              title="Ringkasan Database"
              description="Status koneksi target, informasi lock aktif, hak akses efektif, dan pintasan menuju viewer, migrations, dan jobs."
              endpoints={['GET /api/databases/:dbId', 'GET /api/databases/:dbId/status', 'GET /api/databases/:dbId/lock']}
            />
          }
        />
        <Route
          path="/databases/:dbId/viewer"
          element={
            <ModulePlaceholderPage
              title="Database Viewer"
              description="Eksplorasi schema, tabel, kolom, constraint, index, serta pagination isi tabel PostgreSQL."
              endpoints={['GET /api/databases/:dbId/schema', 'GET /api/databases/:dbId/tables/:table/data']}
            />
          }
        />
        <Route
          path="/databases/:dbId/migrations"
          element={
            <ModulePlaceholderPage
              title="Migration Definitions & Editor"
              description="Buat/edit definisi SQL up/down (atau irreversible), mode normal/batch, EXPLAIN/EXPLAIN ANALYZE, ajukan review, serta eksekusi async."
              endpoints={[
                'GET/POST /api/databases/:dbId/migrations/definitions',
                'POST .../execute',
                'POST .../rollback',
                'POST .../forward',
                'POST .../explain',
              ]}
            />
          }
        />
        <Route
          path="/databases/:dbId/migrations/history"
          element={
            <ModulePlaceholderPage
              title="Migration Execution History"
              description="Riwayat lengkap eksekusi (sukses, gagal, rollback, forward, batch) dengan filter Hide rollback dan detail SQL."
              endpoints={[
                'GET /api/databases/:dbId/migrations/executions?hide_rollback=...',
                'POST .../resume',
                'POST .../revert',
                'POST .../reconcile',
              ]}
            />
          }
        />
        <Route
          path="/databases/:dbId/jobs"
          element={
            <ModulePlaceholderPage
              title="Async Jobs & Lock Monitor"
              description="Pantau antrean job migration, progres batch & checkpoint secara live, serta status lock database."
              endpoints={['GET /api/databases/:dbId/jobs', 'GET /api/databases/:dbId/jobs/:jobId', 'GET /api/databases/:dbId/lock']}
            />
          }
        />
        <Route
          path="/approvals"
          element={
            <ModulePlaceholderPage
              title="Approval Workflow"
              description="Daftar permintaan review migration/rollback/forward yang menunggu persetujuan leader setiap group."
              endpoints={['GET /api/approvals/pending', 'POST .../approve', 'POST .../reject']}
            />
          }
        />

        <Route element={<RequireAdmin />}>
          <Route
            path="/admin/users"
            element={
              <ModulePlaceholderPage
                title="Manajemen Pengguna"
                description="Kelola akun pengguna, status admin/aktif, reset password, dan pencabutan sesi."
                endpoints={['GET/POST /api/admin/users', 'PUT/DELETE /api/admin/users/:id']}
                parentTo="/admin/users"
                parentLabel="Platform Admin"
              />
            }
          />
          <Route
            path="/admin/groups"
            element={
              <ModulePlaceholderPage
                title="Group & Akses Database"
                description="Kelola group, penetapan leader group, anggota, dan hak akses View Only / View + Edit per database."
                endpoints={['GET/POST /api/admin/groups', 'PUT .../leader', 'POST/DELETE .../members', 'PUT/DELETE .../grants']}
                parentTo="/admin/groups"
                parentLabel="Platform Admin"
              />
            }
          />
          <Route
            path="/admin/connections"
            element={
              <ModulePlaceholderPage
                title="Koneksi Database"
                description="Kelola koneksi PostgreSQL terenkripsi dan uji konektivitas sebelum/sesudah penyimpanan."
                endpoints={['GET/POST /api/admin/connections', 'POST .../test', 'PUT/DELETE /api/admin/connections/:id']}
                parentTo="/admin/connections"
                parentLabel="Platform Admin"
              />
            }
          />
        </Route>

        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  );
}
