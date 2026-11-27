import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { Link } from 'react-router-dom';
import {
  ArrowRight,
  CheckSquare,
  Code2,
  Crown,
  Database,
  RefreshCw,
  ThumbsDown,
  ThumbsUp,
} from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import * as approvalService from '@/services/approvals';
import * as migrationService from '@/services/migrations';
import type { ApprovalRequest, MigrationDefinition } from '@/types/api';
import { ApprovalMatrixList } from '@/components/migration/ApprovalPanel';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SkeletonLine } from '@/components/ui/Skeleton';

export function ApprovalsPage() {
  const { user, isLeader } = useAuth();
  const { pushToast } = useToast();

  const [requests, setRequests] = useState<ApprovalRequest[]>([]);
  const [loading, setLoading] = useState(true);
  const [backendUnavailable, setBackendUnavailable] = useState(false);
  const [error, setError] = useState('');

  const [selectedReqId, setSelectedReqId] = useState<number | null>(null);
  const [definitionPreview, setDefinitionPreview] = useState<MigrationDefinition | null>(null);
  const [loadingPreview, setLoadingPreview] = useState(false);

  const [comment, setComment] = useState('');
  const [decisionError, setDecisionError] = useState('');
  const [submittingDecision, setSubmittingDecision] = useState<'approve' | 'reject' | null>(null);
  const commentRef = useRef<HTMLTextAreaElement>(null);

  const loadPending = useCallback(async () => {
    setLoading(true);
    setError('');
    setBackendUnavailable(false);
    try {
      const list = await approvalService.listPendingApprovals();
      setRequests(list);
      setSelectedReqId((prev) => {
        if (prev && list.some((r) => r.id === prev)) return prev;
        return list.length > 0 ? list[0].id : null;
      });
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setBackendUnavailable(true);
        setRequests([]);
      } else {
        setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar approval.');
      }
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadPending();
  }, [loadPending]);

  const selectedReq = useMemo(
    () => requests.find((r) => r.id === selectedReqId) ?? null,
    [requests, selectedReqId],
  );

  useEffect(() => {
    if (!selectedReq) {
      setDefinitionPreview(null);
      return;
    }
    let cancelled = false;
    setLoadingPreview(true);
    setComment('');
    setDecisionError('');

    migrationService
      .getDefinition(selectedReq.database_id, selectedReq.migration_id)
      .then((def) => {
        if (!cancelled) setDefinitionPreview(def);
      })
      .catch(() => {
        if (!cancelled) setDefinitionPreview(null);
      })
      .finally(() => {
        if (!cancelled) setLoadingPreview(false);
      });

    return () => {
      cancelled = true;
    };
  }, [selectedReq]);

  const handleDecision = async (kind: 'approve' | 'reject') => {
    if (!selectedReq) return;
    const trimmed = comment.trim();
    if (kind === 'reject' && !trimmed) {
      setDecisionError('Alasan penolakan wajib diisi agar pembuat migration mengetahui dampaknya.');
      commentRef.current?.focus();
      return;
    }

    setDecisionError('');
    setSubmittingDecision(kind);
    try {
      if (kind === 'approve') {
        await approvalService.approveRequest(selectedReq.database_id, selectedReq.id, trimmed);
        pushToast(
          'success',
          `Request #${selectedReq.id} (v${selectedReq.migration_version} ${selectedReq.action}) disetujui.`,
        );
      } else {
        await approvalService.rejectRequest(selectedReq.database_id, selectedReq.id, trimmed);
        pushToast(
          'info',
          `Request #${selectedReq.id} (v${selectedReq.migration_version} ${selectedReq.action}) ditolak.`,
        );
      }
      setComment('');
      await loadPending();
    } catch (err) {
      setDecisionError(err instanceof ApiError ? err.message : 'Gagal mengirim keputusan review.');
    } finally {
      setSubmittingDecision(null);
    }
  };

  return (
    <div>
      <PageHeader
        title="Approval Workflow"
        description="Tinjau dan setujui atau tolak perubahan database (migration, rollback, forward, dan batch recovery) untuk seluruh group yang kamu pimpin, termasuk group dengan akses View Only."
        actions={
          <Button variant="secondary" size="sm" loading={loading} onClick={() => void loadPending()}>
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Muat Ulang</span>
          </Button>
        }
      />

      {user?.led_groups && user.led_groups.length > 0 && (
        <div className="mb-5 flex flex-wrap items-center gap-2 text-xs text-muted">
          <span className="inline-flex items-center gap-1 font-medium text-ink">
            <Crown className="h-3.5 w-3.5 text-accent" aria-hidden="true" />
            <span>Group yang kamu pimpin:</span>
          </span>
          {user.led_groups.map((g) => (
            <Badge key={g.id} tone="accent">
              {g.name}
            </Badge>
          ))}
        </div>
      )}

      {backendUnavailable ? (
        <Alert variant="info" title="Menunggu endpoint Approval Backend">
          <p>
            Halaman inbox approval multi-group leader sudah dibangun sesuai{' '}
            <code className="font-mono">api_contract.md §2.2</code> (endpoint{' '}
            <code className="font-mono">GET /api/approvals/pending</code>,{' '}
            <code className="font-mono">POST .../approve</code>, dan{' '}
            <code className="font-mono">POST .../reject</code>).
          </p>
          <p className="mt-1">
            Setelah tim backend mengaktifkan fitur Group Leader &amp; Approval, seluruh permintaan review lintas database akan tampil otomatis di sini.
          </p>
        </Alert>
      ) : error ? (
        <Alert variant="error">{error}</Alert>
      ) : loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat daftar approval">
          <SkeletonLine className="h-5 w-64" />
          <SkeletonLine className="h-14 w-full" />
          <SkeletonLine className="h-14 w-full" />
        </div>
      ) : requests.length === 0 ? (
        <EmptyState
          icon={CheckSquare}
          title="Tidak ada permintaan review yang menunggu keputusanmu"
          description={
            isLeader
              ? 'Semua permintaan migration untuk group yang kamu pimpin sudah diproses.'
              : 'Jika kamu ditunjuk sebagai leader suatu group oleh Platform Admin, permintaan review untuk database yang diakses group-mu akan muncul di sini.'
          }
          action={
            <Link
              to="/databases"
              className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border bg-surface px-3.5 py-1.5 text-sm font-medium text-ink no-underline hover:bg-surface-subtle"
            >
              <span>Buka Database Saya</span>
              <ArrowRight className="h-4 w-4" aria-hidden="true" />
            </Link>
          }
        />
      ) : (
        <div className="grid grid-cols-1 gap-5 xl:grid-cols-[340px_minmax(0,1fr)] xl:items-start">
          <section aria-label="Daftar permintaan review menunggu" className="card-surface min-w-0 p-3.5">
            <h2 className="mb-2.5 px-1 text-sm font-semibold text-ink tabular-nums">
              Menunggu Keputusan ({requests.length})
            </h2>
            <ul className="max-h-72 space-y-1.5 overflow-y-auto pr-0.5 xl:max-h-[calc(100dvh-14rem)]">
              {requests.map((req) => {
                const selected = req.id === selectedReqId;
                return (
                  <li key={req.id}>
                    <button
                      type="button"
                      onClick={() => setSelectedReqId(req.id)}
                      aria-current={selected ? 'true' : undefined}
                      className={`w-full cursor-pointer rounded-lg border p-3 text-left transition-colors ${
                        selected
                          ? 'border-accent bg-accent-soft/50'
                          : 'border-border bg-surface hover:bg-surface-subtle/60'
                      }`}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <p className="flex items-center gap-1.5 text-xs text-muted">
                            <Database className="h-3 w-3 shrink-0" aria-hidden="true" />
                            <span className="truncate font-medium text-ink">
                              {req.database_name || `Database #${req.database_id}`}
                            </span>
                          </p>
                          <p className="mt-1 truncate text-sm font-semibold text-ink">
                            <span className="font-mono text-xs">v{req.migration_version}</span>{' '}
                            {req.migration_name || '(tanpa nama)'}
                          </p>
                        </div>
                        <Badge tone="warn">{req.action}</Badge>
                      </div>
                      <div className="mt-2 flex items-center justify-between text-[11px] text-muted tabular-nums">
                        <span>oleh {req.requested_by}</span>
                        <span>
                          {req.approved_count}/{req.required_approvals} setuju
                        </span>
                      </div>
                    </button>
                  </li>
                );
              })}
            </ul>
          </section>

          {selectedReq && (
            <section className="card-surface min-w-0 space-y-5 p-4 sm:p-5" aria-label="Detail tinjauan migration">
              <div className="flex flex-wrap items-start justify-between gap-3 border-b border-border pb-4">
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <h2 className="text-base font-bold text-ink">
                      Request #{selectedReq.id} | v{selectedReq.migration_version}{' '}
                      {selectedReq.migration_name}
                    </h2>
                    <Badge tone="warn">{selectedReq.action}</Badge>
                    <Badge tone="neutral" className="tabular-nums">
                      {selectedReq.approved_count}/{selectedReq.required_approvals} leader setuju
                    </Badge>
                  </div>
                  <p className="mt-1 text-xs text-muted">
                    Database:{' '}
                    <Link
                      to={`/databases/${selectedReq.database_id}/migrations`}
                      className="font-semibold text-accent hover:underline"
                    >
                      {selectedReq.database_name || `Database #${selectedReq.database_id}`}
                    </Link>{' '}
                    · Diajukan oleh <span className="font-mono text-ink">{selectedReq.requested_by}</span> pada{' '}
                    {formatDateTime(selectedReq.created_at)}
                  </p>
                  {selectedReq.note && (
                    <p className="mt-2 rounded-md bg-surface-subtle px-3 py-2 text-xs text-ink">
                      Catatan pengaju: “{selectedReq.note}”
                    </p>
                  )}
                </div>
              </div>

              <div className="space-y-3">
                <h3 className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink">
                  <Code2 className="h-4 w-4 text-accent" aria-hidden="true" />
                  <span>Pratinjau Definisi SQL</span>
                </h3>

                {loadingPreview ? (
                  <div className="space-y-2" role="status" aria-label="Memuat pratinjau SQL">
                    <SkeletonLine className="h-24 w-full" />
                  </div>
                ) : !definitionPreview ? (
                  <p className="text-xs text-muted">
                    Pratinjau SQL tidak tersedia. Buka halaman Migrations pada database terkait untuk melihat detail lengkap.
                  </p>
                ) : (
                  <div className="space-y-3">
                    <div className="flex flex-wrap items-center gap-2 text-xs">
                      <Badge tone={definitionPreview.execution_mode === 'batch' ? 'accent' : 'neutral'}>
                        mode: {definitionPreview.execution_mode}
                      </Badge>
                      {definitionPreview.is_irreversible && (
                        <Badge tone="warn">Irreversible</Badge>
                      )}
                      {definitionPreview.batch_config && (
                        <span className="font-mono text-muted">
                          {definitionPreview.batch_config.schema || 'public'}.
                          {definitionPreview.batch_config.table} · cursor:{' '}
                          {definitionPreview.batch_config.cursor_column} · size:{' '}
                          {definitionPreview.batch_config.batch_size}
                        </span>
                      )}
                    </div>

                    <div>
                      <p className="mb-1 text-xs font-medium text-muted">SQL Up</p>
                      <pre className="max-h-56 overflow-auto rounded-lg border border-border bg-surface-subtle p-3 font-mono text-xs leading-relaxed text-ink">
                        {definitionPreview.sql_up}
                      </pre>
                    </div>

                    {definitionPreview.is_irreversible ? (
                      <Alert variant="warning" title="Migration Irreversible">
                        {definitionPreview.irreversible_reason}
                      </Alert>
                    ) : (
                      definitionPreview.sql_down && (
                        <div>
                          <p className="mb-1 text-xs font-medium text-muted">SQL Down (Rollback)</p>
                          <pre className="max-h-44 overflow-auto rounded-lg border border-border bg-surface-subtle p-3 font-mono text-xs leading-relaxed text-ink">
                            {definitionPreview.sql_down}
                          </pre>
                        </div>
                      )
                    )}
                  </div>
                )}
              </div>

              <div className="space-y-2">
                <h3 className="text-sm font-semibold text-ink">Status Persetujuan Tiap Group</h3>
                <ApprovalMatrixList request={selectedReq} />
              </div>

              {selectedReq.can_current_user_decide && (
                <div className="space-y-3 border-t border-border pt-4">
                  <h3 className="text-sm font-semibold text-ink">Keputusan Kamu sebagai Group Leader</h3>

                  {decisionError && <Alert variant="error">{decisionError}</Alert>}

                  <div className="space-y-1">
                    <label htmlFor="inbox-decision-comment" className="block text-xs font-medium text-ink">
                      Komentar (opsional untuk Approve, wajib diisi untuk Reject)
                    </label>
                    <textarea
                      ref={commentRef}
                      id="inbox-decision-comment"
                      name="comment"
                      rows={3}
                      value={comment}
                      onChange={(e) => {
                        setComment(e.target.value);
                        if (decisionError) setDecisionError('');
                      }}
                      placeholder="Tuliskan hasil verifikasi kompatibilitas aplikasi/service kamu…"
                      className="w-full rounded-md border border-border bg-surface p-2.5 text-sm text-ink"
                    />
                  </div>

                  <div className="flex flex-wrap justify-end gap-2">
                    <Button
                      variant="danger"
                      loading={submittingDecision === 'reject'}
                      disabled={submittingDecision !== null}
                      onClick={() => void handleDecision('reject')}
                    >
                      <ThumbsDown className="h-4 w-4" aria-hidden="true" />
                      <span>Reject Perubahan</span>
                    </Button>
                    <Button
                      variant="primary"
                      loading={submittingDecision === 'approve'}
                      disabled={submittingDecision !== null}
                      onClick={() => void handleDecision('approve')}
                    >
                      <ThumbsUp className="h-4 w-4" aria-hidden="true" />
                      <span>Approve Perubahan</span>
                    </Button>
                  </div>
                </div>
              )}
            </section>
          )}
        </div>
      )}
    </div>
  );
}
