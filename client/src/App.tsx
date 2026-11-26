import { Navigate, Route, Routes } from 'react-router-dom';
import { PublicOnly, RequireAdmin, RequireAuth } from '@/components/RouteGuards';
import { DatabaseLayout } from '@/components/layout/DatabaseLayout';
import { LoginPage } from '@/pages/LoginPage';
import { DatabasesPage } from '@/pages/DatabasesPage';
import { DatabaseOverviewPage } from '@/pages/database/DatabaseOverviewPage';
import { DatabaseViewerPage } from '@/pages/database/DatabaseViewerPage';
import { MigrationDefinitionsPage } from '@/pages/database/MigrationDefinitionsPage';
import { MigrationHistoryPage } from '@/pages/database/MigrationHistoryPage';
import { DatabaseJobsPage } from '@/pages/database/DatabaseJobsPage';
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

        <Route path="/databases/:dbId" element={<DatabaseLayout />}>
          <Route index element={<DatabaseOverviewPage />} />
          <Route path="viewer" element={<DatabaseViewerPage />} />
          <Route path="migrations" element={<MigrationDefinitionsPage />} />
          <Route path="migrations/history" element={<MigrationHistoryPage />} />
          <Route path="jobs" element={<DatabaseJobsPage />} />
        </Route>

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
