import { useCallback, useEffect, useId, useRef, useState, type FormEvent } from 'react';
import {
  CheckCircle2,
  Eye,
  EyeOff,
  Pencil,
  PlugZap,
  Plus,
  RefreshCw,
  Server,
  Trash2,
  XCircle,
} from 'lucide-react';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import * as adminService from '@/services/admin';
import type { ConnectionInput, ConnectionTestResult, DBConnection, SSLMode } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { EmptyState } from '@/components/ui/EmptyState';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

const SSL_MODES: SSLMode[] = ['disable', 'allow', 'prefer', 'require', 'verify-ca', 'verify-full'];

interface ConnFormState {
  name: string;
  description: string;
  host: string;
  port: string;
  database_name: string;
  username: string;
  password: string;
  ssl_mode: SSLMode;
  test_before_save: boolean;
}

const DEFAULT_FORM: ConnFormState = {
  name: '',
  description: '',
  host: 'localhost',
  port: '5433',
  database_name: '',
  username: '',
  password: '',
  ssl_mode: 'disable',
  test_before_save: true,
};

export function AdminConnectionsPage() {
  const { pushToast } = useToast();

  const [connections, setConnections] = useState<DBConnection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  // Form panel (inline so admin can edit + test without losing connection list context)
  const [panelOpen, setPanelOpen] = useState(false);
  const [editingConn, setEditingConn] = useState<DBConnection | null>(null);
  const [form, setForm] = useState<ConnFormState>(DEFAULT_FORM);
  const [showPassword, setShowPassword] = useState(false);
  const [formError, setFormError] = useState('');
  const [saving, setSaving] = useState(false);
  const [testingDraft, setTestingDraft] = useState(false);
  const [draftTestResult, setDraftTestResult] = useState<ConnectionTestResult | null>(null);

  // Per-row saved connection test states
  const [testingRowId, setTestingRowId] = useState<number | null>(null);
  const [rowTestResults, setRowTestResults] = useState<Record<number, ConnectionTestResult>>({});

  // Delete confirmation
  const [deleteTarget, setDeleteTarget] = useState<DBConnection | null>(null);
  const [deleting, setDeleting] = useState(false);

  const nameRef = useRef<HTMLInputElement>(null);
  const hostRef = useRef<HTMLInputElement>(null);
  const portRef = useRef<HTMLInputElement>(null);
  const dbNameRef = useRef<HTMLInputElement>(null);
  const userRef = useRef<HTMLInputElement>(null);
  const formHeadingId = useId();
  const tableCaptionId = useId();

  const loadConnections = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await adminService.listConnections();
      setConnections(data);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar koneksi database.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadConnections();
  }, [loadConnections]);

  const openCreateForm = () => {
    setEditingConn(null);
    setForm(DEFAULT_FORM);
    setShowPassword(false);
    setFormError('');
    setDraftTestResult(null);
    setPanelOpen(true);
  };

  const openEditForm = (conn: DBConnection) => {
    setEditingConn(conn);
    setForm({
      name: conn.name,
      description: conn.description,
      host: conn.host,
      port: String(conn.port),
      database_name: conn.database_name,
      username: conn.username,
      password: '',
      ssl_mode: conn.ssl_mode,
      test_before_save: true,
    });
    setShowPassword(false);
    setFormError('');
    setDraftTestResult(null);
    setPanelOpen(true);
  };

  const closeForm = () => {
    setPanelOpen(false);
    setEditingConn(null);
    setFormError('');
    setDraftTestResult(null);
  };

  const buildPayload = (requireName: boolean): ConnectionInput | null => {
    const trimmedName = form.name.trim();
    const trimmedHost = form.host.trim();
    const trimmedDb = form.database_name.trim();
    const trimmedUser = form.username.trim();
    const portNum = Number.parseInt(form.port.trim(), 10);

    if (requireName && !trimmedName) {
      setFormError('Nama koneksi wajib diisi.');
      nameRef.current?.focus();
      return null;
    }
    if (!trimmedHost) {
      setFormError('Host wajib diisi.');
      hostRef.current?.focus();
      return null;
    }
    if (!Number.isInteger(portNum) || portNum < 1 || portNum > 65535) {
      setFormError('Port harus berupa angka antara 1 dan 65535.');
      portRef.current?.focus();
      return null;
    }
    if (!trimmedDb) {
      setFormError('Nama database wajib diisi.');
      dbNameRef.current?.focus();
      return null;
    }
    if (!trimmedUser) {
      setFormError('Username database wajib diisi.');
      userRef.current?.focus();
      return null;
    }

    const payload: ConnectionInput = {
      name: trimmedName,
      description: form.description.trim(),
      host: trimmedHost,
      port: portNum,
      database_name: trimmedDb,
      username: trimmedUser,
      ssl_mode: form.ssl_mode,
      test_before_save: form.test_before_save,
    };

    if (form.password !== '' || !editingConn) {
      payload.password = form.password;
    }
    if (editingConn) {
      payload.connection_id = editingConn.id;
    }
    return payload;
  };

  const handleTestDraft = async () => {
    setFormError('');
    setDraftTestResult(null);
    const payload = buildPayload(false);
    if (!payload) return;

    setTestingDraft(true);
    try {
      const res = await adminService.testNewConnection(payload);
      setDraftTestResult(res);
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Gagal menguji koneksi.');
    } finally {
      setTestingDraft(false);
    }
  };

  const handleSave = async (e: FormEvent) => {
    e.preventDefault();
    setFormError('');
    const payload = buildPayload(true);
    if (!payload) return;

    setSaving(true);
    try {
      const { connection_id: _omit, ...savePayload } = payload;
      if (editingConn) {
        const updated = await adminService.updateConnection(editingConn.id, savePayload);
        pushToast('success', `Koneksi "${updated.name}" berhasil diperbarui.`);
      } else {
        const created = await adminService.createConnection(savePayload);
        pushToast('success', `Koneksi "${created.name}" berhasil disimpan.`);
      }
      closeForm();
      await loadConnections();
    } catch (err) {
      setFormError(err instanceof ApiError ? err.message : 'Gagal menyimpan koneksi database.');
    } finally {
      setSaving(false);
    }
  };

  const handleTestSaved = async (conn: DBConnection) => {
    setTestingRowId(conn.id);
    try {
      const res = await adminService.testSavedConnection(conn.id);
      setRowTestResults((prev) => ({ ...prev, [conn.id]: res }));
      if (res.ok) {
        pushToast('success', `${conn.name}: terhubung (${res.latency_ms ?? 0} ms).`);
      } else {
        pushToast('error', `${conn.name}: ${res.error || 'Tes koneksi gagal.'}`);
      }
    } catch (err) {
      const msg = err instanceof ApiError ? err.message : 'Gagal mengetes koneksi.';
      setRowTestResults((prev) => ({ ...prev, [conn.id]: { ok: false, error: msg } }));
      pushToast('error', `${conn.name}: ${msg}`);
    } finally {
      setTestingRowId(null);
    }
  };

  const handleConfirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      await adminService.deleteConnection(deleteTarget.id);
      pushToast('success', `Koneksi "${deleteTarget.name}" dan seluruh grant terkait berhasil dihapus.`);
      if (editingConn?.id === deleteTarget.id) {
        closeForm();
      }
      setDeleteTarget(null);
      await loadConnections();
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal menghapus koneksi.');
      setDeleteTarget(null);
    } finally {
      setDeleting(false);
    }
  };

  const targetChangedOnEdit =
    editingConn !== null &&
    (form.host.trim().toLowerCase() !== editingConn.host.toLowerCase() ||
      Number.parseInt(form.port.trim(), 10) !== editingConn.port ||
      form.database_name.trim() !== editingConn.database_name ||
      form.username.trim() !== editingConn.username ||
      form.ssl_mode !== editingConn.ssl_mode);

  return (
    <div>
      <PageHeader
        title="Koneksi Database"
        description="Kelola koneksi ke satu atau lebih database target PostgreSQL. Password koneksi dienkripsi di backend dan tidak pernah ditampilkan kembali."
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => void loadConnections()} loading={loading}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Muat Ulang</span>
            </Button>
            <Button
              variant="primary"
              size="sm"
              aria-expanded={panelOpen && !editingConn}
              onClick={panelOpen && !editingConn ? closeForm : openCreateForm}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{panelOpen && !editingConn ? 'Tutup Form' : 'Tambah Koneksi'}</span>
            </Button>
          </>
        }
      />

      {panelOpen && (
        <section className="card-surface mb-6 p-5" aria-labelledby={formHeadingId}>
          <h2 id={formHeadingId} className="text-base font-semibold text-ink">
            {editingConn ? `Ubah Koneksi: ${editingConn.name}` : 'Tambah Koneksi PostgreSQL'}
          </h2>
          <p className="mt-1 text-xs text-muted">
            Contoh lokal Docker Compose: host <code className="font-mono">localhost</code>, port{' '}
            <code className="font-mono">5433</code>, database <code className="font-mono">sample_db</code>, user{' '}
            <code className="font-mono">sample_app</code>.
          </p>

          {formError && (
            <Alert variant="error" className="mt-3">
              {formError}
            </Alert>
          )}

          {draftTestResult && (
            <Alert
              variant={draftTestResult.ok ? 'success' : 'error'}
              className="mt-3"
              title={draftTestResult.ok ? 'Tes koneksi berhasil' : 'Tes koneksi gagal'}
            >
              {draftTestResult.ok ? (
                <span>
                  Terhubung ke PostgreSQL{' '}
                  <span className="font-mono">{draftTestResult.server_version}</span> (
                  <span className="tabular-nums">{draftTestResult.latency_ms ?? 0} ms</span>).
                </span>
              ) : (
                <span>{draftTestResult.error}</span>
              )}
            </Alert>
          )}

          <form aria-labelledby={formHeadingId} onSubmit={handleSave} className="mt-4 space-y-4" noValidate>
            <div className="grid gap-4 sm:grid-cols-2">
              <div className="space-y-1">
                <label htmlFor="conn-name" className="block text-sm font-medium text-ink">
                  Nama Koneksi <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={nameRef}
                  id="conn-name"
                  name="name"
                  type="text"
                  maxLength={64}
                  required
                  value={form.name}
                  onChange={(e) => setForm((f) => ({ ...f, name: e.target.value }))}
                  placeholder="mis. Production Sample DB"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
                />
              </div>

              <div className="space-y-1">
                <label htmlFor="conn-desc" className="block text-sm font-medium text-ink">
                  Deskripsi
                </label>
                <input
                  id="conn-desc"
                  name="description"
                  type="text"
                  maxLength={500}
                  value={form.description}
                  onChange={(e) => setForm((f) => ({ ...f, description: e.target.value }))}
                  placeholder="Database layanan bersama"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
                />
              </div>
            </div>

            <div className="grid gap-4 sm:grid-cols-12">
              <div className="space-y-1 sm:col-span-6">
                <label htmlFor="conn-host" className="block text-sm font-medium text-ink">
                  Host <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={hostRef}
                  id="conn-host"
                  name="host"
                  type="text"
                  required
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  value={form.host}
                  onChange={(e) => setForm((f) => ({ ...f, host: e.target.value }))}
                  placeholder="localhost"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
                />
              </div>

              <div className="space-y-1 sm:col-span-2">
                <label htmlFor="conn-port" className="block text-sm font-medium text-ink">
                  Port <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={portRef}
                  id="conn-port"
                  name="port"
                  type="text"
                  inputMode="numeric"
                  pattern="[0-9]*"
                  required
                  value={form.port}
                  onChange={(e) => setForm((f) => ({ ...f, port: e.target.value.replace(/[^0-9]/g, '') }))}
                  placeholder="5432"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink tabular-nums"
                />
              </div>

              <div className="space-y-1 sm:col-span-4">
                <label htmlFor="conn-ssl" className="block text-sm font-medium text-ink">
                  SSL Mode
                </label>
                <select
                  id="conn-ssl"
                  name="ssl_mode"
                  value={form.ssl_mode}
                  onChange={(e) => setForm((f) => ({ ...f, ssl_mode: e.target.value as SSLMode }))}
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
                >
                  {SSL_MODES.map((mode) => (
                    <option key={mode} value={mode}>
                      {mode}
                    </option>
                  ))}
                </select>
              </div>
            </div>

            <div className="grid gap-4 sm:grid-cols-3">
              <div className="space-y-1">
                <label htmlFor="conn-dbname" className="block text-sm font-medium text-ink">
                  Nama Database <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={dbNameRef}
                  id="conn-dbname"
                  name="database_name"
                  type="text"
                  maxLength={63}
                  required
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  value={form.database_name}
                  onChange={(e) => setForm((f) => ({ ...f, database_name: e.target.value }))}
                  placeholder="sample_db"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
                />
              </div>

              <div className="space-y-1">
                <label htmlFor="conn-user" className="block text-sm font-medium text-ink">
                  Username DB <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={userRef}
                  id="conn-user"
                  name="username"
                  type="text"
                  maxLength={63}
                  required
                  autoCorrect="off"
                  autoCapitalize="none"
                  spellCheck={false}
                  autoComplete="off"
                  value={form.username}
                  onChange={(e) => setForm((f) => ({ ...f, username: e.target.value }))}
                  placeholder="sample_app"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
                />
              </div>

              <div className="space-y-1">
                <label htmlFor="conn-password" className="block text-sm font-medium text-ink">
                  Password DB {targetChangedOnEdit && <span aria-hidden="true" className="text-danger">*</span>}
                </label>
                <div className="relative">
                  <input
                    id="conn-password"
                    name="password"
                    type={showPassword ? 'text' : 'password'}
                    autoComplete="new-password"
                    aria-describedby="conn-password-hint"
                    value={form.password}
                    onChange={(e) => setForm((f) => ({ ...f, password: e.target.value }))}
                    placeholder={editingConn ? 'Kosongkan jika tidak diubah' : 'Password database'}
                    className="w-full rounded-md border border-border bg-surface py-2 pr-10 pl-3 text-sm text-ink"
                  />
                  <button
                    type="button"
                    aria-pressed={showPassword}
                    aria-label={showPassword ? 'Sembunyikan password' : 'Tampilkan password'}
                    onClick={() => setShowPassword((p) => !p)}
                    className="absolute top-1/2 right-2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-muted hover:bg-surface-subtle hover:text-ink"
                  >
                    {showPassword ? (
                      <EyeOff className="h-4 w-4" aria-hidden="true" />
                    ) : (
                      <Eye className="h-4 w-4" aria-hidden="true" />
                    )}
                  </button>
                </div>
                <p id="conn-password-hint" className="text-xs text-muted">
                  {editingConn
                    ? targetChangedOnEdit
                      ? 'Target koneksi berubah — password wajib diisi ulang.'
                      : 'Kosongkan untuk mempertahankan password tersimpan.'
                    : 'Dienkripsi menggunakan NaCl secretbox.'}
                </p>
              </div>
            </div>

            <div className="flex items-center gap-2">
              <input
                id="conn-test-before-save"
                name="test_before_save"
                type="checkbox"
                checked={form.test_before_save}
                onChange={(e) => setForm((f) => ({ ...f, test_before_save: e.target.checked }))}
                className="h-4 w-4 rounded border-border"
              />
              <label htmlFor="conn-test-before-save" className="text-sm text-ink">
                Uji koneksi terlebih dahulu sebelum menyimpan
              </label>
            </div>

            <div className="flex flex-wrap items-center justify-between gap-2 border-t border-border pt-4">
              <Button
                variant="secondary"
                loading={testingDraft}
                disabled={saving}
                onClick={() => void handleTestDraft()}
              >
                <PlugZap className="h-4 w-4" aria-hidden="true" />
                <span>Uji Koneksi</span>
              </Button>
              <div className="flex flex-wrap gap-2">
                <Button variant="secondary" disabled={saving} onClick={closeForm}>
                  Batal
                </Button>
                <Button type="submit" variant="primary" loading={saving}>
                  {editingConn ? 'Simpan Perubahan' : 'Simpan Koneksi'}
                </Button>
              </div>
            </div>
          </form>
        </section>
      )}

      {error && (
        <Alert variant="error" className="mb-5">
          {error}
        </Alert>
      )}

      {loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat daftar koneksi">
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-3/4" />
        </div>
      ) : connections.length === 0 ? (
        <EmptyState
          icon={Server}
          title="Belum ada koneksi database"
          description="Tambahkan koneksi PostgreSQL target agar group dapat diberikan akses untuk melihat schema, data, dan menjalankan migration."
          action={
            <Button variant="primary" size="sm" onClick={openCreateForm}>
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Tambah Koneksi Pertama</span>
            </Button>
          }
        />
      ) : (
        <TableShell ariaLabelledBy={tableCaptionId}>
          <table>
            <caption id={tableCaptionId} className="sr-only-live">
              Daftar koneksi PostgreSQL yang dikelola platform
            </caption>
            <thead>
              <tr>
                <th scope="col">Nama Koneksi</th>
                <th scope="col">Target</th>
                <th scope="col">User &amp; SSL</th>
                <th scope="col">Status Uji</th>
                <th scope="col" className="text-right">
                  Aksi
                </th>
              </tr>
            </thead>
            <tbody>
              {connections.map((c) => {
                const testRes = rowTestResults[c.id];
                return (
                  <tr key={c.id} className="hover:bg-surface-subtle/50">
                    <th scope="row" className="text-left">
                      <div className="font-semibold text-ink">{c.name}</div>
                      {c.description && (
                        <p className="mt-0.5 max-w-xs truncate text-xs font-normal text-muted">
                          {c.description}
                        </p>
                      )}
                    </th>
                    <td className="font-mono text-xs text-ink">
                      <div>
                        {c.host}:{c.port}
                      </div>
                      <div className="text-muted">/{c.database_name}</div>
                    </td>
                    <td className="text-xs">
                      <div className="font-mono text-ink">{c.username}</div>
                      <Badge tone="neutral" className="mt-1 font-mono text-[11px]">
                        ssl: {c.ssl_mode}
                      </Badge>
                    </td>
                    <td className="text-xs">
                      {!testRes ? (
                        <span className="text-muted">Belum diuji</span>
                      ) : testRes.ok ? (
                        <div className="inline-flex items-center gap-1.5 text-ok">
                          <CheckCircle2 className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                          <span className="tabular-nums">
                            OK · {testRes.latency_ms ?? 0} ms
                          </span>
                        </div>
                      ) : (
                        <div className="flex max-w-xs items-start gap-1.5 text-danger">
                          <XCircle className="mt-0.5 h-3.5 w-3.5 shrink-0" aria-hidden="true" />
                          <span className="line-clamp-2">{testRes.error}</span>
                        </div>
                      )}
                    </td>
                    <td>
                      <div className="flex items-center justify-end gap-1.5">
                        <Button
                          variant="secondary"
                          size="sm"
                          loading={testingRowId === c.id}
                          onClick={() => void handleTestSaved(c)}
                        >
                          <PlugZap className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Tes</span>
                        </Button>
                        <Button variant="secondary" size="sm" onClick={() => openEditForm(c)}>
                          <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Edit</span>
                        </Button>
                        <Button
                          variant="danger-ghost"
                          size="sm"
                          onClick={() => setDeleteTarget(c)}
                          title="Hapus koneksi"
                        >
                          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                          <span className="sr-only-live">Hapus {c.name}</span>
                        </Button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </TableShell>
      )}

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title={`Hapus koneksi "${deleteTarget?.name ?? ''}"?`}
        description="Koneksi ini akan dihapus dari platform beserta seluruh grant akses group terhadap database ini. Database PostgreSQL aslinya tidak akan dihapus."
        confirmLabel="Hapus Koneksi"
        destructive
        loading={deleting}
        onConfirm={() => void handleConfirmDelete()}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
}
