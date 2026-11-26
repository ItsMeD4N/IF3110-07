import { useCallback, useEffect, useState } from 'react';
import { Link, NavLink, Outlet, useOutletContext, useParams } from 'react-router-dom';
import {
  Activity,
  ArrowLeft,
  GitBranch,
  History,
  LayoutDashboard,
  Lock,
  ShieldAlert,
  Table2,
} from 'lucide-react';
import { ApiError } from '@/lib/api';
import { getMyDatabase } from '@/services/databases';
import { getLockInfo } from '@/services/migrations';
import type { LockInfo, MyDatabase } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { AccessBadge, Badge } from '@/components/ui/Badge';
import { SkeletonLine } from '@/components/ui/Skeleton';

export interface DatabaseOutletContext {
  database: MyDatabase;
  lockInfo: LockInfo | null;
  canEdit: boolean;
  refreshLock: () => Promise<LockInfo | null>;
  refreshDatabase: () => Promise<void>;
}

export function useDatabaseContext(): DatabaseOutletContext {
  return useOutletContext<DatabaseOutletContext>();
}

export function DatabaseLayout() {
  const { dbId } = useParams();
  const parsedDbId = Number(dbId);

  const [database, setDatabase] = useState<MyDatabase | null>(null);
  const [lockInfo, setLockInfo] = useState<LockInfo | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const refreshLock = useCallback(async () => {
    if (!Number.isInteger(parsedDbId) || parsedDbId <= 0) return null;
    try {
      const info = await getLockInfo(parsedDbId);
      setLockInfo(info);
      return info;
    } catch {
      return null;
    }
  }, [parsedDbId]);

  const refreshDatabase = useCallback(async () => {
    if (!Number.isInteger(parsedDbId) || parsedDbId <= 0) {
      setError('ID database tidak valid.');
      setLoading(false);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const [db, lock] = await Promise.all([
        getMyDatabase(parsedDbId),
        getLockInfo(parsedDbId).catch(() => null),
      ]);
      setDatabase(db);
      setLockInfo(lock);
    } catch (err) {
      setDatabase(null);
      setError(
        err instanceof ApiError
          ? err.message
          : 'Database tidak ditemukan atau kamu tidak memiliki akses.',
      );
    } finally {
      setLoading(false);
    }
  }, [parsedDbId]);

  useEffect(() => {
    void refreshDatabase();
  }, [refreshDatabase]);

  if (loading) {
    return (
      <div className="space-y-4" role="status" aria-label="Memuat workspace database">
        <SkeletonLine className="h-4 w-36" />
        <SkeletonLine className="h-8 w-64" />
        <SkeletonLine className="h-10 w-full" />
        <div className="card-surface space-y-3 p-5">
          <SkeletonLine className="h-5 w-48" />
          <SkeletonLine className="h-4 w-full" />
          <SkeletonLine className="h-4 w-2/3" />
        </div>
      </div>
    );
  }

  if (error || !database) {
    return (
      <div className="space-y-4">
        <Alert variant="error" title="Tidak dapat membuka database">
          {error || 'Database tidak ditemukan atau kamu tidak memiliki akses.'}
        </Alert>
        <Link
          to="/databases"
          className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border bg-surface px-3.5 py-1.5 text-sm font-medium text-ink no-underline hover:bg-surface-subtle"
        >
          <ArrowLeft className="h-4 w-4" aria-hidden="true" />
          <span>Kembali ke Database Saya</span>
        </Link>
      </div>
    );
  }

  const canEdit = database.access_level === 'view_edit';
  const isBlocked =
    lockInfo?.locked &&
    (lockInfo.state === 'blocked_partial' || lockInfo.state === 'blocked_uncertain');
  const isHeld = lockInfo?.locked && lockInfo.state === 'held';

  const basePath = `/databases/${database.id}`;

  return (
    <div>
      <PageHeader
        title={database.name}
        description={database.description || `Database target PostgreSQL: ${database.database_name}`}
        breadcrumbs={[
          { label: 'Database Saya', to: '/databases' },
          { label: database.name },
        ]}
        actions={
          <div className="flex flex-wrap items-center gap-2">
            <Badge tone="neutral" className="font-mono">
              {database.database_name}
            </Badge>
            <AccessBadge level={database.access_level} label={database.access_label} />
            {isBlocked && (
              <Badge tone="danger">
                <ShieldAlert className="h-3 w-3" aria-hidden="true" />
                <span>
                  {lockInfo.state === 'blocked_partial' ? 'Blocked (Partial)' : 'Blocked (Uncertain)'}
                </span>
              </Badge>
            )}
            {isHeld && (
              <Badge tone="warn">
                <Lock className="h-3 w-3" aria-hidden="true" />
                <span>Migration Berjalan</span>
              </Badge>
            )}
          </div>
        }
      />

      {/* Semantic page sub-navigation (uses NavLink aria-current="page", NOT WAI-ARIA tablist) */}
      <nav aria-label="Navigasi workspace database" className="tab-strip mb-6">
        <NavLink to={basePath} end className="tab-item">
          <LayoutDashboard className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span>Overview</span>
        </NavLink>
        <NavLink to={`${basePath}/viewer`} className="tab-item">
          <Table2 className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span>Viewer</span>
        </NavLink>
        <NavLink to={`${basePath}/migrations`} end className="tab-item">
          <GitBranch className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span>Migrations</span>
        </NavLink>
        <NavLink to={`${basePath}/migrations/history`} className="tab-item">
          <History className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span>History</span>
        </NavLink>
        <NavLink to={`${basePath}/jobs`} className="tab-item">
          <Activity className="h-4 w-4 shrink-0" aria-hidden="true" />
          <span>Jobs &amp; Lock</span>
        </NavLink>
      </nav>

      <Outlet
        context={
          {
            database,
            lockInfo,
            canEdit,
            refreshLock,
            refreshDatabase,
          } satisfies DatabaseOutletContext
        }
      />
    </div>
  );
}
