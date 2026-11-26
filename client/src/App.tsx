import { Navigate, Route, Routes } from 'react-router-dom';
import { PublicOnly, RequireAdmin, RequireAuth } from '@/components/RouteGuards';
import { LoginPage } from '@/pages/LoginPage';
import { DatabasesPage } from '@/pages/DatabasesPage';
import { AdminUsersPage } from '@/pages/admin/AdminUsersPage';
import { AdminGroupsPage } from '@/pages/admin/AdminGroupsPage';
import { AdminGroupDetailPage } from '@/pages/admin/AdminGroupDetailPage';
import { AdminConnectionsPage } from '@/pages/admin/AdminConnectionsPage';
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
          <Route path="/admin/users" element={<AdminUsersPage />} />
          <Route path="/admin/groups" element={<AdminGroupsPage />} />
          <Route path="/admin/groups/:groupId" element={<AdminGroupDetailPage />} />
          <Route path="/admin/connections" element={<AdminConnectionsPage />} />
        </Route>

        <Route path="*" element={<NotFoundPage />} />
      </Route>
    </Routes>
  );
}
