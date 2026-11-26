import { useCallback, useEffect, useId, useMemo, useState } from 'react';
import { Link, useSearchParams } from 'react-router-dom';
import {
  CheckCircle2,
  Code2,
  Copy,
  History,
  Play,
  RefreshCw,
  RotateCcw,
  Send,
  ShieldCheck,
  XCircle,
} from 'lucide-react';
import { useDatabaseContext } from '@/components/layout/DatabaseLayout';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import { formatDateTime, formatNumber, generateIdempotencyKey } from '@/lib/format';
import * as approvalService from '@/services/approvals';
import * as migrationService from '@/services/migrations';
import type { MigrationAction, MigrationExecution, MigrationJob } from '@/types/api';
import { LiveJobTracker } from '@/components/migration/LiveJobTracker';
import { Alert } from '@/components/ui/Alert';
import { Badge, StatusBadge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { EmptyState } from '@/components/ui/EmptyState';
import { FormDialog } from '@/components/ui/FormDialog';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

type RecoveryKind = 'resume' | 'revert' | 'reconcile';

const ACTION_LABELS: Record<string, string> = {
  up: 'Up',
  rollback: 'Rollback',
  forward: 'Forward',
  batch_up: 'Batch Up',
  batch_resume: 'Batch Resume',
  batch_revert: 'Batch Revert',
};

export function MigrationHistoryPage() {
  const { database, canEdit, refreshLock } = useDatabaseContext();
  const { pushToast } = useToast();
  const [searchParams, setSearchParams] = useSearchParams();

  const initialHide = searchParams.get('hide_rollback') === '1' || searchParams.get('hide_rollback') === 'true';
  const initialExecId = Number(searchParams.get('execution') || '0') || null;

  const [hideRollback, setHideRollback] = useState(initialHide);
  const [executions, setExecutions] = useState<MigrationExecution[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');

  // Selected execution full detail (includes checkpoints[])
  const [selectedExecId, setSelectedExecId] = useState<number | null>(initialExecId);
  const [detail, setDetail] = useState<MigrationExecution | null>(null);
  const [loadingDetail, setLoadingDetail] = useState(false);
  const [detailError, setDetailError] = useState('');

  // Recovery action state
  const [recoveryTarget, setRecoveryTarget] = useState<{
    kind: RecoveryKind;
    exec: MigrationExecution;
  } | null>(null);
  const [runningRecovery, setRunningRecovery] = useState(false);
  const [actionError, setActionError] = useState<string>('');
  const [trackedJob, setTrackedJob] = useState<{ jobId: string; initialJob: MigrationJob } | null>(null);

  // Batch recovery review submission (M2 approval contract)
  const [reviewModal, setReviewModal] = useState<{
    action: Extract<MigrationAction, 'batch_resume' | 'batch_revert'>;
    exec: MigrationExecution;
  } | null>(null);
  const [reviewNote, setReviewNote] = useState('');
  const [reviewError, setReviewError] = useState('');
  const [submittingReview, setSubmittingReview] = useState(false);

  const tableCaptionId = useId();
  const checkpointsCaptionId = useId();

  const syncParams = useCallback(
    (hide: boolean, execId: number | null) => {
      const next = new URLSearchParams();
      if (hide) next.set('hide_rollback', '1');
      if (execId) next.set('execution', String(execId));
      setSearchParams(next, { replace: true });
    },
    [setSearchParams],
  );

  const loadExecutions = useCallback(
    async (hide: boolean) => {
      setLoading(true);
      setListError('');
      try {
        const res = await migrationService.listExecutions(database.id, hide);
        const list = res.executions ?? [];
        setExecutions(list);
        setSelectedExecId((prev) => {
          if (prev && list.some((e) => e.execution_id === prev)) return prev;
          return list.length > 0 ? list[0].execution_id : null;
        });
      } catch (err) {
        setListError(err instanceof ApiError ? err.message : 'Gagal memuat riwayat eksekusi migration.');
      } finally {
        setLoading(false);
      }
    },
    [database.id],
  );

  const loadDetail = useCallback(
    async (execId: number) => {
      setLoadingDetail(true);
      setDetailError('');
      try {
        const full = await migrationService.getExecution(database.id, execId);
        setDetail(full);
      } catch (err) {
        setDetail(null);
        setDetailError(err instanceof ApiError ? err.message : 'Gagal memuat detail execution.');
      } finally {
        setLoadingDetail(false);
      }
    },
    [database.id],
  );

  useEffect(() => {
    void loadExecutions(hideRollback);
  }, [hideRollback, loadExecutions]);

  useEffect(() => {
    if (selectedExecId) {
      void loadDetail(selectedExecId);
    } else {
      setDetail(null);
    }
  }, [loadDetail, selectedExecId]);

  const handleToggleHideRollback = (checked: boolean) => {
    setHideRollback(checked);
    syncParams(checked, selectedExecId);
  };

  const handleSelectExecution = (execId: number) => {
    setSelectedExecId(execId);
    setActionError('');
    syncParams(hideRollback, execId);
  };

  const handleCopySQL = async (sql: string) => {
    try {
      await navigator.clipboard.writeText(sql);
      pushToast('success', 'SQL berhasil disalin ke clipboard.');
    } catch {
      pushToast('error', 'Tidak dapat menyalin SQL ke clipboard.');
    }
  };

  const handleConfirmRecovery = async () => {
    if (!recoveryTarget) return;
    setRunningRecovery(true);
    setActionError('');
    const { kind, exec } = recoveryTarget;

    try {
      if (kind === 'reconcile') {
        const res = await migrationService.reconcileExecution(database.id, exec.execution_id);
        pushToast('success', res.message || `Execution #${exec.execution_id} selesai direkonsiliasi.`);
        setRecoveryTarget(null);
        await Promise.all([loadExecutions(hideRollback), loadDetail(exec.execution_id), refreshLock()]);
      } else {
        const key = generateIdempotencyKey(`${kind}-e${exec.execution_id}`);
        const res =
          kind === 'resume'
            ? await migrationService.enqueueBatchResume(database.id, exec.execution_id, key)
            : await migrationService.enqueueBatchRevert(database.id, exec.execution_id, key);
        pushToast('info', `${res.message} (Job ${res.job_id.slice(0, 8)}…)`);
        setTrackedJob({ jobId: res.job_id, initialJob: res.job });
        setRecoveryTarget(null);
        await Promise.all([loadExecutions(hideRollback), refreshLock()]);
      }
    } catch (err) {
      setRecoveryTarget(null);
      setActionError(err instanceof ApiError ? err.message : 'Gagal menjalankan aksi pemulihan.');
    } finally {
      setRunningRecovery(false);
    }
  };

  const handleSubmitBatchReview = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!reviewModal) return;
    setReviewError('');
    setSubmittingReview(true);
    try {
      await approvalService.submitApprovalRequest(database.id, reviewModal.exec.migration_id, {
        action: reviewModal.action,
        target_execution_id: reviewModal.exec.execution_id,
        note: reviewNote.trim(),
      });
      pushToast(
        'success',
        `Permintaan review ${reviewModal.action} untuk #E${reviewModal.exec.execution_id} berhasil dikirim ke group leader.`,
      );
      setReviewModal(null);
      setReviewNote('');
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setReviewError(
          'Endpoint approval-requests belum aktif di backend (lihat api_contract.md §2.2).',
        );
      } else {
        setReviewError(err instanceof ApiError ? err.message : 'Gagal mengirim permintaan review.');
      }
    } finally {
      setSubmittingReview(false);
    }
  };

  const handleTrackedJobUpdate = useCallback(
    (job: MigrationJob, liveExec?: MigrationExecution) => {
      if (job.execution_id && job.execution_id !== selectedExecId) {
        setSelectedExecId(job.execution_id);
        syncParams(hideRollback, job.execution_id);
      }
      if (liveExec) {
        setDetail(liveExec);
        setExecutions((prev) => {
          const exists = prev.some((e) => e.execution_id === liveExec.execution_id);
          if (exists) {
            return prev.map((e) => (e.execution_id === liveExec.execution_id ? liveExec : e));
          }
          return [liveExec, ...prev];
        });
      }
    },
    [hideRollback, selectedExecId, syncParams],
  );

  const handleTrackedJobFinished = useCallback(
    async (job: MigrationJob) => {
      const targetExecId = job.execution_id || selectedExecId;
      if (job.execution_id) {
        setSelectedExecId(job.execution_id);
        syncParams(hideRollback, job.execution_id);
      }
      await Promise.all([
        loadExecutions(hideRollback),
        targetExecId ? loadDetail(targetExecId) : Promise.resolve(),
        refreshLock(),
      ]);
      if (job.status === 'completed') {
        pushToast('success', `Job ${job.action} selesai dengan sukses.`);
      } else if (job.status === 'failed' || job.status === 'partial' || job.status === 'partially_reverted') {
        pushToast('info', `Job ${job.action} selesai dengan status ${job.status}.`);
      }
    },
    [hideRollback, loadDetail, loadExecutions, pushToast, refreshLock, selectedExecId, syncParams],
  );

  const childExecution = useMemo(() => {
    if (!detail) return null;
    return executions.find((e) => e.parent_execution_id === detail.execution_id) ?? null;
  }, [detail, executions]);

  const isSupersededPartial = Boolean(
    childExecution &&
      (detail?.status === 'partial' || detail?.status === 'partially_reverted') &&
      (childExecution.status === 'success' ||
        childExecution.status === 'reverted' ||
        childExecution.status === 'running' ||
        childExecution.status === 'partial' ||
        childExecution.status === 'partially_reverted'),
  );

  const canResume = Boolean(
    canEdit &&
      detail &&
      !isSupersededPartial &&
      detail.execution_mode === 'batch' &&
      detail.status === 'partial',
  );
  const canRevert = Boolean(
    canEdit &&
      detail &&
      !isSupersededPartial &&
      detail.execution_mode === 'batch' &&
      (detail.status === 'partial' || detail.status === 'partially_reverted' || detail.status === 'success'),
  );
  const canReconcile = Boolean(canEdit && detail && detail.status === 'uncertain');

  const recoveryConfirmCopy = useMemo(() => {
    if (!recoveryTarget) return null;
    const { kind, exec } = recoveryTarget;
    if (kind === 'resume') {
      return {
        title: `Lanjutkan batch Execution #${exec.execution_id}?`,
        description:
          'Worker akan melanjutkan eksekusi dari checkpoint terakhir tanpa mengulang batch yang sudah committed.',
        confirmLabel: 'Resume dari Checkpoint',
        destructive: false,
      };
    }
    if (kind === 'revert') {
      return {
        title: `Batalkan batch committed pada Execution #${exec.execution_id}?`,
        description:
          'Worker akan menjalankan SQL down immutable secara kompensasi per batch dalam urutan terbalik untuk batch yang sudah committed. Jika pembatalan tidak aman, backend akan menolak.',
        confirmLabel: 'Jalankan Batch Revert',
        destructive: true,
      };
    }
    return {
      title: `Rekonsiliasi Execution #${exec.execution_id}?`,
      description:
        'Sistem akan memeriksa bukti eksekusi pada metadata database target untuk memastikan apakah commit sebelumnya sudah masuk atau belum, lalu memperbarui status dan lock.',
      confirmLabel: 'Periksa & Rekonsiliasi',
      destructive: false,
    };
  }, [recoveryTarget]);

  return (
    <div className="space-y-5">
      {trackedJob && (
        <LiveJobTracker
          dbId={database.id}
          jobId={trackedJob.jobId}
          initialJob={trackedJob.initialJob}
          onUpdate={handleTrackedJobUpdate}
          onTerminal={(job) => void handleTrackedJobFinished(job)}
          onDismiss={() => setTrackedJob(null)}
        />
      )}

      {actionError && (
        <Alert variant="error" title="Aksi pemulihan ditolak">
          {actionError}
        </Alert>
      )}

      {/* Top Controls: Hide rollback checkbox + refresh */}
      <div className="card-surface flex flex-col gap-3 p-4 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-col gap-1">
          <div className="flex items-center gap-2.5">
            <input
              id="hide-rollback-checkbox"
              name="hide_rollback"
              type="checkbox"
              checked={hideRollback}
              onChange={(e) => handleToggleHideRollback(e.target.checked)}
              className="h-4 w-4 rounded border-border"
            />
            <label htmlFor="hide-rollback-checkbox" className="text-sm font-medium text-ink">
              Hide rollback
            </label>
          </div>
          <p className="text-xs text-muted">
            Jika dicentang, pasangan eksekusi yang dibatalkan dan rollback suksesnya (berdasarkan execution ID) disembunyikan dari tampilan tanpa menghapus data riwayat. Attempt gagal tetap tampil.
          </p>
        </div>

        <div className="flex shrink-0 items-center gap-2">
          <Badge tone="neutral" className="tabular-nums">
            {executions.length} catatan
          </Badge>
          <Button
            variant="secondary"
            size="sm"
            loading={loading}
            onClick={() => void loadExecutions(hideRollback)}
          >
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Muat Ulang</span>
          </Button>
        </div>
      </div>

      {listError && <Alert variant="error">{listError}</Alert>}

      {loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat riwayat migration">
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-4/5" />
        </div>
      ) : executions.length === 0 ? (
        <EmptyState
          icon={History}
          title={hideRollback ? 'Tidak ada eksekusi aktif yang tampil' : 'Belum ada riwayat eksekusi migration'}
          description={
            hideRollback
              ? 'Semua eksekusi sebelumnya mungkin sudah dibatalkan. Hapus centang "Hide rollback" untuk melihat riwayat lengkap.'
              : 'Jalankan migration dari tab Migrations untuk mulai mencatat riwayat eksekusi di sini.'
          }
          action={
            hideRollback ? (
              <Button variant="secondary" size="sm" onClick={() => handleToggleHideRollback(false)}>
                Tampilkan Semua Riwayat
              </Button>
            ) : (
              <Link
                to={`/databases/${database.id}/migrations`}
                className="inline-flex min-h-9 items-center gap-2 rounded-md bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-ink no-underline hover:bg-accent-hover"
              >
                Buka Migrations
              </Link>
            )
          }
        />
      ) : (
        <div className="space-y-5">
          {/* Execution History Table */}
          <TableShell ariaLabelledBy={tableCaptionId}>
            <table>
              <caption id={tableCaptionId} className="sr-only-live">
                Riwayat eksekusi migration database
              </caption>
              <thead>
                <tr>
                  <th scope="col">Exec ID</th>
                  <th scope="col">Migration</th>
                  <th scope="col">Aksi</th>
                  <th scope="col">Status</th>
                  <th scope="col">Pelaksana</th>
                  <th scope="col">Waktu</th>
                  <th scope="col">Keterangan</th>
                </tr>
              </thead>
              <tbody>
                {executions.map((ex) => {
                  const selected = ex.execution_id === selectedExecId;
                  return (
                    <tr
                      key={ex.execution_id}
                      onClick={() => handleSelectExecution(ex.execution_id)}
                      className={`cursor-pointer transition-colors ${
                        selected ? 'bg-accent-soft/45' : 'hover:bg-surface-subtle/50'
                      }`}
                    >
                      <th scope="row" className="text-left font-mono text-xs font-semibold text-ink tabular-nums">
                        <button
                          type="button"
                          onClick={(e) => {
                            e.stopPropagation();
                            handleSelectExecution(ex.execution_id);
                          }}
                          className="cursor-pointer font-mono text-accent hover:underline"
                        >
                          #E{ex.execution_id}
                        </button>
                      </th>
                      <td>
                        <div className="flex flex-wrap items-center gap-1.5">
                          <span className="font-mono text-xs font-semibold text-ink">
                            v{ex.migration_version}
                          </span>
                          <span className="text-sm font-medium text-ink">
                            {ex.migration_name || '(tanpa nama)'}
                          </span>
                        </div>
                        <p className="text-[11px] text-muted tabular-nums">
                          mig #{ex.migration_id} · rev {ex.migration_revision} · {ex.execution_mode}
                        </p>
                      </td>
                      <td>
                        <Badge tone={ex.action.includes('rollback') || ex.action.includes('revert') ? 'warn' : 'neutral'}>
                          {ACTION_LABELS[ex.action] ?? ex.action}
                        </Badge>
                      </td>
                      <td>
                        <StatusBadge status={ex.status} />
                      </td>
                      <td className="font-mono text-xs text-ink">{ex.actor}</td>
                      <td className="text-xs text-muted tabular-nums whitespace-nowrap">
                        {formatDateTime(ex.started_at || ex.created_at)}
                      </td>
                      <td className="max-w-xs text-xs">
                        {ex.reverted_execution_id ? (
                          <span className="font-mono text-warn">
                            membatalkan #E{ex.reverted_execution_id}
                          </span>
                        ) : ex.error ? (
                          <span className="line-clamp-2 text-danger">{ex.error}</span>
                        ) : ex.execution_mode === 'batch' ? (
                          <span className="text-muted tabular-nums">
                            {ex.completed_batches}/{ex.total_batches} batch · {formatNumber(ex.rows_affected)} rows
                          </span>
                        ) : (
                          <span className="text-muted">—</span>
                        )}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </TableShell>

          {/* Selected Execution Inspector + Batch Checkpoints + Recovery Controls */}
          <section className="card-surface p-5" aria-label="Detail eksekusi migration">
            {loadingDetail ? (
              <div className="space-y-3" role="status" aria-label="Memuat detail execution">
                <SkeletonLine className="h-6 w-56" />
                <SkeletonLine className="h-4 w-full" />
                <SkeletonLine className="h-28 w-full" />
              </div>
            ) : detailError ? (
              <Alert variant="error">{detailError}</Alert>
            ) : !detail ? (
              <p className="text-sm text-muted">Pilih salah satu baris execution di atas untuk melihat detail SQL dan checkpoint.</p>
            ) : (
              <div className="space-y-5">
                <div className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-start sm:justify-between">
                  <div>
                    <div className="flex flex-wrap items-center gap-2">
                      <h2 className="text-base font-bold text-ink">
                        Execution <span className="font-mono">#E{detail.execution_id}</span> — v
                        {detail.migration_version} {detail.migration_name}
                      </h2>
                      <StatusBadge status={detail.status} />
                      <Badge tone="neutral">{ACTION_LABELS[detail.action] ?? detail.action}</Badge>
                    </div>
                    <p className="mt-1 text-xs text-muted tabular-nums">
                      Migration ID #{detail.migration_id} · Revisi {detail.migration_revision} · Attempt{' '}
                      {detail.attempt} · Actor <span className="font-mono text-ink">{detail.actor}</span>
                    </p>
                  </div>

                  {/* Batch Recovery & Reconciliation Buttons */}
                  {(canResume || canRevert || canReconcile) && (
                    <div className="flex flex-wrap items-center gap-2">
                      {canResume && (
                        <>
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => {
                              setReviewModal({ action: 'batch_resume', exec: detail });
                              setReviewNote('');
                              setReviewError('');
                            }}
                          >
                            <Send className="h-3.5 w-3.5" aria-hidden="true" />
                            <span>Ajukan Review Resume</span>
                          </Button>
                          <Button
                            variant="primary"
                            size="sm"
                            onClick={() => setRecoveryTarget({ kind: 'resume', exec: detail })}
                          >
                            <Play className="h-3.5 w-3.5" aria-hidden="true" />
                            <span>Resume dari Checkpoint</span>
                          </Button>
                        </>
                      )}
                      {canRevert && (
                        <>
                          <Button
                            variant="secondary"
                            size="sm"
                            onClick={() => {
                              setReviewModal({ action: 'batch_revert', exec: detail });
                              setReviewNote('');
                              setReviewError('');
                            }}
                          >
                            <Send className="h-3.5 w-3.5" aria-hidden="true" />
                            <span>Ajukan Review Revert</span>
                          </Button>
                          <Button
                            variant="danger"
                            size="sm"
                            onClick={() => setRecoveryTarget({ kind: 'revert', exec: detail })}
                          >
                            <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
                            <span>
                              {detail.status === 'partially_reverted'
                                ? 'Lanjutkan Batch Revert'
                                : 'Batalkan Batch (Revert)'}
                            </span>
                          </Button>
                        </>
                      )}
                      {canReconcile && (
                        <Button
                          variant="primary"
                          size="sm"
                          onClick={() => setRecoveryTarget({ kind: 'reconcile', exec: detail })}
                        >
                          <ShieldCheck className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Reconcile Bukti Eksekusi</span>
                        </Button>
                      )}
                    </div>
                  )}
                </div>

                {/* Metadata Grid */}
                <dl className="grid gap-3 text-xs sm:grid-cols-2 lg:grid-cols-4">
                  <div className="rounded-md border border-border bg-surface-subtle/40 p-2.5">
                    <dt className="text-muted">Mulai / Selesai</dt>
                    <dd className="mt-0.5 font-medium text-ink tabular-nums">
                      {formatDateTime(detail.started_at || detail.created_at)} →{' '}
                      {formatDateTime(detail.finished_at)}
                    </dd>
                  </div>
                  <div className="rounded-md border border-border bg-surface-subtle/40 p-2.5">
                    <dt className="text-muted">Job ID</dt>
                    <dd className="mt-0.5 truncate font-mono text-ink" title={detail.job_id || ''}>
                      {detail.job_id || '—'}
                    </dd>
                  </div>
                  <div className="rounded-md border border-border bg-surface-subtle/40 p-2.5">
                    <dt className="text-muted">Trace / Correlation ID</dt>
                    <dd
                      className="mt-0.5 truncate font-mono text-ink"
                      title={detail.trace_id || detail.correlation_id || ''}
                    >
                      {detail.trace_id || detail.correlation_id || '—'}
                    </dd>
                  </div>
                  <div className="rounded-md border border-border bg-surface-subtle/40 p-2.5">
                    <dt className="text-muted">Relasi Execution</dt>
                    <dd className="mt-0.5 font-mono text-ink">
                      {detail.reverted_execution_id
                        ? `Reverts #E${detail.reverted_execution_id}`
                        : detail.parent_execution_id
                          ? `Parent #E${detail.parent_execution_id}`
                          : '—'}
                    </dd>
                  </div>
                </dl>

                {childExecution && (
                  <Alert variant="info" title="Execution ini sudah memiliki tindak lanjut">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <span>
                        Riwayat <code className="font-mono">#E{detail.execution_id}</code> bersifat append-only (menunjukkan snapshot saat berhenti). Proses lanjutannya tercatat di{' '}
                        <code className="font-mono font-semibold">#E{childExecution.execution_id}</code> ({ACTION_LABELS[childExecution.action] ?? childExecution.action} · {childExecution.status}).
                      </span>
                      <Button
                        variant="secondary"
                        size="sm"
                        onClick={() => handleSelectExecution(childExecution.execution_id)}
                      >
                        Lihat #E{childExecution.execution_id} →
                      </Button>
                    </div>
                  </Alert>
                )}

                {detail.error && (
                  <Alert variant="error" title="Error Eksekusi">
                    <p className="font-mono text-xs">{detail.error}</p>
                    {detail.sqlstate && (
                      <p className="mt-1 text-xs">
                        SQLSTATE: <code className="font-mono">{detail.sqlstate}</code>
                      </p>
                    )}
                  </Alert>
                )}

                {/* Executed SQL Copy */}
                <div className="space-y-1.5">
                  <div className="flex items-center justify-between gap-2">
                    <h3 className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink">
                      <Code2 className="h-4 w-4 text-accent" aria-hidden="true" />
                      <span>Salinan SQL yang Dijalankan</span>
                    </h3>
                    <Button
                      variant="secondary"
                      size="sm"
                      onClick={() => void handleCopySQL(detail.sql_executed)}
                    >
                      <Copy className="h-3.5 w-3.5" aria-hidden="true" />
                      <span>Salin SQL</span>
                    </Button>
                  </div>
                  <pre className="max-h-72 overflow-auto rounded-lg border border-border bg-surface-subtle p-3.5 font-mono text-xs leading-relaxed text-ink">
                    {detail.sql_executed}
                  </pre>
                </div>

                {/* Batch Summary & Checkpoints */}
                {(detail.execution_mode === 'batch' || (detail.checkpoints && detail.checkpoints.length > 0)) && (
                  <div className="space-y-3 border-t border-border pt-4">
                    <div className="flex flex-wrap items-center justify-between gap-2">
                      <h3 id={checkpointsCaptionId} className="text-sm font-semibold text-ink">
                        Progres &amp; Checkpoints Batch
                      </h3>
                      <div className="flex flex-wrap items-center gap-3 text-xs text-muted tabular-nums">
                        <span>
                          Committed:{' '}
                          <strong className="text-ink">
                            {detail.completed_batches}/{detail.total_batches}
                          </strong>{' '}
                          batch ({formatNumber(detail.rows_affected)} rows)
                        </span>
                        {detail.reverted_batches > 0 && (
                          <span>
                            Reverted:{' '}
                            <strong className="text-warn">{detail.reverted_batches}</strong> batch (
                            {formatNumber(detail.reverted_rows)} rows)
                          </span>
                        )}
                        {detail.last_cursor && (
                          <span>
                            Cursor terakhir:{' '}
                            <code className="font-mono text-ink">{detail.last_cursor}</code>
                          </span>
                        )}
                      </div>
                    </div>

                    {detail.total_batches > 0 && (
                      <progress
                        value={
                          detail.action === 'batch_revert'
                            ? detail.reverted_batches
                            : detail.completed_batches
                        }
                        max={detail.total_batches}
                        aria-label="Progres batch execution"
                        className="h-2 w-full overflow-hidden rounded-full"
                      />
                    )}

                    {detail.checkpoints && detail.checkpoints.length > 0 ? (
                      <TableShell ariaLabelledBy={checkpointsCaptionId}>
                        <table>
                          <thead>
                            <tr>
                              <th scope="col">Batch</th>
                              <th scope="col">Exec</th>
                              <th scope="col">Rentang Cursor</th>
                              <th scope="col" className="text-right">
                                Rows
                              </th>
                              <th scope="col">Status</th>
                              <th scope="col" className="text-right">
                                Durasi
                              </th>
                              <th scope="col">Error / Catatan</th>
                            </tr>
                          </thead>
                          <tbody>
                            {detail.checkpoints.map((cp) => (
                              <tr key={cp.id} className="hover:bg-surface-subtle/50">
                                <th scope="row" className="text-left font-mono text-xs font-semibold text-ink tabular-nums">
                                  #{cp.batch_number}
                                </th>
                                <td className="font-mono text-xs text-muted tabular-nums">
                                  #E{cp.execution_id}
                                </td>
                                <td className="font-mono text-xs text-ink">
                                  {cp.cursor_start} → {cp.cursor_end}
                                </td>
                                <td className="text-right font-mono text-xs text-ink tabular-nums">
                                  {formatNumber(cp.rows_affected)}
                                </td>
                                <td>
                                  {cp.status === 'committed' ? (
                                    <Badge tone="ok">
                                      <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
                                      <span>committed</span>
                                    </Badge>
                                  ) : cp.status === 'reverted' ? (
                                    <Badge tone="neutral">reverted</Badge>
                                  ) : (
                                    <Badge tone="danger">
                                      <XCircle className="h-3 w-3" aria-hidden="true" />
                                      <span>{cp.status}</span>
                                    </Badge>
                                  )}
                                </td>
                                <td className="text-right font-mono text-xs text-muted tabular-nums">
                                  {cp.duration_ms} ms
                                </td>
                                <td className="max-w-xs text-xs text-danger">
                                  {cp.error ? (
                                    <span>
                                      {cp.error}
                                      {cp.sqlstate ? ` (${cp.sqlstate})` : ''}
                                    </span>
                                  ) : (
                                    <span className="text-muted">—</span>
                                  )}
                                </td>
                              </tr>
                            ))}
                          </tbody>
                        </table>
                      </TableShell>
                    ) : (
                      <p className="text-xs text-muted">Belum ada checkpoint batch yang tercatat.</p>
                    )}
                  </div>
                )}
              </div>
            )}
          </section>
        </div>
      )}

      <ConfirmDialog
        open={Boolean(recoveryTarget && recoveryConfirmCopy)}
        title={recoveryConfirmCopy?.title ?? ''}
        description={recoveryConfirmCopy?.description ?? ''}
        confirmLabel={recoveryConfirmCopy?.confirmLabel ?? 'Lanjutkan'}
        destructive={recoveryConfirmCopy?.destructive ?? false}
        loading={runningRecovery}
        onConfirm={() => void handleConfirmRecovery()}
        onCancel={() => setRecoveryTarget(null)}
      />

      <FormDialog
        open={Boolean(reviewModal)}
        title={`Ajukan Review ${reviewModal?.action ?? ''} — #E${reviewModal?.exec.execution_id ?? ''}`}
        description="Sesuai spesifikasi Milestone 2, aksi batch resume dan batch revert tetap mengikuti permission, validasi, dan approval leader setiap group."
        busy={submittingReview}
        onClose={() => setReviewModal(null)}
      >
        {reviewError && (
          <Alert variant="error" className="mb-4">
            {reviewError}
          </Alert>
        )}
        <form onSubmit={(e) => void handleSubmitBatchReview(e)} className="space-y-4" noValidate>
          <div className="space-y-1">
            <label htmlFor="batch-review-note" className="block text-sm font-medium text-ink">
              Catatan untuk Group Leader (Opsional)
            </label>
            <textarea
              id="batch-review-note"
              name="note"
              rows={3}
              value={reviewNote}
              onChange={(e) => setReviewNote(e.target.value)}
              placeholder="Jelaskan kondisi checkpoint terakhir dan rencana pemulihan."
              className="w-full rounded-md border border-border bg-surface p-2.5 text-sm text-ink"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" disabled={submittingReview} onClick={() => setReviewModal(null)}>
              Batal
            </Button>
            <Button type="submit" variant="primary" loading={submittingReview}>
              <Send className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Kirim Permintaan Review</span>
            </Button>
          </div>
        </form>
      </FormDialog>
    </div>
  );
}
