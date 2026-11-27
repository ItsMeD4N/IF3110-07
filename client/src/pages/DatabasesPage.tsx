import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ArrowRight,
  Check,
  Database,
  GitBranch,
  History,
  Minus,
  RefreshCw,
  Table2,
} from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { ApiError } from '@/lib/api';
import { listMyDatabases } from '@/services/databases';
import type { MyDatabase } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { AccessBadge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { CardGridSkeleton } from '@/components/ui/Skeleton';

export function DatabasesPage() {
  const { user } = useAuth();
  const [databases, setDatabases] = useState<MyDatabase[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const loadDatabases = useCallback(async (signal?: AbortSignal) => {
    setLoading(true);
    setError('');
    try {
      const data = await listMyDatabases(signal);
      setDatabases(data);
    } catch (err) {
      if (err instanceof DOMException && err.name === 'AbortError') return;
      setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar database.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    void loadDatabases(controller.signal);
    return () => controller.abort();
  }, [loadDatabases]);

  return (
    <div>
      <PageHeader
        title="Database Saya"
        description="Pilih database PostgreSQL yang dapat kamu akses melalui keanggotaan group untuk meninjau schema, data, dan menjalankan migration."
        actions={
          <Button variant="secondary" size="sm" onClick={() => void loadDatabases()} loading={loading}>
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Muat Ulang</span>
          </Button>
        }
      />

      {error && (
        <Alert variant="error" className="mb-5">
          {error}
        </Alert>
      )}

      {loading ? (
        <CardGridSkeleton count={3} />
      ) : databases.length === 0 ? (
        <EmptyState
          icon={Database}
          title="Belum ada database yang dapat diakses"
          description={
            user?.is_platform_admin
              ? 'Akun Platform Admin tidak otomatis memiliki akses ke database target. Tambahkan koneksi dan berikan akses melalui Group & Akses.'
              : 'Akun kamu belum tergabung dalam group yang memiliki akses database. Hubungi Platform Admin untuk mendapatkan akses.'
          }
          action={
            user?.is_platform_admin ? (
              <Link
                to="/admin/groups"
                className="inline-flex min-h-9 items-center gap-2 rounded-md bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-ink no-underline hover:bg-accent-hover"
              >
                <span>Kelola Group &amp; Akses</span>
                <ArrowRight className="h-4 w-4" aria-hidden="true" />
              </Link>
            ) : undefined
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2 xl:grid-cols-3">
          {databases.map((db) => {
            const canEdit = db.access_level === 'view_edit';
            return (
              <article key={db.id} className="card-surface flex min-w-0 flex-col p-5">
                <div className="flex items-start justify-between gap-3">
                  <div className="min-w-0">
                    <h2 className="truncate text-base font-semibold text-ink">{db.name}</h2>
                    <p className="mt-0.5 font-mono text-xs text-muted">{db.database_name}</p>
                  </div>
                  <AccessBadge level={db.access_level} label={db.access_label} />
                </div>

                {db.description && (
                  <p className="mt-2.5 text-sm leading-relaxed text-muted">{db.description}</p>
                )}

                {db.via_groups && db.via_groups.length > 0 && (
                  <p className="mt-2.5 text-xs text-muted">
                    Melalui group:{' '}
                    <span className="font-medium text-ink">{db.via_groups.join(', ')}</span>
                  </p>
                )}

                <ul className="mt-3 space-y-1 border-t border-border pt-3 text-xs">
                  <li className="flex items-center gap-2 text-ink">
                    <Check className="h-3.5 w-3.5 shrink-0 text-ok" aria-hidden="true" />
                    <span>Lihat schema, tabel, data, &amp; riwayat migration</span>
                  </li>
                  <li className={`flex items-center gap-2 ${canEdit ? 'text-ink' : 'text-muted line-through'}`}>
                    {canEdit ? (
                      <Check className="h-3.5 w-3.5 shrink-0 text-ok" aria-hidden="true" />
                    ) : (
                      <Minus className="h-3.5 w-3.5 shrink-0 text-muted" aria-hidden="true" />
                    )}
                    <span>Buat, ajukan review, &amp; jalankan migration / rollback</span>
                  </li>
                </ul>

                <div className="mt-4 flex flex-wrap gap-2 border-t border-border pt-4">
                  <Link
                    to={`/databases/${db.id}/viewer`}
                    className="inline-flex min-h-8 flex-1 items-center justify-center gap-1.5 rounded-md border border-border bg-surface px-2.5 py-1 text-xs font-medium text-ink no-underline hover:bg-surface-subtle"
                  >
                    <Table2 className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Viewer</span>
                  </Link>
                  <Link
                    to={`/databases/${db.id}/migrations`}
                    className="inline-flex min-h-8 flex-1 items-center justify-center gap-1.5 rounded-md bg-accent px-2.5 py-1 text-xs font-medium text-accent-ink no-underline hover:bg-accent-hover"
                  >
                    <GitBranch className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Migrations</span>
                  </Link>
                </div>

                <div className="mt-2 flex items-center justify-between gap-2 text-xs text-muted">
                  <Link
                    to={`/databases/${db.id}/migrations/history`}
                    className="inline-flex items-center gap-1 hover:text-ink hover:underline"
                  >
                    <History className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>History</span>
                  </Link>
                  <Link
                    to={`/databases/${db.id}/jobs`}
                    className="inline-flex items-center gap-1 hover:text-ink hover:underline"
                  >
                    <Activity className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Jobs &amp; Lock</span>
                  </Link>
                  <Link
                    to={`/databases/${db.id}`}
                    className="inline-flex items-center gap-1 font-medium text-accent hover:underline"
                  >
                    <span>Detail</span>
                    <ArrowRight className="h-3 w-3" aria-hidden="true" />
                  </Link>
                </div>
              </article>
            );
          })}
        </div>
      )}
    </div>
  );
}
