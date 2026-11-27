import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  Activity,
  ArrowRight,
  Loader2,
  Lock,
  LockOpen,
  RefreshCw,
  ShieldAlert,
} from 'lucide-react';
import { useDatabaseContext } from '@/components/layout/DatabaseLayout';
import { ApiError } from '@/lib/api';
import { formatDateTime, formatNumber } from '@/lib/format';
import * as migrationService from '@/services/migrations';
import type { MigrationExecution, MigrationJob } from '@/types/api';
import { isTerminalJobStatus } from '@/components/migration/LiveJobTracker';
import { Alert } from '@/components/ui/Alert';
import { Badge, StatusBadge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { ProgressBar } from '@/components/ui/ProgressBar';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

type JobFilter = 'all' | 'active' | 'attention' | 'completed';

export function DatabaseJobsPage() {
  const { database, lockInfo, refreshLock } = useDatabaseContext();

  const [jobs, setJobs] = useState<MigrationJob[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [filter, setFilter] = useState<JobFilter>('all');

  const [selectedJobId, setSelectedJobId] = useState<string | null>(null);
  const [selectedDetail, setSelectedDetail] = useState<{
    job: MigrationJob;
    execution?: MigrationExecution;
  } | null>(null);
  const [loadingDetail, setLoadingDetail] = useState(false);

  const jobsCaptionId = useId();

  const loadJobs = useCallback(
    async (silent = false) => {
      if (!silent) setLoading(true);
      setError('');
      try {
        const list = await migrationService.listJobs(database.id, 50);
        setJobs(list);
        setSelectedJobId((prev) => {
          if (prev && list.some((j) => j.id === prev)) return prev;
          return list.length > 0 ? list[0].id : null;
        });
      } catch (err) {
        if (!silent) {
          setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar job migration.');
        }
      } finally {
        if (!silent) setLoading(false);
      }
    },
    [database.id],
  );

  const loadJobDetail = useCallback(
    async (jobId: string, silent = false) => {
      if (!silent) setLoadingDetail(true);
      try {
        const res = await migrationService.getJob(database.id, jobId);
        setSelectedDetail(res);
      } catch {
        if (!silent) setSelectedDetail(null);
      } finally {
        if (!silent) setLoadingDetail(false);
      }
    },
    [database.id],
  );

  useEffect(() => {
    void Promise.all([loadJobs(false), refreshLock()]);
  }, [loadJobs, refreshLock]);

  useEffect(() => {
    if (selectedJobId) {
      void loadJobDetail(selectedJobId, false);
    } else {
      setSelectedDetail(null);
    }
  }, [loadJobDetail, selectedJobId]);

  const hasActiveWork = useMemo(() => {
    if (lockInfo?.locked && lockInfo.state === 'held') return true;
    return jobs.some((j) => !isTerminalJobStatus(j.status));
  }, [jobs, lockInfo]);

  useEffect(() => {
    if (!hasActiveWork) return;
    const timer = window.setInterval(() => {
      void loadJobs(true);
      void refreshLock();
      if (selectedJobId) {
        void loadJobDetail(selectedJobId, true);
      }
    }, 500);
    return () => window.clearInterval(timer);
  }, [hasActiveWork, loadJobDetail, loadJobs, refreshLock, selectedJobId]);

  const filteredJobs = useMemo(() => {
    switch (filter) {
      case 'active':
        return jobs.filter((j) => !isTerminalJobStatus(j.status));
      case 'attention':
        return jobs.filter(
          (j) =>
            j.status === 'failed' ||
            j.status === 'partial' ||
            j.status === 'partially_reverted' ||
            j.status === 'uncertain',
        );
      case 'completed':
        return jobs.filter((j) => j.status === 'completed');
      default:
        return jobs;
    }
  }, [filter, jobs]);

  const handleManualRefresh = async () => {
    await Promise.all([
      loadJobs(false),
      refreshLock(),
      selectedJobId ? loadJobDetail(selectedJobId, false) : Promise.resolve(),
    ]);
  };

  const basePath = `/databases/${database.id}`;
  const detailExec = selectedDetail?.execution;

  return (
    <div className="space-y-5">
      <section aria-label="Status lock database" className="card-surface p-5">
        <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
          <div className="flex items-start gap-3">
            <span
              className={`mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-lg ${
                !lockInfo?.locked
                  ? 'bg-ok-soft text-ok'
                  : lockInfo.state === 'held'
                    ? 'bg-warn-soft text-warn'
                    : 'bg-danger-soft text-danger'
              }`}
            >
              {!lockInfo?.locked ? (
                <LockOpen className="h-4 w-4" aria-hidden="true" />
              ) : lockInfo.state === 'held' ? (
                <Lock className="h-4 w-4" aria-hidden="true" />
              ) : (
                <ShieldAlert className="h-4 w-4" aria-hidden="true" />
              )}
            </span>
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <h2 className="text-base font-semibold text-ink">Status Distributed Lock Database</h2>
                {!lockInfo?.locked ? (
                  <Badge tone="ok">Bebas</Badge>
                ) : lockInfo.state === 'held' ? (
                  <Badge tone="warn">Held (Migration Aktif)</Badge>
                ) : (
                  <Badge tone="danger">{lockInfo.state}</Badge>
                )}
              </div>
              <p className="mt-1 text-xs text-muted">
                {!lockInfo?.locked
                  ? 'Satu database hanya menjalankan satu migration pada satu waktu; saat ini tidak ada lock aktif.'
                  : lockInfo.reason ||
                    `Dikunci oleh worker ${lockInfo.owner_id ?? '-'} (fencing v${lockInfo.fencing_version ?? 0}).`}
              </p>
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {hasActiveWork ? (
              <Badge tone="accent">
                <Loader2 className="h-3 w-3 animate-spin" aria-hidden="true" />
                <span>Live Polling (0.5s)</span>
              </Badge>
            ) : (
              <Badge tone="neutral">Idle</Badge>
            )}
            <Button variant="secondary" size="sm" loading={loading} onClick={() => void handleManualRefresh()}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Muat Ulang</span>
            </Button>
          </div>
        </div>

        {lockInfo?.locked && (
          <dl className="mt-4 grid grid-cols-1 gap-3 border-t border-border pt-4 text-xs sm:grid-cols-2 lg:grid-cols-4">
            <div>
              <dt className="text-muted">Worker Owner</dt>
              <dd className="mt-0.5 font-mono font-medium text-ink">{lockInfo.owner_id || '-'}</dd>
            </div>
            <div>
              <dt className="text-muted">Job / Execution</dt>
              <dd className="mt-0.5 font-mono text-ink">
                {lockInfo.job_id ? `${lockInfo.job_id.slice(0, 12)}…` : '-'}
                {lockInfo.execution_id ? ` · #E${lockInfo.execution_id}` : ''}
              </dd>
            </div>
            <div>
              <dt className="text-muted">Heartbeat Terakhir</dt>
              <dd className="mt-0.5 text-ink tabular-nums">{formatDateTime(lockInfo.heartbeat_at)}</dd>
            </div>
            <div>
              <dt className="text-muted">Lease Berakhir</dt>
              <dd className="mt-0.5 text-ink tabular-nums">
                {formatDateTime(lockInfo.expires_at)}
                {lockInfo.expired && <span className="ml-1 font-semibold text-danger">(expired)</span>}
              </dd>
            </div>
          </dl>
        )}

        {lockInfo?.locked &&
          (lockInfo.state === 'blocked_partial' || lockInfo.state === 'blocked_uncertain') && (
            <div className="mt-3 border-t border-border pt-3">
              <Link
                to={
                  lockInfo.execution_id
                    ? `${basePath}/migrations/history?execution=${lockInfo.execution_id}`
                    : `${basePath}/migrations/history`
                }
                className="inline-flex items-center gap-1.5 text-xs font-semibold text-accent hover:underline"
              >
                <span>
                  Tinjau &amp; pulihkan Execution #{lockInfo.execution_id ?? '-'} di Migration History
                </span>
                <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
              </Link>
            </div>
          )}
      </section>

      {error && <Alert variant="error">{error}</Alert>}

      <div className="flex flex-wrap items-center justify-between gap-2">
        <div role="group" aria-label="Filter status job" className="inline-flex flex-wrap gap-1.5">
          {(
            [
              { id: 'all', label: `Semua (${jobs.length})` },
              {
                id: 'active',
                label: `Aktif (${jobs.filter((j) => !isTerminalJobStatus(j.status)).length})`,
              },
              {
                id: 'attention',
                label: `Perlu Perhatian (${
                  jobs.filter((j) =>
                    ['failed', 'partial', 'partially_reverted', 'uncertain'].includes(j.status),
                  ).length
                })`,
              },
              {
                id: 'completed',
                label: `Selesai (${jobs.filter((j) => j.status === 'completed').length})`,
              },
            ] as const
          ).map((item) => (
            <button
              key={item.id}
              type="button"
              aria-pressed={filter === item.id}
              onClick={() => setFilter(item.id)}
              className={`cursor-pointer rounded-md border px-2.5 py-1 text-xs font-medium tabular-nums transition-colors ${
                filter === item.id
                  ? 'border-accent bg-accent-soft text-accent'
                  : 'border-border bg-surface text-muted hover:bg-surface-subtle hover:text-ink'
              }`}
            >
              {item.label}
            </button>
          ))}
        </div>
      </div>

      {loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat daftar jobs">
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-3/4" />
        </div>
      ) : filteredJobs.length === 0 ? (
        <EmptyState
          icon={Activity}
          title={filter === 'all' ? 'Belum ada job migration' : 'Tidak ada job pada filter ini'}
          description={
            filter === 'all'
              ? 'Setiap eksekusi migration, rollback, forward, resume, atau revert akan muncul di antrean job ini.'
              : 'Pilih filter "Semua" untuk melihat seluruh riwayat job.'
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-5 xl:grid-cols-[minmax(0,1.25fr)_minmax(0,1fr)] xl:items-start">
          <TableShell ariaLabelledBy={jobsCaptionId} className="min-w-0">
            <table>
              <caption id={jobsCaptionId} className="sr-only-live">
                Daftar antrean dan riwayat job migration
              </caption>
              <thead>
                <tr>
                  <th scope="col">Job ID</th>
                  <th scope="col">Aksi</th>
                  <th scope="col">Status</th>
                  <th scope="col">Attempt</th>
                  <th scope="col">Dibuat</th>
                </tr>
              </thead>
              <tbody>
                {filteredJobs.map((j) => {
                  const selected = j.id === selectedJobId;
                  return (
                    <tr
                      key={j.id}
                      onClick={() => setSelectedJobId(j.id)}
                      className={`cursor-pointer transition-colors ${
                        selected ? 'bg-accent-soft/45' : 'hover:bg-surface-subtle/50'
                      }`}
                    >
                      <th scope="row" className="text-left font-mono text-xs text-ink">
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            setSelectedJobId(j.id);
                          }}
                          className="cursor-pointer font-mono text-accent hover:underline"
                          title={j.id}
                        >
                          {j.id.slice(0, 8)}…
                        </button>
                        <div className="text-[11px] font-normal text-muted">
                          mig #{j.migration_id}
                          {j.execution_id ? ` · #E${j.execution_id}` : ''}
                        </div>
                      </th>
                      <td>
                        <Badge tone="neutral">{j.action}</Badge>
                      </td>
                      <td>
                        <StatusBadge status={j.status} />
                      </td>
                      <td className="font-mono text-xs text-muted tabular-nums">
                        {j.attempt}/{j.max_attempts}
                      </td>
                      <td className="text-xs text-muted tabular-nums whitespace-nowrap">
                        {formatDateTime(j.created_at)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableShell>

          <section className="card-surface min-w-0 p-4 sm:p-5" aria-label="Detail job terpilih">
            {loadingDetail && !selectedDetail ? (
              <div className="space-y-3" role="status" aria-label="Memuat detail job">
                <SkeletonLine className="h-5 w-48" />
                <SkeletonLine className="h-4 w-full" />
                <SkeletonLine className="h-20 w-full" />
              </div>
            ) : !selectedDetail ? (
              <p className="text-sm text-muted">Pilih salah satu job untuk melihat detail dan progres batch-nya.</p>
            ) : (
              <div className="space-y-4">
                <div className="flex flex-wrap items-start justify-between gap-2 border-b border-border pb-3">
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <h3 className="text-sm font-bold text-ink">
                        Job <span className="font-mono">{selectedDetail.job.action}</span>
                      </h3>
                      <StatusBadge status={selectedDetail.job.status} />
                    </div>
                    <p className="mt-0.5 break-all font-mono text-[11px] text-muted">
                      {selectedDetail.job.id}
                    </p>
                  </div>
                  {detailExec && (
                    <Link
                      to={`${basePath}/migrations/history?execution=${detailExec.execution_id}`}
                      className="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline"
                    >
                      <span>Execution #E{detailExec.execution_id}</span>
                      <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
                    </Link>
                  )}
                </div>

                <dl className="grid grid-cols-1 gap-3 text-xs sm:grid-cols-2">
                  <div className="min-w-0">
                    <dt className="text-muted">Pelaksana</dt>
                    <dd className="mt-0.5 truncate font-mono text-ink">{selectedDetail.job.actor}</dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-muted">Attempt</dt>
                    <dd className="mt-0.5 font-mono text-ink tabular-nums">
                      {selectedDetail.job.attempt} / {selectedDetail.job.max_attempts}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-muted">Worker</dt>
                    <dd className="mt-0.5 truncate font-mono text-ink">
                      {selectedDetail.job.locked_by || '-'}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-muted">Jadwal / Mulai</dt>
                    <dd className="mt-0.5 text-ink tabular-nums">
                      {formatDateTime(selectedDetail.job.started_at || selectedDetail.job.run_at)}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-muted">Selesai</dt>
                    <dd className="mt-0.5 text-ink tabular-nums">
                      {formatDateTime(selectedDetail.job.completed_at)}
                    </dd>
                  </div>
                  <div className="min-w-0">
                    <dt className="text-muted">Idempotency Key</dt>
                    <dd
                      className="mt-0.5 truncate font-mono text-ink"
                      title={selectedDetail.job.idempotency_key}
                    >
                      {selectedDetail.job.idempotency_key}
                    </dd>
                  </div>
                </dl>

                {detailExec && (detailExec.execution_mode === 'batch' || detailExec.total_batches > 0) && (
                  <div className="space-y-2 rounded-lg border border-border bg-surface-subtle/40 p-3">
                    <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
                      <span className="font-semibold text-ink tabular-nums">
                        {detailExec.action === 'batch_revert' ? 'Batch Reverted' : 'Batch Committed'}:{' '}
                        {detailExec.action === 'batch_revert'
                          ? detailExec.reverted_batches
                          : detailExec.completed_batches}{' '}
                        / {detailExec.total_batches}
                      </span>
                      <span className="text-muted tabular-nums">
                        {formatNumber(
                          detailExec.action === 'batch_revert'
                            ? detailExec.reverted_rows
                            : detailExec.rows_affected,
                        )}{' '}
                        rows
                      </span>
                    </div>
                    <ProgressBar
                      value={
                        detailExec.action === 'batch_revert'
                          ? detailExec.reverted_batches
                          : detailExec.completed_batches
                      }
                      max={Math.max(detailExec.total_batches, 1)}
                      label="Progres batch job"
                      active={!isTerminalJobStatus(selectedDetail.job.status)}
                      tone={
                        selectedDetail.job.status === 'completed'
                          ? 'ok'
                          : selectedDetail.job.status === 'failed' || selectedDetail.job.status === 'uncertain'
                            ? 'danger'
                            : selectedDetail.job.status === 'partial' || selectedDetail.job.status === 'partially_reverted'
                              ? 'warn'
                              : 'accent'
                      }
                    />
                    {detailExec.last_cursor && (
                      <p className="font-mono text-[11px] text-muted">
                        last_cursor: {detailExec.last_cursor}
                        {detailExec.scope_max_cursor ? ` / max: ${detailExec.scope_max_cursor}` : ''}
                      </p>
                    )}
                  </div>
                )}

                {selectedDetail.job.last_error && (
                  <Alert variant="error" title="Error Terakhir">
                    <p className="font-mono text-xs">{selectedDetail.job.last_error}</p>
                    {selectedDetail.job.last_sqlstate && (
                      <p className="mt-0.5 text-[11px]">
                        SQLSTATE: <code className="font-mono">{selectedDetail.job.last_sqlstate}</code>
                      </p>
                    )}
                  </Alert>
                )}
              </div>
            )}
          </section>
        </div>
      )}
    </div>
  );
}
