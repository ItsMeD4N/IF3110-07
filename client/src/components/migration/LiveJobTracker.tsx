import { useEffect, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import { Activity, ArrowRight, Loader2, X } from 'lucide-react';
import { formatNumber } from '@/lib/format';
import { getJob } from '@/services/migrations';
import type { JobStatus, MigrationExecution, MigrationJob } from '@/types/api';
import { StatusBadge } from '@/components/ui/Badge';
import { Alert } from '@/components/ui/Alert';

const TERMINAL_STATUSES = new Set<JobStatus>([
  'completed',
  'failed',
  'partial',
  'partially_reverted',
  'uncertain',
  'cancelled',
]);

export function isTerminalJobStatus(status: JobStatus): boolean {
  return TERMINAL_STATUSES.has(status);
}

interface LiveJobTrackerProps {
  dbId: number;
  jobId: string;
  initialJob?: MigrationJob;
  onTerminal?: (job: MigrationJob, execution?: MigrationExecution) => void;
  onDismiss?: () => void;
}

export function LiveJobTracker({
  dbId,
  jobId,
  initialJob,
  onTerminal,
  onDismiss,
}: LiveJobTrackerProps) {
  const [job, setJob] = useState<MigrationJob | null>(initialJob ?? null);
  const [execution, setExecution] = useState<MigrationExecution | undefined>(undefined);
  const [pollError, setPollError] = useState('');
  const notifiedRef = useRef(false);
  const onTerminalRef = useRef(onTerminal);
  onTerminalRef.current = onTerminal;

  useEffect(() => {
    notifiedRef.current = false;
    setJob(initialJob ?? null);
    setExecution(undefined);
    setPollError('');

    let cancelled = false;
    let timerId: number | undefined;

    const poll = async () => {
      try {
        const res = await getJob(dbId, jobId);
        if (cancelled) return;
        setJob(res.job);
        setExecution(res.execution);
        setPollError('');

        if (isTerminalJobStatus(res.job.status)) {
          if (!notifiedRef.current) {
            notifiedRef.current = true;
            onTerminalRef.current?.(res.job, res.execution);
          }
          return;
        }
      } catch {
        if (!cancelled) {
          setPollError('Gagal mengambil pembaruan status job sementara. Mencoba ulang…');
        }
      }
      if (!cancelled) {
        timerId = window.setTimeout(() => void poll(), 1200);
      }
    };

    void poll();

    return () => {
      cancelled = true;
      if (timerId !== undefined) window.clearTimeout(timerId);
    };
  }, [dbId, initialJob, jobId]);

  const terminal = job ? isTerminalJobStatus(job.status) : false;
  const totalBatches = execution?.total_batches ?? 0;
  const completedBatches = execution?.completed_batches ?? 0;
  const revertedBatches = execution?.reverted_batches ?? 0;
  const isRevert = job?.action === 'batch_revert';
  const progressDone = isRevert ? revertedBatches : completedBatches;
  const progressPct = totalBatches > 0 ? Math.min(100, Math.round((progressDone / totalBatches) * 100)) : 0;

  return (
    <section
      aria-label="Pelacak status job migration"
      className="card-surface p-4"
    >
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div className="flex min-w-0 items-center gap-2.5">
          {terminal ? (
            <Activity className="h-4 w-4 shrink-0 text-accent" aria-hidden="true" />
          ) : (
            <Loader2 className="h-4 w-4 shrink-0 animate-spin text-accent" aria-hidden="true" />
          )}
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <p className="text-sm font-semibold text-ink">
                Job Migration ({job?.action ?? 'queued'})
              </p>
              {job && <StatusBadge status={job.status} />}
            </div>
            <p className="mt-0.5 truncate font-mono text-xs text-muted">
              job_id: {jobId}
              {job ? ` · attempt ${job.attempt}/${job.max_attempts}` : ''}
              {execution ? ` · execution #${execution.execution_id}` : ''}
            </p>
          </div>
        </div>

        <div className="flex items-center gap-2">
          <Link
            to={`/databases/${dbId}/jobs`}
            className="inline-flex items-center gap-1 text-xs font-medium text-accent hover:underline"
          >
            <span>Lihat semua jobs</span>
            <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
          </Link>
          {onDismiss && (
            <button
              type="button"
              aria-label="Tutup pelacak job"
              onClick={onDismiss}
              className="inline-flex h-7 w-7 items-center justify-center rounded text-muted hover:bg-surface-subtle hover:text-ink"
            >
              <X className="h-4 w-4" aria-hidden="true" />
            </button>
          )}
        </div>
      </div>

      {execution && (execution.execution_mode === 'batch' || totalBatches > 0) && (
        <div className="mt-3 space-y-1.5 border-t border-border pt-3">
          <div className="flex flex-wrap items-center justify-between gap-2 text-xs">
            <span className="font-medium text-ink tabular-nums">
              {isRevert ? 'Batch dibatalkan' : 'Batch selesai'}: {formatNumber(progressDone)} /{' '}
              {formatNumber(totalBatches)} ({progressPct}%)
            </span>
            <span className="text-muted tabular-nums">
              {isRevert
                ? `${formatNumber(execution.reverted_rows)} baris direvert`
                : `${formatNumber(execution.rows_affected)} baris diproses`}
              {execution.last_cursor ? ` · cursor: ${execution.last_cursor}` : ''}
            </span>
          </div>
          <progress
            value={progressDone}
            max={Math.max(totalBatches, 1)}
            aria-label="Progres batch migration"
            className="h-2 w-full overflow-hidden rounded-full"
          />
        </div>
      )}

      {pollError && <p className="mt-2 text-xs text-warn">{pollError}</p>}

      {(job?.last_error || execution?.error) && (
        <Alert variant="error" className="mt-3">
          <p className="font-mono text-xs">{execution?.error || job?.last_error}</p>
          {(execution?.sqlstate || job?.last_sqlstate) && (
            <p className="mt-0.5 text-[11px]">
              SQLSTATE: <span className="font-mono">{execution?.sqlstate || job?.last_sqlstate}</span>
            </p>
          )}
        </Alert>
      )}

      {terminal && (job?.status === 'partial' || job?.status === 'partially_reverted' || job?.status === 'uncertain') && (
        <div className="mt-3 flex flex-wrap items-center gap-2 text-xs">
          <Link
            to={`/databases/${dbId}/migrations/history`}
            className="font-semibold text-accent underline"
          >
            Buka Migration History untuk Resume / Revert / Reconcile →
          </Link>
        </div>
      )}
    </section>
  );
}
