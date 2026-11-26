import { Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '@/context/AuthContext';
import { AppShell } from '@/components/layout/AppShell';
import { CardGridSkeleton, SkeletonLine } from '@/components/ui/Skeleton';
import { Alert } from '@/components/ui/Alert';
import { Link } from 'react-router-dom';

function SessionLoadingScreen() {
  return (
    <div className="mx-auto max-w-4xl px-6 py-12" role="status" aria-label="Memeriksa sesi">
      <SkeletonLine className="mb-3 h-7 w-48" />
      <SkeletonLine className="mb-8 h-4 w-80" />
      <CardGridSkeleton count={3} />
    </div>
  );
}

export function RequireAuth() {
  const { user, loading } = useAuth();
  const location = useLocation();

  if (loading) {
    return <SessionLoadingScreen />;
  }

  if (!user) {
    return <Navigate to="/login" replace state={{ from: location.pathname + location.search }} />;
  }

  return (
    <AppShell>
      <Outlet />
    </AppShell>
  );
}

export function RequireAdmin() {
  const { user } = useAuth();

  if (!user?.is_platform_admin) {
    return (
      <div className="mx-auto max-w-lg py-10">
        <Alert variant="error" title="Akses Platform Admin diperlukan">
          Halaman ini hanya dapat diakses oleh Platform Admin.{' '}
          <Link to="/databases" className="font-semibold underline">
            Kembali ke Database Saya
          </Link>
        </Alert>
      </div>
    );
  }

  return <Outlet />;
}

export function PublicOnly() {
  const { user, loading } = useAuth();

  if (loading) {
    return <SessionLoadingScreen />;
  }

  if (user) {
    return <Navigate to={user.is_platform_admin ? '/admin/users' : '/databases'} replace />;
  }

  return <Outlet />;
}
