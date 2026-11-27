import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import {
  CheckCircle2,
  Clock,
  Crown,
  RefreshCw,
  Send,
  ShieldCheck,
  ThumbsDown,
  ThumbsUp,
  XCircle,
} from 'lucide-react';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import * as approvalService from '@/services/approvals';
import type { ApprovalRequest, MigrationAction, MigrationDefinition } from '@/types/api';
import { WorkflowStepper } from '@/components/migration/WorkflowStepper';
import { Alert } from '@/components/ui/Alert';
import { AccessBadge, Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { FormDialog } from '@/components/ui/FormDialog';
import { SkeletonLine } from '@/components/ui/Skeleton';

interface ApprovalPanelProps {
  dbId: number;
  definition: MigrationDefinition;
  canEdit: boolean;
  onStatusUpdated?: () => Promise<void> | void;
}

const REQUEST_STATUS_TONES: Record<ApprovalRequest['status'], 'warn' | 'ok' | 'danger' | 'neutral'> = {
  pending_review: 'warn',
  approved: 'ok',
  rejected: 'danger',
  consumed: 'neutral',
  superseded: 'neutral',
};

export function ApprovalPanel({
  dbId,
  definition,
  canEdit,
  onStatusUpdated,
}: ApprovalPanelProps) {
  const { pushToast } = useToast();

  const [requests, setRequests] = useState<ApprovalRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [backendUnavailable, setBackendUnavailable] = useState(false);
  const [loadError, setLoadError] = useState('');

  const [submitOpen, setSubmitOpen] = useState(false);
  const [submitAction, setSubmitAction] = useState<MigrationAction>('up');
  const [submitNote, setSubmitNote] = useState('');
  const [submitError, setSubmitError] = useState('');
  const [submitting, setSubmitting] = useState(false);

  const [decisionTarget, setDecisionTarget] = useState<{
    req: ApprovalRequest;
    kind: 'approve' | 'reject';
  } | null>(null);
  const [decisionComment, setDecisionComment] = useState('');
  const [decisionError, setDecisionError] = useState('');
  const [deciding, setDeciding] = useState(false);
  const commentRef = useRef<HTMLTextAreaElement>(null);

  const sectionHeadingId = useId();

  const defaultActionForDef = useCallback((): MigrationAction => {
    if (definition.can_rollback && !definition.is_irreversible) {
      return definition.execution_mode === 'batch' ? 'batch_revert' : 'rollback';
    }
    if (definition.can_forward) {
      return 'forward';
    }
    return definition.execution_mode === 'batch' ? 'batch_up' : 'up';
  }, [definition.can_forward, definition.can_rollback, definition.execution_mode, definition.is_irreversible]);

  const loadRequests = useCallback(async () => {
    setLoading(true);
    setLoadError('');
    setBackendUnavailable(false);
    try {
      const list = await approvalService.listDatabaseApprovalRequests(dbId, {
        migration_id: definition.id,
      });
      setRequests(list);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setBackendUnavailable(true);
        setRequests([]);
      } else {
        setLoadError(err instanceof ApiError ? err.message : 'Gagal memuat status approval.');
      }
    } finally {
      setLoading(false);
    }
  }, [dbId, definition.id]);

  useEffect(() => {
    void loadRequests();
  }, [loadRequests, definition.revision, definition.workflow_status]);

  const openSubmitDialog = () => {
    setSubmitAction(defaultActionForDef());
    setSubmitNote('');
    setSubmitError('');
    setSubmitOpen(true);
  };

  const handleSubmitReview = async (e: FormEvent) => {
    e.preventDefault();
    setSubmitError('');
    setSubmitting(true);
    try {
      await approvalService.submitApprovalRequest(dbId, definition.id, {
        action: submitAction,
        note: submitNote.trim(),
      });
      pushToast('success', `Permintaan review (${submitAction}) untuk v${definition.version} berhasil diajukan.`);
      setSubmitOpen(false);
      await loadRequests();
      await onStatusUpdated?.();
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setSubmitError(
          'Endpoint approval-requests belum aktif di backend (lihat api_contract.md §2.2).',
        );
      } else {
        setSubmitError(err instanceof ApiError ? err.message : 'Gagal mengajukan review.');
      }
    } finally {
      setSubmitting(false);
    }
  };

  const handleDecisionSubmit = async (e: FormEvent) => {
    e.preventDefault();
    if (!decisionTarget) return;
    const trimmed = decisionComment.trim();
    if (decisionTarget.kind === 'reject' && !trimmed) {
      setDecisionError('Alasan penolakan wajib diisi agar pembuat migration tahu apa yang harus diperbaiki.');
      commentRef.current?.focus();
      return;
    }
    setDecisionError('');
    setDeciding(true);
    try {
      if (decisionTarget.kind === 'approve') {
        await approvalService.approveRequest(dbId, decisionTarget.req.id, trimmed);
        pushToast('success', 'Persetujuan group leader berhasil dicatat.');
      } else {
        await approvalService.rejectRequest(dbId, decisionTarget.req.id, trimmed);
        pushToast('info', 'Permintaan migration ditolak dengan catatan.');
      }
      setDecisionTarget(null);
      setDecisionComment('');
      await loadRequests();
      await onStatusUpdated?.();
    } catch (err) {
      setDecisionError(err instanceof ApiError ? err.message : 'Gagal menyimpan keputusan review.');
    } finally {
      setDeciding(false);
    }
  };

  const latestRequest = requests[0] ?? null;

  return (
    <section
      aria-labelledby={sectionHeadingId}
      className="mt-5 space-y-4 border-t border-border pt-5"
    >
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 id={sectionHeadingId} className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink">
          <ShieldCheck className="h-4 w-4 text-accent" aria-hidden="true" />
          <span>Workflow &amp; Multi-Group Leader Approval</span>
        </h3>
        <div className="flex flex-wrap items-center gap-2">
          <Button variant="ghost" size="sm" loading={loading} onClick={() => void loadRequests()}>
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span className="sr-only-live">Muat ulang approval</span>
          </Button>
          {canEdit && (
            <Button variant="secondary" size="sm" onClick={openSubmitDialog}>
              <Send className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Ajukan Review</span>
            </Button>
          )}
        </div>
      </div>

      <WorkflowStepper status={definition.workflow_status} />

      {loading ? (
        <div className="space-y-2" role="status" aria-label="Memuat status approval">
          <SkeletonLine className="h-4 w-56" />
          <SkeletonLine className="h-12 w-full" />
        </div>
      ) : backendUnavailable ? (
        <Alert variant="info" title="Menunggu endpoint Approval Backend">
          UI workflow &amp; matriks approval multi-group leader sudah terpasang mengikuti{' '}
          <code className="font-mono">api_contract.md §2.2</code>. Data approval akan otomatis muncul setelah endpoint{' '}
          <code className="font-mono">/api/databases/:dbId/approval-requests</code> aktif di backend.
        </Alert>
      ) : loadError ? (
        <Alert variant="error">{loadError}</Alert>
      ) : !latestRequest ? (
        <p className="rounded-lg border border-dashed border-border bg-surface-subtle/30 px-4 py-3 text-xs text-muted">
          Belum ada permintaan review untuk migration ini. Klik <strong>Ajukan Review</strong> agar seluruh group leader yang memiliki akses ke database ini (View Only maupun View + Edit) dapat memeriksa dan menyetujui perubahan.
        </p>
      ) : (
        <div className="space-y-3 rounded-lg border border-border bg-surface-subtle/30 p-4">
          <div className="flex flex-wrap items-start justify-between gap-2">
            <div>
              <div className="flex flex-wrap items-center gap-2">
                <span className="text-xs font-semibold text-ink">
                  Request #{latestRequest.id} · Aksi:{' '}
                  <code className="font-mono">{latestRequest.action}</code>
                </span>
                <Badge tone={REQUEST_STATUS_TONES[latestRequest.status]}>{latestRequest.status}</Badge>
                <Badge tone="neutral" className="tabular-nums">
                  {latestRequest.approved_count}/{latestRequest.required_approvals} leader setuju
                </Badge>
              </div>
              <p className="mt-1 text-xs text-muted">
                Diajukan oleh <span className="font-mono text-ink">{latestRequest.requested_by}</span> pada{' '}
                {formatDateTime(latestRequest.created_at)}
                {latestRequest.note ? ` — "${latestRequest.note}"` : ''}
              </p>
            </div>

            {latestRequest.status === 'pending_review' && latestRequest.can_current_user_decide && (
              <div className="flex flex-wrap items-center gap-1.5">
                <Button
                  variant="primary"
                  size="sm"
                  onClick={() => {
                    setDecisionTarget({ req: latestRequest, kind: 'approve' });
                    setDecisionComment('');
                    setDecisionError('');
                  }}
                >
                  <ThumbsUp className="h-3.5 w-3.5" aria-hidden="true" />
                  <span>Approve</span>
                </Button>
                <Button
                  variant="danger"
                  size="sm"
                  onClick={() => {
                    setDecisionTarget({ req: latestRequest, kind: 'reject' });
                    setDecisionComment('');
                    setDecisionError('');
                  }}
                >
                  <ThumbsDown className="h-3.5 w-3.5" aria-hidden="true" />
                  <span>Reject</span>
                </Button>
              </div>
            )}
          </div>

          <ApprovalMatrixList request={latestRequest} />
        </div>
      )}

      <FormDialog
        open={submitOpen}
        title={`Ajukan Review — v${definition.version} ${definition.name}`}
        description="Permintaan review akan dikirim ke leader setiap group yang memiliki akses ke database ini (View Only & View + Edit)."
        busy={submitting}
        onClose={() => setSubmitOpen(false)}
      >
        {submitError && (
          <Alert variant="error" className="mb-4">
            {submitError}
          </Alert>
        )}
        <form onSubmit={handleSubmitReview} className="space-y-4" noValidate>
          <div className="space-y-1">
            <label htmlFor="review-action-select" className="block text-sm font-medium text-ink">
              Jenis Aksi yang Diajukan
            </label>
            <select
              id="review-action-select"
              name="action"
              value={submitAction}
              onChange={(e) => setSubmitAction(e.target.value as MigrationAction)}
              className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
            >
              <option value={definition.execution_mode === 'batch' ? 'batch_up' : 'up'}>
                {definition.execution_mode === 'batch' ? 'batch_up (Eksekusi Batch)' : 'up (Eksekusi Normal)'}
              </option>
              {!definition.is_irreversible && (
                <option value={definition.execution_mode === 'batch' ? 'batch_revert' : 'rollback'}>
                  {definition.execution_mode === 'batch' ? 'batch_revert (Batalkan Batch)' : 'rollback (Rollback SQL Down)'}
                </option>
              )}
              <option value="forward">forward (Jalankan Ulang SQL Up)</option>
            </select>
            {definition.execution_mode === 'batch' && (
              <p className="text-xs text-muted">
                Untuk mengajukan review <code className="font-mono">batch_resume</code> atau{' '}
                <code className="font-mono">batch_revert</code> pada execution berstatus{' '}
                <code className="font-mono">partial</code>, gunakan tombol review langsung pada halaman{' '}
                <strong>History</strong> agar <code className="font-mono">target_execution_id</code> ikut tercatat.
              </p>
            )}
          </div>

          <div className="space-y-1">
            <label htmlFor="review-note" className="block text-sm font-medium text-ink">
              Catatan untuk Reviewer / Group Leader (Opsional)
            </label>
            <textarea
              id="review-note"
              name="note"
              rows={3}
              value={submitNote}
              onChange={(e) => setSubmitNote(e.target.value)}
              placeholder="Jelaskan dampak perubahan schema/data bagi layanan lain yang berbagi database ini."
              className="w-full rounded-md border border-border bg-surface p-2.5 text-sm text-ink"
            />
          </div>

          <div className="flex justify-end gap-2">
            <Button variant="secondary" disabled={submitting} onClick={() => setSubmitOpen(false)}>
              Batal
            </Button>
            <Button type="submit" variant="primary" loading={submitting}>
              <Send className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Kirim untuk Review</span>
            </Button>
          </div>
        </form>
      </FormDialog>

      <FormDialog
        open={Boolean(decisionTarget)}
        title={
          decisionTarget?.kind === 'approve'
            ? `Approve Request #${decisionTarget.req.id}`
            : `Reject Request #${decisionTarget?.req.id ?? ''}`
        }
        description={
          decisionTarget?.kind === 'approve'
            ? 'Konfirmasi bahwa perubahan ini kompatibel dengan layanan yang dikelola group kamu.'
            : 'Tuliskan alasan penolakan agar pembuat migration dapat memperbaiki definisinya.'
        }
        busy={deciding}
        onClose={() => setDecisionTarget(null)}
      >
        {decisionError && (
          <Alert variant="error" className="mb-4">
            {decisionError}
          </Alert>
        )}
        <form onSubmit={handleDecisionSubmit} className="space-y-4" noValidate>
          <div className="space-y-1">
            <label htmlFor="panel-decision-comment" className="block text-sm font-medium text-ink">
              {decisionTarget?.kind === 'reject' ? (
                <>
                  Alasan Penolakan <span aria-hidden="true" className="text-danger">*</span>
                </>
              ) : (
                'Catatan Persetujuan (Opsional)'
              )}
            </label>
            <textarea
              ref={commentRef}
              id="panel-decision-comment"
              name="comment"
              rows={3}
              required={decisionTarget?.kind === 'reject'}
              value={decisionComment}
              onChange={(e) => {
                setDecisionComment(e.target.value);
                if (decisionError) setDecisionError('');
              }}
              placeholder={
                decisionTarget?.kind === 'reject'
                  ? 'Contoh: Menghapus kolom status akan mematahkan query service analytics.'
                  : 'Aman untuk service tim kami.'
              }
              className="w-full rounded-md border border-border bg-surface p-2.5 text-sm text-ink"
            />
          </div>
          <div className="flex justify-end gap-2">
            <Button variant="secondary" disabled={deciding} onClick={() => setDecisionTarget(null)}>
              Batal
            </Button>
            <Button
              type="submit"
              variant={decisionTarget?.kind === 'reject' ? 'danger' : 'primary'}
              loading={deciding}
            >
              {decisionTarget?.kind === 'reject' ? 'Tolak Perubahan' : 'Setujui Perubahan'}
            </Button>
          </div>
        </form>
      </FormDialog>
    </section>
  );
}

export function ApprovalMatrixList({ request }: { request: ApprovalRequest }) {
  if (!request.approvals || request.approvals.length === 0) {
    return <p className="text-xs text-muted">Belum ada daftar group reviewer.</p>;
  }

  return (
    <ul className="divide-y divide-border rounded-md border border-border bg-surface">
      {request.approvals.map((item, idx) => (
        <li
          key={item.group_id ?? `${item.group_name}-${idx}`}
          className="flex flex-col gap-1.5 px-3.5 py-2.5 text-xs sm:flex-row sm:items-center sm:justify-between"
        >
          <div className="min-w-0 space-y-0.5">
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="font-semibold text-ink">{item.group_name}</span>
              <AccessBadge level={item.access_level} />
              <span className="inline-flex items-center gap-1 text-muted">
                <Crown className="h-3 w-3 text-accent" aria-hidden="true" />
                <span className="font-mono text-ink">
                  {item.leader_username || item.decided_by || '—'}
                </span>
              </span>
            </div>
            {item.comment && <p className="text-muted">“{item.comment}”</p>}
          </div>

          <div className="flex shrink-0 items-center gap-2">
            {item.decision === 'approved' ? (
              <Badge tone="ok">
                <CheckCircle2 className="h-3 w-3" aria-hidden="true" />
                <span>Approved</span>
              </Badge>
            ) : item.decision === 'rejected' ? (
              <Badge tone="danger">
                <XCircle className="h-3 w-3" aria-hidden="true" />
                <span>Rejected</span>
              </Badge>
            ) : (
              <Badge tone="warn">
                <Clock className="h-3 w-3" aria-hidden="true" />
                <span>Pending</span>
              </Badge>
            )}
            {item.decided_at && (
              <span className="text-[11px] text-muted tabular-nums">
                {formatDateTime(item.decided_at)}
              </span>
            )}
          </div>
        </li>
      ))}
    </ul>
  );
}
