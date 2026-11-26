import { useCallback, useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ArrowRight,
  CheckCircle2,
  GitBranch,
  History,
  Lock,
  LockOpen,
  RefreshCw,
  ShieldAlert,
  ShieldCheck,
  Table2,
  XCircle,
} from 'lucide-react';
import { useDatabaseContext } from '@/components/layout/DatabaseLayout';
import { ApiError } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import { checkDatabaseStatus } from '@/services/databases';
import { Alert } from '@/components/ui/Alert';
import { AccessBadge, Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';

const PERMISSION_LABELS: Record<string, string> = {
  'db.schema.view': 'Melihat daftar schema, tabel, kolom, constraint, dan index',
  'db.data.view': 'Melihat isi data tabel secara read-only',
  'db.migrations.view': 'Melihat definisi, riwayat migration, status job, dan EXPLAIN biasa',
  'db.migrations.create': 'Membuat definisi migration baru (SQL up/down atau irreversible)',
  'db.migrations.edit': 'Mengubah definisi draft migration sebelum dieksekusi',
  'db.migrations.execute': 'Menjalankan EXPLAIN ANALYZE, mengajukan review, serta enqueue execute/rollback/forward',
};

export function DatabaseOverviewPage() {
  const { database, lockInfo, canEdit, refreshLock } = useDatabaseContext();

  const [checkingStatus, setCheckingStatus] = useState(true);
  const [connOk, setConnOk] = useState<boolean | null>(null);
  const [connLatencyMs, setConnLatencyMs] = useState<number | null>(null);
  const [connError, setConnError] = useState('');

  const runHealthCheck = useCallback(async () => {
    setCheckingStatus(true);
    setConnError('');
    const started = performance.now();
    try {
      const res = await checkDatabaseStatus(database.id);
      setConnOk(res.ok);
      setConnLatencyMs(Math.round(performance.now() - started));
    } catch (err) {
      setConnOk(false);
      setConnLatencyMs(null);
      setConnError(
        err instanceof ApiError
          ? err.message
          : 'Database target tidak dapat dijangkau saat ini.',
      );
    } finally {
      setCheckingStatus(false);
    }
  }, [database.id]);

  useEffect(() => {
    void runHealthCheck();
  }, [runHealthCheck]);

  const handleRefreshAll = async () => {
    await Promise.all([runHealthCheck(), refreshLock()]);
  };

  const basePath = `/databases/${database.id}`;

  return (
    <div className="space-y-6">
      {lockInfo?.locked &&
        (lockInfo.state === 'blocked_partial' || lockInfo.state === 'blocked_uncertain') && (
          <Alert
            variant="error"
            title={
              lockInfo.state === 'blocked_partial'
                ? 'Database tertahan karena migration batch berstatus partial'
                : 'Database tertahan karena hasil commit sebelumnya belum pasti (uncertain)'
            }
          >
            <p>
              {lockInfo.reason ||
                'Migration baru pada database ini ditahan sampai execution sebelumnya dilanjutkan (resume), dibatalkan (revert), atau direkonsiliasi.'}
            </p>
            <div className="mt-2">
              <Link
                to={`${basePath}/migrations/history`}
                className="font-semibold underline"
              >
                Buka Migration History untuk meninjau Execution #{lockInfo.execution_id ?? '—'} →
              </Link>
            </div>
          </Alert>
        )}

      <div className="grid gap-5 lg:grid-cols-2">
        {/* Status Koneksi & Lock */}
        <section className="card-surface p-5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-base font-semibold text-ink">Status Koneksi &amp; Lock</h2>
            <Button
              variant="secondary"
              size="sm"
              loading={checkingStatus}
              onClick={() => void handleRefreshAll()}
            >
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Cek Ulang</span>
            </Button>
          </div>

          <dl className="mt-4 divide-y divide-border text-sm">
            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted">Nama Database</dt>
              <dd className="font-mono font-medium text-ink">{database.database_name}</dd>
            </div>

            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted">Konektivitas Target</dt>
              <dd>
                {checkingStatus ? (
                  <Badge tone="neutral">Memeriksa…</Badge>
                ) : connOk ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-ok tabular-nums">
                    <CheckCircle2 className="h-4 w-4" aria-hidden="true" />
                    <span>Terhubung{connLatencyMs !== null ? ` (${connLatencyMs} ms)` : ''}</span>
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 font-medium text-danger">
                    <XCircle className="h-4 w-4" aria-hidden="true" />
                    <span>Tidak dapat dijangkau</span>
                  </span>
                )}
              </dd>
            </div>

            <div className="flex items-center justify-between gap-4 py-2.5">
              <dt className="text-muted">Status Lock Migration</dt>
              <dd>
                {!lockInfo || !lockInfo.locked ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-ok">
                    <LockOpen className="h-4 w-4" aria-hidden="true" />
                    <span>Bebas (siap menjalankan migration)</span>
                  </span>
                ) : lockInfo.state === 'held' ? (
                  <span className="inline-flex items-center gap-1.5 font-medium text-warn">
                    <Lock className="h-4 w-4" aria-hidden="true" />
                    <span>Dikunci oleh worker {lockInfo.owner_id}</span>
                  </span>
                ) : (
                  <span className="inline-flex items-center gap-1.5 font-medium text-danger">
                    <ShieldAlert className="h-4 w-4" aria-hidden="true" />
                    <span>{lockInfo.state}</span>
                  </span>
                )}
              </dd>
            </div>

            {lockInfo?.locked && (
              <>
                {lockInfo.job_id && (
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-muted">Job ID Aktif</dt>
                    <dd className="truncate font-mono text-xs text-ink">{lockInfo.job_id}</dd>
                  </div>
                )}
                {lockInfo.expires_at && (
                  <div className="flex items-center justify-between gap-4 py-2.5">
                    <dt className="text-muted">Lease Berakhir</dt>
                    <dd className="text-xs text-ink tabular-nums">
                      {formatDateTime(lockInfo.expires_at)}
                    </dd>
                  </div>
                )}
              </>
            )}
          </dl>

          {connError && (
            <Alert variant="error" className="mt-3">
              {connError}
            </Alert>
          )}
        </section>

        {/* Hak Akses Efektif */}
        <section className="card-surface p-5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="inline-flex items-center gap-2 text-base font-semibold text-ink">
              <ShieldCheck className="h-4 w-4 text-accent" aria-hidden="true" />
              <span>Hak Akses Efektif Kamu</span>
            </h2>
            <AccessBadge level={database.access_level} label={database.access_label} />
          </div>

          <p className="mt-2 text-xs leading-relaxed text-muted">
            {canEdit
              ? 'Kamu memiliki izin penuh untuk melihat schema/data, menjalankan EXPLAIN ANALYZE, menyusun definisi migration, serta memicu eksekusi/rollback/forward.'
              : 'Kamu memiliki akses baca (View Only) pada database ini. Jika kamu adalah leader group, kamu tetap dapat meninjau dan menyetujui/menolak permintaan review migration.'}
          </p>

          <ul className="mt-4 space-y-2 border-t border-border pt-3.5 text-sm">
            {database.permissions.map((perm) => (
              <li key={perm} className="flex items-start gap-2 text-ink">
                <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-ok" aria-hidden="true" />
                <span>{PERMISSION_LABELS[perm] ?? perm}</span>
              </li>
            ))}
          </ul>
        </section>
      </div>

      {/* Pintasan Aksi Cepat */}
      <section className="card-surface p-5">
        <h2 className="text-base font-semibold text-ink">Mulai Bekerja pada Database Ini</h2>
        <div className="mt-4 grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
          <Link
            to={`${basePath}/viewer`}
            className="flex flex-col justify-between gap-2 rounded-lg border border-border bg-surface-subtle/40 p-4 text-ink no-underline transition-colors hover:border-border-strong hover:bg-surface-subtle"
          >
            <div className="flex items-center justify-between">
              <Table2 className="h-4 w-4 text-accent" aria-hidden="true" />
              <ArrowRight className="h-4 w-4 text-muted" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-semibold">Schema &amp; Data Viewer</p>
              <p className="mt-0.5 text-xs text-muted">
                Jelajahi tabel, kolom, constraint, index, dan isi data.
              </p>
            </div>
          </Link>

          <Link
            to={`${basePath}/migrations`}
            className="flex flex-col justify-between gap-2 rounded-lg border border-border bg-surface-subtle/40 p-4 text-ink no-underline transition-colors hover:border-border-strong hover:bg-surface-subtle"
          >
            <div className="flex items-center justify-between">
              <GitBranch className="h-4 w-4 text-accent" aria-hidden="true" />
              <ArrowRight className="h-4 w-4 text-muted" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-semibold">Migration Editor</p>
              <p className="mt-0.5 text-xs text-muted">
                Susun SQL up/down, konfigurasi batch, EXPLAIN, &amp; jalankan async.
              </p>
            </div>
          </Link>

          <Link
            to={`${basePath}/migrations/history`}
            className="flex flex-col justify-between gap-2 rounded-lg border border-border bg-surface-subtle/40 p-4 text-ink no-underline transition-colors hover:border-border-strong hover:bg-surface-subtle"
          >
            <div className="flex items-center justify-between">
              <History className="h-4 w-4 text-accent" aria-hidden="true" />
              <ArrowRight className="h-4 w-4 text-muted" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-semibold">Execution History</p>
              <p className="mt-0.5 text-xs text-muted">
                Riwayat attempt lengkap, filter Hide rollback, &amp; batch recovery.
              </p>
            </div>
          </Link>

          <Link
            to={`${basePath}/jobs`}
            className="flex flex-col justify-between gap-2 rounded-lg border border-border bg-surface-subtle/40 p-4 text-ink no-underline transition-colors hover:border-border-strong hover:bg-surface-subtle"
          >
            <div className="flex items-center justify-between">
              <Activity className="h-4 w-4 text-accent" aria-hidden="true" />
              <ArrowRight className="h-4 w-4 text-muted" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-semibold">Jobs &amp; Lock Monitor</p>
              <p className="mt-0.5 text-xs text-muted">
                Pantau progres antrean worker, batch checkpoints, &amp; lock DB.
              </p>
            </div>
          </Link>
        </div>
      </section>
    </div>
  );
}
