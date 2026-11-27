import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import { Link } from 'react-router-dom';
import {
  FastForward,
  FileCode2,
  GitBranch,
  Lock,
  Play,
  Plus,
  RefreshCw,
  RotateCcw,
  Save,
  ShieldAlert,
  Sparkles,
} from 'lucide-react';
import { useDatabaseContext } from '@/components/layout/DatabaseLayout';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import { formatDateTime, generateIdempotencyKey } from '@/lib/format';
import * as migrationService from '@/services/migrations';
import type {
  ExecutionMode,
  MigrationDefinition,
  MigrationDefinitionInput,
  MigrationJob,
} from '@/types/api';
import { ApprovalPanel } from '@/components/migration/ApprovalPanel';
import { ExplainPanel } from '@/components/migration/ExplainPanel';
import { LiveJobTracker } from '@/components/migration/LiveJobTracker';
import { Alert } from '@/components/ui/Alert';
import { Badge, StatusBadge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { EmptyState } from '@/components/ui/EmptyState';
import { SkeletonLine } from '@/components/ui/Skeleton';

interface EditorState {
  name: string;
  sql_up: string;
  sql_down: string;
  is_irreversible: boolean;
  irreversible_reason: string;
  execution_mode: ExecutionMode;
  batch_schema: string;
  batch_table: string;
  batch_cursor_column: string;
  batch_size: string;
  batch_where_clause: string;
  batch_capture_columns: string;
  batch_delay_ms: string;
}

const EMPTY_EDITOR: EditorState = {
  name: '',
  sql_up: '',
  sql_down: '',
  is_irreversible: false,
  irreversible_reason: '',
  execution_mode: 'normal',
  batch_schema: 'public',
  batch_table: '',
  batch_cursor_column: 'id',
  batch_size: '1000',
  batch_where_clause: '',
  batch_capture_columns: '',
  batch_delay_ms: '0',
};

function definitionToEditor(def: MigrationDefinition): EditorState {
  return {
    name: def.name,
    sql_up: def.sql_up,
    sql_down: def.sql_down ?? '',
    is_irreversible: def.is_irreversible,
    irreversible_reason: def.irreversible_reason ?? '',
    execution_mode: def.execution_mode,
    batch_schema: def.batch_config?.schema || 'public',
    batch_table: def.batch_config?.table || '',
    batch_cursor_column: def.batch_config?.cursor_column || 'id',
    batch_size: String(def.batch_config?.batch_size || 1000),
    batch_where_clause: def.batch_config?.where_clause || '',
    batch_capture_columns: (def.batch_config?.capture_columns ?? []).join(', '),
    batch_delay_ms: String(def.batch_config?.batch_delay_ms ?? 0),
  };
}

type PendingActionKind = 'execute' | 'rollback' | 'forward';

export function MigrationDefinitionsPage() {
  const { database, canEdit, refreshLock } = useDatabaseContext();
  const { pushToast } = useToast();

  const [definitions, setDefinitions] = useState<MigrationDefinition[]>([]);
  const [loading, setLoading] = useState(true);
  const [listError, setListError] = useState('');

  const [mode, setMode] = useState<'create' | 'inspect'>('create');
  const [selectedId, setSelectedId] = useState<number | null>(null);
  const [editor, setEditor] = useState<EditorState>(EMPTY_EDITOR);
  const [formError, setFormError] = useState('');
  const [saving, setSaving] = useState(false);

  const [pendingAction, setPendingAction] = useState<{
    kind: PendingActionKind;
    def: MigrationDefinition;
  } | null>(null);
  const [enqueuing, setEnqueuing] = useState(false);
  const [actionError, setActionError] = useState<{
    message: string;
    code?: string;
    blockingExecutionId?: number;
  } | null>(null);
  const [trackedJob, setTrackedJob] = useState<{ jobId: string; initialJob: MigrationJob } | null>(null);

  const sqlUpRef = useRef<HTMLTextAreaElement>(null);
  const sqlDownRef = useRef<HTMLTextAreaElement>(null);
  const irrevReasonRef = useRef<HTMLTextAreaElement>(null);
  const batchTableRef = useRef<HTMLInputElement>(null);
  const editorHeadingId = useId();

  const loadDefinitions = useCallback(async () => {
    setLoading(true);
    setListError('');
    try {
      const list = await migrationService.listDefinitions(database.id);
      setDefinitions(list);
      setSelectedId((prev) => {
        if (prev && list.some((d) => d.id === prev)) return prev;
        return list.length > 0 ? list[0].id : null;
      });
      if (list.length > 0) {
        setMode((m) => (m === 'create' && selectedId === null ? 'inspect' : m));
      } else {
        setMode('create');
      }
    } catch (err) {
      setListError(err instanceof ApiError ? err.message : 'Gagal memuat daftar definisi migration.');
    } finally {
      setLoading(false);
    }
  }, [database.id]);

  useEffect(() => {
    void loadDefinitions();
  }, [loadDefinitions]);

  const selectedDef = useMemo(
    () => definitions.find((d) => d.id === selectedId) ?? null,
    [definitions, selectedId],
  );

  useEffect(() => {
    if (mode === 'inspect' && selectedDef) {
      setEditor(definitionToEditor(selectedDef));
      setFormError('');
    }
  }, [mode, selectedDef]);

  const activeDef = useMemo(() => {
    const active = definitions.filter((d) => d.is_active);
    if (active.length === 0) return null;
    return active.reduce((acc, cur) => (cur.version > acc.version ? cur : acc), active[0]);
  }, [definitions]);

  const startCreateNew = () => {
    setMode('create');
    setEditor(EMPTY_EDITOR);
    setFormError('');
    setActionError(null);
  };

  const selectDefinition = (def: MigrationDefinition) => {
    setSelectedId(def.id);
    setMode('inspect');
    setEditor(definitionToEditor(def));
    setFormError('');
    setActionError(null);
  };

  const applyBatchSampleTemplate = () => {
    const tbl = editor.batch_table.trim() || 'items';
    const schema = editor.batch_schema.trim() || 'public';
    const cursor = editor.batch_cursor_column.trim() || 'id';
    setEditor((prev) => ({
      ...prev,
      execution_mode: 'batch',
      batch_schema: schema,
      batch_table: tbl,
      batch_cursor_column: cursor,
      sql_up: `UPDATE ${schema}.${tbl}\nSET updated_at = now()\nWHERE ${cursor} = ANY({{batch_ids}});`,
      sql_down: prev.is_irreversible
        ? prev.sql_down
        : `UPDATE ${schema}.${tbl} AS t\nSET updated_at = r.updated_at\nFROM jsonb_to_recordset({{recovery_json}}) AS r(${cursor} bigint, updated_at timestamptz)\nWHERE t.${cursor} = r.${cursor};`,
      batch_capture_columns: prev.batch_capture_columns || 'updated_at',
    }));
  };

  const handleSaveDefinition = async (e: FormEvent) => {
    e.preventDefault();
    setFormError('');

    if (!editor.sql_up.trim()) {
      setFormError('SQL Up tidak boleh kosong.');
      sqlUpRef.current?.focus();
      return;
    }
    if (editor.is_irreversible) {
      if (!editor.irreversible_reason.trim()) {
        setFormError('Alasan wajib diisi untuk migration yang ditandai irreversible.');
        irrevReasonRef.current?.focus();
        return;
      }
    } else if (!editor.sql_down.trim()) {
      setFormError('SQL Down wajib diisi, atau tandai migration sebagai irreversible beserta alasannya.');
      sqlDownRef.current?.focus();
      return;
    }

    const input: MigrationDefinitionInput = {
      name: editor.name.trim(),
      sql_up: editor.sql_up.trim(),
      sql_down: editor.is_irreversible ? '' : editor.sql_down.trim(),
      is_irreversible: editor.is_irreversible,
      irreversible_reason: editor.is_irreversible ? editor.irreversible_reason.trim() : '',
      execution_mode: editor.execution_mode,
    };

    if (editor.execution_mode === 'batch') {
      const batchSize = Number.parseInt(editor.batch_size.trim(), 10);
      const delayMs = Number.parseInt(editor.batch_delay_ms.trim() || '0', 10);
      if (!editor.batch_table.trim()) {
        setFormError('Nama tabel target wajib diisi untuk mode batch.');
        batchTableRef.current?.focus();
        return;
      }
      if (!Number.isInteger(batchSize) || batchSize < 1 || batchSize > 100000) {
        setFormError('Batch size harus antara 1 dan 100.000.');
        return;
      }
      const captureCols = editor.batch_capture_columns
        .split(',')
        .map((c) => c.trim())
        .filter(Boolean);

      input.batch_config = {
        schema: editor.batch_schema.trim() || 'public',
        table: editor.batch_table.trim(),
        cursor_column: editor.batch_cursor_column.trim() || 'id',
        batch_size: batchSize,
        where_clause: editor.batch_where_clause.trim() || undefined,
        capture_columns: captureCols.length > 0 ? captureCols : undefined,
        batch_delay_ms: Number.isInteger(delayMs) && delayMs >= 0 ? delayMs : 0,
      };
    }

    setSaving(true);
    try {
      if (mode === 'create') {
        const created = await migrationService.createDefinition(database.id, input);
        pushToast('success', `Definisi migration v${created.version} berhasil dibuat.`);
        await loadDefinitions();
        setSelectedId(created.id);
        setMode('inspect');
      } else if (selectedDef) {
        const updated = await migrationService.updateDefinition(database.id, selectedDef.id, input);
        pushToast('success', `Definisi migration v${updated.version} (rev ${updated.revision}) diperbarui.`);
        await loadDefinitions();
      }
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Gagal menyimpan definisi migration.');
    } finally {
      setSaving(false);
    }
  };

  const handleConfirmAsyncAction = async () => {
    if (!pendingAction) return;
    setEnqueuing(true);
    setActionError(null);
    const { kind, def } = pendingAction;
    const key = generateIdempotencyKey(`${kind}-v${def.version}`);

    try {
      const res =
        kind === 'execute'
          ? await migrationService.enqueueExecute(database.id, def.id, key)
          : kind === 'rollback'
            ? await migrationService.enqueueRollback(database.id, def.id, key)
            : await migrationService.enqueueForward(database.id, def.id, key);

      pushToast('info', `${res.message} (Job ${res.job_id.slice(0, 8)}…)`);
      setTrackedJob({ jobId: res.job_id, initialJob: res.job });
      setPendingAction(null);
      await Promise.all([loadDefinitions(), refreshLock()]);
    } catch (err) {
      setPendingAction(null);
      if (err instanceof ApiError) {
        setActionError({
          message: err.message,
          code: err.code,
          blockingExecutionId: err.blockingExecutionId,
        });
      } else {
        setActionError({ message: 'Gagal memasukkan job migration ke antrean.' });
      }
    } finally {
      setEnqueuing(false);
    }
  };

  const handleTrackedJobFinished = useCallback(
    async (job: MigrationJob) => {
      await Promise.all([loadDefinitions(), refreshLock()]);
      if (job.status === 'completed') {
        pushToast('success', `Job ${job.action} selesai dan berhasil di-commit.`);
      } else if (job.status === 'failed') {
        pushToast('error', `Job ${job.action} gagal: ${job.last_error || 'periksa detail di History.'}`);
      } else if (job.status === 'partial' || job.status === 'partially_reverted') {
        pushToast('info', `Job ${job.action} berhenti dengan status ${job.status}. Lanjutkan atau revert di History.`);
      }
    },
    [loadDefinitions, pushToast, refreshLock],
  );

  const isEditingImmutable = mode === 'inspect' && Boolean(selectedDef?.is_locked_immutable);
  const formReadOnly = !canEdit || isEditingImmutable;

  const confirmCopy = useMemo(() => {
    if (!pendingAction) return null;
    const { kind, def } = pendingAction;
    const label = `v${def.version}${def.name ? ` — ${def.name}` : ''}`;
    if (kind === 'execute') {
      return {
        title: `Jalankan migration ${label}?`,
        description: `Migration (${def.execution_mode}) akan dipublikasikan ke queue dan diproses secara asynchronous oleh worker. Setelah pernah dieksekusi, SQL up/down pada definisi ini menjadi immutable.`,
        confirmLabel: 'Masukkan ke Antrean',
        destructive: false,
      };
    }
    if (kind === 'rollback') {
      return {
        title: `Rollback migration ${label}?`,
        description: `Worker akan menjalankan SQL down asli yang immutable secara asynchronous untuk membatalkan perubahan aktif terakhir. Rollback tercatat sebagai execution baru di history.`,
        confirmLabel: 'Jalankan Rollback',
        destructive: true,
      };
    }
    return {
      title: `Forward migration ${label}?`,
      description: `Worker akan menjalankan kembali SQL up asli secara linear (hanya migration ini, tidak otomatis menjalankan versi berikutnya).`,
      confirmLabel: 'Jalankan Forward',
      destructive: false,
    };
  }, [pendingAction]);

  return (
    <div className="space-y-5">
      {trackedJob && (
        <LiveJobTracker
          dbId={database.id}
          jobId={trackedJob.jobId}
          initialJob={trackedJob.initialJob}
          onTerminal={(job) => void handleTrackedJobFinished(job)}
          onDismiss={() => setTrackedJob(null)}
        />
      )}

      {actionError && (
        <Alert
          variant="error"
          title={
            actionError.code === 'approval_required'
              ? 'Approval Leader Diperlukan'
              : actionError.code === 'database_blocked'
                ? 'Database Sedang Ditahan (Blocked)'
                : 'Aksi Migration Ditolak'
          }
        >
          <p>{actionError.message}</p>
          {actionError.code === 'database_blocked' && (
            <p className="mt-1.5">
              <Link to={`/databases/${database.id}/migrations/history`} className="font-semibold underline">
                Buka Migration History (Execution #{actionError.blockingExecutionId ?? '—'}) →
              </Link>
            </p>
          )}
          {actionError.code === 'approval_required' && (
            <p className="mt-1.5">
              <Link to="/approvals" className="font-semibold underline">
                Buka Approval Workflow →
              </Link>
            </p>
          )}
        </Alert>
      )}

      <div className="flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <div className="flex flex-wrap items-center gap-2 text-xs text-muted">
          <span className="tabular-nums">
            Total <strong className="text-ink">{definitions.length}</strong> definisi
          </span>
          <span>·</span>
          <span>
            Versi aktif terakhir:{' '}
            <strong className="font-mono text-ink">
              {activeDef ? `v${activeDef.version}` : 'Belum ada'}
            </strong>
          </span>
          {!canEdit && <Badge tone="neutral">Mode Read-Only (View Only)</Badge>}
        </div>

        <div className="flex flex-wrap items-center gap-2">
          <Button variant="secondary" size="sm" loading={loading} onClick={() => void loadDefinitions()}>
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Muat Ulang</span>
          </Button>
          {canEdit && (
            <Button variant="primary" size="sm" onClick={startCreateNew}>
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Buat Migration Baru</span>
            </Button>
          )}
        </div>
      </div>

      {listError && <Alert variant="error">{listError}</Alert>}

      <div className="grid gap-5 xl:grid-cols-[340px_minmax(0,1fr)] xl:items-start">
        <section aria-label="Daftar definisi migration" className="card-surface p-3.5">
          <h2 className="mb-2.5 flex items-center gap-1.5 px-1 text-sm font-semibold text-ink">
            <GitBranch className="h-4 w-4 text-accent" aria-hidden="true" />
            <span>Definisi Migration</span>
          </h2>

          {loading ? (
            <div className="space-y-2.5 p-1" role="status" aria-label="Memuat definisi">
              <SkeletonLine className="h-14 w-full rounded-md" />
              <SkeletonLine className="h-14 w-full rounded-md" />
              <SkeletonLine className="h-14 w-full rounded-md" />
            </div>
          ) : definitions.length === 0 ? (
            <EmptyState
              icon={FileCode2}
              title="Belum ada definisi migration"
              description={
                canEdit
                  ? 'Buat definisi migration pertama pada panel editor di samping.'
                  : 'Belum ada migration yang dibuat pada database ini.'
              }
            />
          ) : (
            <ul className="space-y-1.5">
              {definitions.map((def) => {
                const selected = mode === 'inspect' && def.id === selectedId;
                return (
                  <li key={def.id}>
                    <button
                      type="button"
                      onClick={() => selectDefinition(def)}
                      aria-current={selected ? 'true' : undefined}
                      className={`w-full cursor-pointer rounded-lg border p-3 text-left transition-colors ${
                        selected
                          ? 'border-accent bg-accent-soft/50'
                          : 'border-border bg-surface hover:bg-surface-subtle/60'
                      }`}
                    >
                      <div className="flex items-start justify-between gap-2">
                        <div className="min-w-0">
                          <div className="flex flex-wrap items-center gap-1.5">
                            <span className="font-mono text-xs font-bold text-ink">
                              v{def.version}
                            </span>
                            <span className="truncate text-sm font-semibold text-ink">
                              {def.name || '(tanpa nama)'}
                            </span>
                          </div>
                          <p className="mt-0.5 text-[11px] text-muted tabular-nums">
                            rev {def.revision} · {def.created_by} · {formatDateTime(def.updated_at)}
                          </p>
                        </div>
                        <StatusBadge status={def.workflow_status} />
                      </div>

                      <div className="mt-2 flex flex-wrap items-center gap-1.5">
                        <Badge tone={def.execution_mode === 'batch' ? 'accent' : 'neutral'}>
                          {def.execution_mode}
                        </Badge>
                        {def.is_active && <Badge tone="ok">Aktif</Badge>}
                        {def.is_locked_immutable && (
                          <Badge tone="neutral">
                            <Lock className="h-2.5 w-2.5" aria-hidden="true" />
                            <span>Immutable</span>
                          </Badge>
                        )}
                        {def.is_irreversible && <Badge tone="warn">Irreversible</Badge>}
                      </div>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        <section className="card-surface p-5" aria-labelledby={editorHeadingId}>
          <div className="flex flex-col gap-3 border-b border-border pb-4 sm:flex-row sm:items-start sm:justify-between">
            <div>
              <h2 id={editorHeadingId} className="text-base font-semibold text-ink">
                {mode === 'create'
                  ? 'Buat Definisi Migration Baru'
                  : selectedDef
                    ? `v${selectedDef.version} — ${selectedDef.name || '(tanpa nama)'}`
                    : 'Editor Migration'}
              </h2>
              {mode === 'inspect' && selectedDef ? (
                <p className="mt-0.5 text-xs text-muted tabular-nums">
                  ID #{selectedDef.id} · Revisi {selectedDef.revision} · Diperbarui oleh{' '}
                  <span className="font-mono text-ink">{selectedDef.updated_by}</span> pada{' '}
                  {formatDateTime(selectedDef.updated_at)}
                </p>
              ) : (
                <p className="mt-0.5 text-xs text-muted">
                  Sertakan SQL up dan SQL down (atau tandai irreversible beserta alasannya). Transaksi dikelola otomatis oleh platform — jangan tulis BEGIN/COMMIT.
                </p>
              )}
            </div>

            {mode === 'inspect' && selectedDef && canEdit && (
              <div className="flex flex-wrap items-center gap-2">
                {!selectedDef.is_active && !selectedDef.can_forward && (
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => setPendingAction({ kind: 'execute', def: selectedDef })}
                  >
                    <Play className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Execute Async</span>
                  </Button>
                )}
                {selectedDef.can_forward && (
                  <Button
                    variant="primary"
                    size="sm"
                    onClick={() => setPendingAction({ kind: 'forward', def: selectedDef })}
                  >
                    <FastForward className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Forward</span>
                  </Button>
                )}
                {selectedDef.can_rollback && !selectedDef.is_irreversible && (
                  <Button
                    variant="danger"
                    size="sm"
                    onClick={() => setPendingAction({ kind: 'rollback', def: selectedDef })}
                  >
                    <RotateCcw className="h-3.5 w-3.5" aria-hidden="true" />
                    <span>Rollback</span>
                  </Button>
                )}
              </div>
            )}
          </div>

          {isEditingImmutable && (
            <Alert variant="info" className="mt-4" title="Definisi Migration Bersifat Immutable">
              SQL up, SQL down, dan konfigurasi batch pada definisi yang pernah dieksekusi tidak dapat diubah. Jika perlu perbaikan, buat definisi migration baru.
            </Alert>
          )}

          {mode === 'inspect' && selectedDef?.is_irreversible && selectedDef.is_active && (
            <Alert variant="warning" className="mt-4" title="Migration Irreversible">
              Migration ini tidak dapat di-rollback. Alasan: {selectedDef.irreversible_reason}
            </Alert>
          )}

          {formError && (
            <Alert variant="error" className="mt-4">
              {formError}
            </Alert>
          )}

          <form aria-labelledby={editorHeadingId} onSubmit={handleSaveDefinition} className="mt-4 space-y-5" noValidate>
            <div className="space-y-1">
              <label htmlFor="mig-name" className="block text-sm font-medium text-ink">
                Nama Migration
              </label>
              <input
                id="mig-name"
                name="name"
                type="text"
                maxLength={200}
                disabled={formReadOnly}
                value={editor.name}
                onChange={(e) => setEditor((s) => ({ ...s, name: e.target.value }))}
                placeholder="mis. add_orders_status_index atau backfill_order_totals"
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink disabled:bg-surface-subtle disabled:opacity-80"
              />
            </div>

            <div className="grid gap-4 md:grid-cols-2">
              <fieldset className="space-y-2 rounded-lg border border-border bg-surface-subtle/40 p-3.5">
                <legend className="px-1 text-xs font-semibold text-muted">
                  Mode Eksekusi (Async via Queue)
                </legend>
                <div className="flex flex-col gap-2">
                  <div className="flex items-start gap-2">
                    <input
                      id="mode-normal"
                      name="execution_mode"
                      type="radio"
                      value="normal"
                      disabled={formReadOnly}
                      checked={editor.execution_mode === 'normal'}
                      onChange={() => setEditor((s) => ({ ...s, execution_mode: 'normal' }))}
                      className="mt-0.5 h-4 w-4"
                    />
                    <label htmlFor="mode-normal" className="text-sm text-ink">
                      <span className="font-medium">Normal</span>
                      <span className="block text-xs text-muted">
                        Jalankan SQL dalam satu transaksi secara asynchronous.
                      </span>
                    </label>
                  </div>
                  <div className="flex items-start gap-2">
                    <input
                      id="mode-batch"
                      name="execution_mode"
                      type="radio"
                      value="batch"
                      disabled={formReadOnly}
                      checked={editor.execution_mode === 'batch'}
                      onChange={() => setEditor((s) => ({ ...s, execution_mode: 'batch' }))}
                      className="mt-0.5 h-4 w-4"
                    />
                    <label htmlFor="mode-batch" className="text-sm text-ink">
                      <span className="font-medium">Per Batch (Dataset Besar)</span>
                      <span className="block text-xs text-muted">
                        Transaksi &amp; checkpoint terpisah per batch berdasarkan kolom cursor.
                      </span>
                    </label>
                  </div>
                </div>
              </fieldset>

              <fieldset className="space-y-2 rounded-lg border border-border bg-surface-subtle/40 p-3.5">
                <legend className="px-1 text-xs font-semibold text-muted">
                  Kebijakan Rollback
                </legend>
                <div className="flex flex-col gap-2">
                  <div className="flex items-start gap-2">
                    <input
                      id="rev-reversible"
                      name="reversibility"
                      type="radio"
                      value="reversible"
                      disabled={formReadOnly}
                      checked={!editor.is_irreversible}
                      onChange={() => setEditor((s) => ({ ...s, is_irreversible: false }))}
                      className="mt-0.5 h-4 w-4"
                    />
                    <label htmlFor="rev-reversible" className="text-sm text-ink">
                      <span className="font-medium">Reversible (SQL Down)</span>
                      <span className="block text-xs text-muted">
                        Sertakan SQL down untuk membatalkan perubahan.
                      </span>
                    </label>
                  </div>
                  <div className="flex items-start gap-2">
                    <input
                      id="rev-irreversible"
                      name="reversibility"
                      type="radio"
                      value="irreversible"
                      disabled={formReadOnly}
                      checked={editor.is_irreversible}
                      onChange={() => setEditor((s) => ({ ...s, is_irreversible: true }))}
                      className="mt-0.5 h-4 w-4"
                    />
                    <label htmlFor="rev-irreversible" className="text-sm text-ink">
                      <span className="font-medium">Irreversible</span>
                      <span className="block text-xs text-muted">
                        Tidak dapat di-rollback (mis. penghapusan data permanen); wajib isi alasan.
                      </span>
                    </label>
                  </div>
                </div>
              </fieldset>
            </div>

            {editor.execution_mode === 'batch' && (
              <fieldset className="space-y-3.5 rounded-lg border border-accent/30 bg-accent-soft/20 p-4">
                <legend className="px-1 text-xs font-semibold text-accent">
                  Konfigurasi Batch (Batch by Attribute)
                </legend>

                <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
                  <div className="space-y-1">
                    <label htmlFor="batch-schema" className="block text-xs font-medium text-ink">
                      Schema
                    </label>
                    <input
                      id="batch-schema"
                      name="batch_schema"
                      type="text"
                      disabled={formReadOnly}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      value={editor.batch_schema}
                      onChange={(e) => setEditor((s) => ({ ...s, batch_schema: e.target.value }))}
                      placeholder="public"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink disabled:bg-surface-subtle"
                    />
                  </div>
                  <div className="space-y-1">
                    <label htmlFor="batch-table" className="block text-xs font-medium text-ink">
                      Tabel Target <span aria-hidden="true" className="text-danger">*</span>
                    </label>
                    <input
                      ref={batchTableRef}
                      id="batch-table"
                      name="batch_table"
                      type="text"
                      required
                      disabled={formReadOnly}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      value={editor.batch_table}
                      onChange={(e) => setEditor((s) => ({ ...s, batch_table: e.target.value }))}
                      placeholder="orders"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink disabled:bg-surface-subtle"
                    />
                  </div>
                  <div className="space-y-1">
                    <label htmlFor="batch-cursor" className="block text-xs font-medium text-ink">
                      Kolom Cursor (Unik) <span aria-hidden="true" className="text-danger">*</span>
                    </label>
                    <input
                      id="batch-cursor"
                      name="batch_cursor_column"
                      type="text"
                      required
                      disabled={formReadOnly}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      value={editor.batch_cursor_column}
                      onChange={(e) =>
                        setEditor((s) => ({ ...s, batch_cursor_column: e.target.value }))
                      }
                      placeholder="id"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink disabled:bg-surface-subtle"
                    />
                  </div>
                  <div className="space-y-1">
                    <label htmlFor="batch-size" className="block text-xs font-medium text-ink">
                      Batch Size <span aria-hidden="true" className="text-danger">*</span>
                    </label>
                    <input
                      id="batch-size"
                      name="batch_size"
                      type="text"
                      inputMode="numeric"
                      pattern="[0-9]*"
                      required
                      disabled={formReadOnly}
                      value={editor.batch_size}
                      onChange={(e) =>
                        setEditor((s) => ({ ...s, batch_size: e.target.value.replace(/[^0-9]/g, '') }))
                      }
                      placeholder="1000"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink tabular-nums disabled:bg-surface-subtle"
                    />
                  </div>
                </div>

                <div className="grid gap-3 sm:grid-cols-3">
                  <div className="space-y-1">
                    <label htmlFor="batch-where" className="block text-xs font-medium text-ink">
                      WHERE Clause (Opsional)
                    </label>
                    <input
                      id="batch-where"
                      name="batch_where_clause"
                      type="text"
                      disabled={formReadOnly}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      value={editor.batch_where_clause}
                      onChange={(e) =>
                        setEditor((s) => ({ ...s, batch_where_clause: e.target.value }))
                      }
                      placeholder="status = 'pending'"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink disabled:bg-surface-subtle"
                    />
                  </div>
                  <div className="space-y-1">
                    <label htmlFor="batch-capture" className="block text-xs font-medium text-ink">
                      Capture Columns (pemulihan revert)
                    </label>
                    <input
                      id="batch-capture"
                      name="batch_capture_columns"
                      type="text"
                      disabled={formReadOnly}
                      autoCorrect="off"
                      autoCapitalize="none"
                      spellCheck={false}
                      value={editor.batch_capture_columns}
                      onChange={(e) =>
                        setEditor((s) => ({ ...s, batch_capture_columns: e.target.value }))
                      }
                      placeholder="status, updated_at"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink disabled:bg-surface-subtle"
                    />
                  </div>
                  <div className="space-y-1">
                    <label htmlFor="batch-delay" className="block text-xs font-medium text-ink">
                      Jeda Antar-Batch (ms)
                    </label>
                    <input
                      id="batch-delay"
                      name="batch_delay_ms"
                      type="text"
                      inputMode="numeric"
                      pattern="[0-9]*"
                      disabled={formReadOnly}
                      value={editor.batch_delay_ms}
                      onChange={(e) =>
                        setEditor((s) => ({
                          ...s,
                          batch_delay_ms: e.target.value.replace(/[^0-9]/g, ''),
                        }))
                      }
                      placeholder="0"
                      className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-xs text-ink tabular-nums disabled:bg-surface-subtle"
                    />
                  </div>
                </div>

                <div className="flex flex-wrap items-center justify-between gap-2 rounded-md border border-border bg-surface px-3 py-2 text-xs text-muted">
                  <span>
                    Placeholder wajib pada SQL batch:{' '}
                    <code className="font-mono text-ink">{'{{batch_ids}}'}</code>,{' '}
                    <code className="font-mono text-ink">{'{{cursor_start}}'}</code>,{' '}
                    <code className="font-mono text-ink">{'{{cursor_end}}'}</code>,{' '}
                    <code className="font-mono text-ink">{'{{last_cursor}}'}</code> (dan{' '}
                    <code className="font-mono text-ink">{'{{recovery_json}}'}</code> untuk down).
                  </span>
                  {!formReadOnly && (
                    <Button variant="secondary" size="sm" onClick={applyBatchSampleTemplate}>
                      <Sparkles className="h-3.5 w-3.5" aria-hidden="true" />
                      <span>Isi Contoh Batch SQL</span>
                    </Button>
                  )}
                </div>
              </fieldset>
            )}

            <div className="space-y-2">
              <label htmlFor="mig-sql-up" className="block text-sm font-medium text-ink">
                SQL Up <span aria-hidden="true" className="text-danger">*</span>
              </label>
              <textarea
                ref={sqlUpRef}
                id="mig-sql-up"
                name="sql_up"
                rows={7}
                required
                readOnly={formReadOnly}
                spellCheck={false}
                autoCorrect="off"
                autoCapitalize="none"
                value={editor.sql_up}
                onChange={(e) => {
                  setEditor((s) => ({ ...s, sql_up: e.target.value }));
                  if (formError) setFormError('');
                }}
                placeholder="CREATE TABLE public.audit_notes (id BIGSERIAL PRIMARY KEY, note TEXT NOT NULL);"
                className="w-full overflow-x-auto rounded-md border border-border bg-surface p-3 font-mono text-xs leading-relaxed text-ink read-only:bg-surface-subtle"
              />
              {editor.execution_mode === 'normal' && (
                <ExplainPanel
                  dbId={database.id}
                  sql={editor.sql_up}
                  canAnalyze={canEdit}
                  label="SQL Up"
                />
              )}
            </div>

            {editor.is_irreversible ? (
              <div className="space-y-1.5">
                <label htmlFor="mig-irrev-reason" className="block text-sm font-medium text-ink">
                  Alasan Irreversible <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <textarea
                  ref={irrevReasonRef}
                  id="mig-irrev-reason"
                  name="irreversible_reason"
                  rows={3}
                  required
                  readOnly={formReadOnly}
                  value={editor.irreversible_reason}
                  onChange={(e) => {
                    setEditor((s) => ({ ...s, irreversible_reason: e.target.value }));
                    if (formError) setFormError('');
                  }}
                  placeholder="Jelaskan mengapa perubahan ini tidak dapat di-rollback (mis. menghapus kolom berisi data historis permanen)."
                  className="w-full rounded-md border border-border bg-surface p-3 text-sm text-ink read-only:bg-surface-subtle"
                />
              </div>
            ) : (
              <div className="space-y-2">
                <label htmlFor="mig-sql-down" className="block text-sm font-medium text-ink">
                  SQL Down (Rollback) <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <textarea
                  ref={sqlDownRef}
                  id="mig-sql-down"
                  name="sql_down"
                  rows={5}
                  required
                  readOnly={formReadOnly}
                  spellCheck={false}
                  autoCorrect="off"
                  autoCapitalize="none"
                  value={editor.sql_down}
                  onChange={(e) => {
                    setEditor((s) => ({ ...s, sql_down: e.target.value }));
                    if (formError) setFormError('');
                  }}
                  placeholder="DROP TABLE IF EXISTS public.audit_notes;"
                  className="w-full overflow-x-auto rounded-md border border-border bg-surface p-3 font-mono text-xs leading-relaxed text-ink read-only:bg-surface-subtle"
                />
                {editor.execution_mode === 'normal' && (
                  <ExplainPanel
                    dbId={database.id}
                    sql={editor.sql_down}
                    canAnalyze={canEdit}
                    label="SQL Down"
                  />
                )}
              </div>
            )}

            {!formReadOnly && (
              <div className="flex flex-wrap items-center justify-end gap-2 border-t border-border pt-4">
                {mode === 'create' && definitions.length > 0 && (
                  <Button
                    variant="secondary"
                    disabled={saving}
                    onClick={() => {
                      setMode('inspect');
                      if (selectedDef) setEditor(definitionToEditor(selectedDef));
                    }}
                  >
                    Batal
                  </Button>
                )}
                <Button type="submit" variant="primary" loading={saving}>
                  <Save className="h-4 w-4" aria-hidden="true" />
                  <span>{mode === 'create' ? 'Simpan Definisi Baru' : 'Simpan Perubahan Draft'}</span>
                </Button>
              </div>
            )}

            {mode === 'inspect' && selectedDef && !canEdit && (
              <div className="flex items-center gap-2 rounded-md border border-border bg-surface-subtle/50 px-3 py-2 text-xs text-muted">
                <ShieldAlert className="h-4 w-4 shrink-0" aria-hidden="true" />
                <span>Kamu memiliki akses View Only pada database ini sehingga tidak dapat mengubah atau menjalankan migration.</span>
              </div>
            )}
          </form>

          {mode === 'inspect' && selectedDef && (
            <ApprovalPanel
              dbId={database.id}
              definition={selectedDef}
              canEdit={canEdit}
              onStatusUpdated={loadDefinitions}
            />
          )}
        </section>
      </div>

      <ConfirmDialog
        open={Boolean(pendingAction && confirmCopy)}
        title={confirmCopy?.title ?? ''}
        description={confirmCopy?.description ?? ''}
        confirmLabel={confirmCopy?.confirmLabel ?? 'Lanjutkan'}
        destructive={confirmCopy?.destructive ?? false}
        loading={enqueuing}
        onConfirm={() => void handleConfirmAsyncAction()}
        onCancel={() => setPendingAction(null)}
      />
    </div>
  );
}
