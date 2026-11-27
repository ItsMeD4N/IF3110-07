import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import {
  Eye,
  EyeOff,
  KeyRound,
  LogOut,
  Pencil,
  Plus,
  RefreshCw,
  Search,
  Trash2,
  UserPlus,
  Users,
} from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import { formatDateTime } from '@/lib/format';
import * as adminService from '@/services/admin';
import type { AuthUser } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { EmptyState } from '@/components/ui/EmptyState';
import { FormDialog } from '@/components/ui/FormDialog';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

export function AdminUsersPage() {
  const { user: currentUser, signOut } = useAuth();
  const { pushToast } = useToast();

  const [users, setUsers] = useState<AuthUser[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');

  const [showCreateForm, setShowCreateForm] = useState(false);
  const [newUsername, setNewUsername] = useState('');
  const [newPassword, setNewPassword] = useState('');
  const [showNewPassword, setShowNewPassword] = useState(false);
  const [newIsAdmin, setNewIsAdmin] = useState(false);
  const [createError, setCreateError] = useState('');
  const [creating, setCreating] = useState(false);
  const createUsernameRef = useRef<HTMLInputElement>(null);
  const createPasswordRef = useRef<HTMLInputElement>(null);

  const [editingUser, setEditingUser] = useState<AuthUser | null>(null);
  const [editUsername, setEditUsername] = useState('');
  const [editIsAdmin, setEditIsAdmin] = useState(false);
  const [editIsActive, setEditIsActive] = useState(true);
  const [editError, setEditError] = useState('');
  const [savingEdit, setSavingEdit] = useState(false);
  const editUsernameRef = useRef<HTMLInputElement>(null);

  const [resetTarget, setResetTarget] = useState<AuthUser | null>(null);
  const [resetPassword, setResetPassword] = useState('');
  const [showResetPassword, setShowResetPassword] = useState(false);
  const [resetError, setResetError] = useState('');
  const [resetting, setResetting] = useState(false);
  const resetInputRef = useRef<HTMLInputElement>(null);

  const [revokeTarget, setRevokeTarget] = useState<AuthUser | null>(null);
  const [revoking, setRevoking] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<AuthUser | null>(null);
  const [deleting, setDeleting] = useState(false);

  const createHeadingId = useId();
  const tableCaptionId = useId();

  const loadUsers = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await adminService.listUsers();
      setUsers(data);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar pengguna.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadUsers();
  }, [loadUsers]);

  const filteredUsers = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return users;
    return users.filter((u) => u.username.toLowerCase().includes(q));
  }, [query, users]);

  const handleCreateUser = async (e: FormEvent) => {
    e.preventDefault();
    const trimmed = newUsername.trim();
    if (trimmed.length < 3) {
      setCreateError('Username minimal terdiri dari 3 karakter.');
      createUsernameRef.current?.focus();
      return;
    }
    if (newPassword.length < 8) {
      setCreateError('Password minimal terdiri dari 8 karakter.');
      createPasswordRef.current?.focus();
      return;
    }

    setCreateError('');
    setCreating(true);
    try {
      const res = await adminService.createUser({
        username: trimmed,
        password: newPassword,
        is_platform_admin: newIsAdmin,
      });
      pushToast('success', `Pengguna "${res.user.username}" berhasil dibuat.`);
      setNewUsername('');
      setNewPassword('');
      setNewIsAdmin(false);
      setShowCreateForm(false);
      await loadUsers();
    } catch (err) {
      setCreateError(err instanceof ApiError ? err.message : 'Gagal membuat pengguna baru.');
    } finally {
      setCreating(false);
    }
  };

  const openEditDialog = (u: AuthUser) => {
    setEditingUser(u);
    setEditUsername(u.username);
    setEditIsAdmin(u.is_platform_admin);
    setEditIsActive(u.is_active ?? true);
    setEditError('');
  };

  const handleSaveEdit = async (e: FormEvent) => {
    e.preventDefault();
    if (!editingUser) return;
    const trimmed = editUsername.trim();
    const isSelf = currentUser?.id === editingUser.id;

    if (trimmed.length < 3) {
      setEditError('Username minimal terdiri dari 3 karakter.');
      editUsernameRef.current?.focus();
      return;
    }
    if (isSelf && (!editIsActive || !editIsAdmin)) {
      setEditError('Kamu tidak dapat menonaktifkan akun sendiri atau mencabut status admin milikmu sendiri.');
      return;
    }

    setEditError('');
    setSavingEdit(true);
    try {
      await adminService.updateUser(editingUser.id, {
        username: trimmed,
        is_platform_admin: editIsAdmin,
        is_active: editIsActive,
      });
      pushToast('success', `Data pengguna "${trimmed}" berhasil diperbarui.`);
      setEditingUser(null);
      await loadUsers();
    } catch (err) {
      setEditError(err instanceof ApiError ? err.message : 'Gagal memperbarui pengguna.');
    } finally {
      setSavingEdit(false);
    }
  };

  const openResetDialog = (u: AuthUser) => {
    setResetTarget(u);
    setResetPassword('');
    setShowResetPassword(false);
    setResetError('');
  };

  const handleResetPassword = async (e: FormEvent) => {
    e.preventDefault();
    if (!resetTarget) return;
    if (resetPassword.length < 8) {
      setResetError('Password baru minimal terdiri dari 8 karakter.');
      resetInputRef.current?.focus();
      return;
    }

    setResetError('');
    setResetting(true);
    try {
      const res = await adminService.resetUserPassword(resetTarget.id, resetPassword);
      pushToast('success', res.message || `Password "${resetTarget.username}" berhasil direset.`);
      const wasSelf = currentUser?.id === resetTarget.id;
      setResetTarget(null);
      setResetPassword('');
      if (wasSelf) {
        await signOut();
      }
    } catch (err) {
      setResetError(err instanceof ApiError ? err.message : 'Gagal mereset password pengguna.');
    } finally {
      setResetting(false);
    }
  };

  const handleConfirmRevoke = async () => {
    if (!revokeTarget) return;
    setRevoking(true);
    try {
      const res = await adminService.revokeUserSessions(revokeTarget.id);
      pushToast('success', res.message || `Semua sesi "${revokeTarget.username}" telah dicabut.`);
      const wasSelf = currentUser?.id === revokeTarget.id;
      setRevokeTarget(null);
      if (wasSelf) {
        await signOut();
      }
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal mencabut sesi pengguna.');
      setRevokeTarget(null);
    } finally {
      setRevoking(false);
    }
  };

  const handleConfirmDelete = async () => {
    if (!deleteTarget) return;
    setDeleting(true);
    try {
      const res = await adminService.deleteUser(deleteTarget.id);
      pushToast('success', res.message || `Pengguna "${deleteTarget.username}" dihapus.`);
      setDeleteTarget(null);
      await loadUsers();
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal menghapus pengguna.');
      setDeleteTarget(null);
    } finally {
      setDeleting(false);
    }
  };

  const editingIsSelf = editingUser ? currentUser?.id === editingUser.id : false;

  return (
    <div>
      <PageHeader
        title="Manajemen Pengguna"
        description="Kelola akun pengguna platform, peran Platform Admin, reset password, dan pencabutan sesi aktif. Akses database tetap diberikan melalui Group & Akses."
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => void loadUsers()} loading={loading}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Muat Ulang</span>
            </Button>
            <Button
              variant="primary"
              size="sm"
              aria-expanded={showCreateForm}
              onClick={() => {
                setShowCreateForm((prev) => !prev);
                setCreateError('');
              }}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{showCreateForm ? 'Tutup Form' : 'Tambah Pengguna'}</span>
            </Button>
          </>
        }
      />

      {showCreateForm && (
        <section className="card-surface mb-6 p-5" aria-labelledby={createHeadingId}>
          <h2 id={createHeadingId} className="text-base font-semibold text-ink">
            Tambah Pengguna Baru
          </h2>
          <p className="mt-1 text-xs text-muted">
            Password disimpan dengan bcrypt. Setelah dibuat, masukkan pengguna ke group untuk memberi akses database.
          </p>

          {createError && (
            <Alert variant="error" className="mt-3">
              {createError}
            </Alert>
          )}

          <form
            aria-labelledby={createHeadingId}
            onSubmit={handleCreateUser}
            className="mt-4 grid grid-cols-1 gap-4 sm:grid-cols-2"
            noValidate
          >
            <div className="space-y-1">
              <label htmlFor="create-username" className="block text-sm font-medium text-ink">
                Username <span aria-hidden="true" className="text-danger">*</span>
              </label>
              <input
                ref={createUsernameRef}
                id="create-username"
                name="username"
                type="text"
                minLength={3}
                required
                autoCorrect="off"
                autoCapitalize="none"
                spellCheck={false}
                autoComplete="off"
                aria-invalid={createError.toLowerCase().includes('username')}
                aria-describedby="create-username-hint"
                value={newUsername}
                onChange={(e) => {
                  setNewUsername(e.target.value);
                  if (createError) setCreateError('');
                }}
                placeholder="mis. alice"
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
              />
              <p id="create-username-hint" className="text-xs text-muted">
                Minimal 3 karakter, unik di seluruh platform.
              </p>
            </div>

            <div className="space-y-1">
              <label htmlFor="create-password" className="block text-sm font-medium text-ink">
                Password <span aria-hidden="true" className="text-danger">*</span>
              </label>
              <div className="relative">
                <input
                  ref={createPasswordRef}
                  id="create-password"
                  name="password"
                  type={showNewPassword ? 'text' : 'password'}
                  minLength={8}
                  required
                  autoComplete="new-password"
                  aria-invalid={createError.toLowerCase().includes('password')}
                  aria-describedby="create-password-hint"
                  value={newPassword}
                  onChange={(e) => {
                    setNewPassword(e.target.value);
                    if (createError) setCreateError('');
                  }}
                  placeholder="Minimal 8 karakter"
                  className="w-full rounded-md border border-border bg-surface py-2 pr-10 pl-3 text-sm text-ink"
                />
                <button
                  type="button"
                  aria-pressed={showNewPassword}
                  aria-label={showNewPassword ? 'Sembunyikan password' : 'Tampilkan password'}
                  onClick={() => setShowNewPassword((p) => !p)}
                  className="absolute top-1/2 right-2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-muted hover:bg-surface-subtle hover:text-ink"
                >
                  {showNewPassword ? (
                    <EyeOff className="h-4 w-4" aria-hidden="true" />
                  ) : (
                    <Eye className="h-4 w-4" aria-hidden="true" />
                  )}
                </button>
              </div>
              <p id="create-password-hint" className="text-xs text-muted">
                Minimal 8 karakter. Tidak ditampilkan lagi setelah disimpan.
              </p>
            </div>

            <div className="flex items-center gap-2 sm:col-span-2">
              <input
                id="create-is-admin"
                name="is_platform_admin"
                type="checkbox"
                checked={newIsAdmin}
                onChange={(e) => setNewIsAdmin(e.target.checked)}
                className="h-4 w-4 rounded border-border"
              />
              <label htmlFor="create-is-admin" className="text-sm text-ink">
                Tetapkan sebagai Platform Admin (dapat mengelola user, group, dan koneksi)
              </label>
            </div>

            <div className="flex flex-wrap justify-end gap-2 sm:col-span-2">
              <Button variant="secondary" onClick={() => setShowCreateForm(false)} disabled={creating}>
                Batal
              </Button>
              <Button type="submit" variant="primary" loading={creating}>
                <UserPlus className="h-4 w-4" aria-hidden="true" />
                <span>Simpan Pengguna</span>
              </Button>
            </div>
          </form>
        </section>
      )}

      {error && (
        <Alert variant="error" className="mb-5">
          {error}
        </Alert>
      )}

      <div className="mb-4 flex flex-col gap-3 sm:flex-row sm:items-center sm:justify-between">
        <search className="relative w-full sm:max-w-xs">
          <label htmlFor="search-users" className="sr-only-live">
            Cari pengguna berdasarkan username
          </label>
          <Search
            className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted"
            aria-hidden="true"
          />
          <input
            id="search-users"
            name="q"
            type="search"
            autoCorrect="off"
            autoCapitalize="none"
            spellCheck={false}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Cari username…"
            className="w-full rounded-md border border-border bg-surface py-2 pr-3 pl-9 text-sm text-ink"
          />
        </search>
        <p className="text-xs text-muted tabular-nums">
          Menampilkan <span className="font-semibold text-ink">{filteredUsers.length}</span> dari{' '}
          <span className="font-semibold text-ink">{users.length}</span> pengguna
        </p>
      </div>

      {loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat daftar pengguna">
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-4/5" />
        </div>
      ) : filteredUsers.length === 0 ? (
        <EmptyState
          icon={Users}
          title={query ? 'Pengguna tidak ditemukan' : 'Belum ada pengguna'}
          description={
            query
              ? `Tidak ada username yang cocok dengan "${query}".`
              : 'Tambahkan akun pengguna pertama agar dapat dimasukkan ke group dan mengakses database.'
          }
        />
      ) : (
        <TableShell ariaLabelledBy={tableCaptionId}>
          <table>
            <caption id={tableCaptionId} className="sr-only-live">
              Daftar pengguna platform
            </caption>
            <thead>
              <tr>
                <th scope="col">Username</th>
                <th scope="col">Peran</th>
                <th scope="col">Status</th>
                <th scope="col">Dibuat</th>
                <th scope="col" className="text-right">
                  Aksi
                </th>
              </tr>
            </thead>
            <tbody>
              {filteredUsers.map((u) => {
                const isSelf = currentUser?.id === u.id;
                const active = u.is_active ?? true;
                return (
                  <tr key={u.id} className="hover:bg-surface-subtle/50">
                    <th scope="row" className="text-left font-medium text-ink">
                      <div className="flex items-center gap-2">
                        <span className="font-mono text-sm">{u.username}</span>
                        {isSelf && <Badge tone="accent">Kamu</Badge>}
                      </div>
                    </th>
                    <td>
                      {u.is_platform_admin ? (
                        <Badge tone="accent">Platform Admin</Badge>
                      ) : (
                        <Badge tone="neutral">Member</Badge>
                      )}
                    </td>
                    <td>
                      {active ? (
                        <Badge tone="ok">Aktif</Badge>
                      ) : (
                        <Badge tone="danger">Nonaktif</Badge>
                      )}
                    </td>
                    <td className="text-xs text-muted tabular-nums">
                      {formatDateTime(u.created_at)}
                    </td>
                    <td>
                      <div className="flex items-center justify-end gap-1.5">
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => openEditDialog(u)}
                          title="Edit pengguna"
                        >
                          <Pencil className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Edit</span>
                        </Button>
                        <Button
                          variant="secondary"
                          size="sm"
                          onClick={() => openResetDialog(u)}
                          title="Reset password"
                        >
                          <KeyRound className="h-3.5 w-3.5" aria-hidden="true" />
                          <span>Reset PW</span>
                        </Button>
                        <Button
                          variant="ghost"
                          size="sm"
                          onClick={() => setRevokeTarget(u)}
                          title="Cabut semua sesi aktif"
                        >
                          <LogOut className="h-3.5 w-3.5" aria-hidden="true" />
                          <span className="hidden sm:inline">Cabut Sesi</span>
                        </Button>
                        <Button
                          variant="danger-ghost"
                          size="sm"
                          disabled={isSelf}
                          onClick={() => setDeleteTarget(u)}
                          title={isSelf ? 'Tidak bisa menghapus akun sendiri' : 'Hapus pengguna'}
                        >
                          <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
                          <span className="sr-only-live">Hapus {u.username}</span>
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

      <FormDialog
        open={Boolean(editingUser)}
        title={`Edit Pengguna: ${editingUser?.username ?? ''}`}
        description="Menonaktifkan pengguna atau mengubah status admin otomatis mencabut semua sesi aktif pengguna tersebut."
        busy={savingEdit}
        onClose={() => setEditingUser(null)}
      >
        {editError && (
          <Alert variant="error" className="mb-4">
            {editError}
          </Alert>
        )}
        <form onSubmit={handleSaveEdit} className="space-y-4" noValidate>
          <div className="space-y-1">
            <label htmlFor="edit-username" className="block text-sm font-medium text-ink">
              Username <span aria-hidden="true" className="text-danger">*</span>
            </label>
            <input
              ref={editUsernameRef}
              id="edit-username"
              name="username"
              type="text"
              minLength={3}
              required
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              value={editUsername}
              onChange={(e) => {
                setEditUsername(e.target.value);
                if (editError) setEditError('');
              }}
              className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
            />
          </div>

          <fieldset className="min-w-0 space-y-2.5 rounded-lg border border-border bg-surface-subtle/50 p-3.5">
            <legend className="px-1 text-xs font-semibold text-muted">
              Peran &amp; Status Akun
            </legend>
            <div className="flex items-center gap-2">
              <input
                id="edit-is-admin"
                name="is_platform_admin"
                type="checkbox"
                checked={editIsAdmin}
                disabled={editingIsSelf}
                onChange={(e) => setEditIsAdmin(e.target.checked)}
                className="h-4 w-4 rounded border-border"
              />
              <label htmlFor="edit-is-admin" className="text-sm text-ink">
                Platform Admin {editingIsSelf && <span className="text-xs text-muted">(tidak dapat dicabut sendiri)</span>}
              </label>
            </div>
            <div className="flex items-center gap-2">
              <input
                id="edit-is-active"
                name="is_active"
                type="checkbox"
                checked={editIsActive}
                disabled={editingIsSelf}
                onChange={(e) => setEditIsActive(e.target.checked)}
                className="h-4 w-4 rounded border-border"
              />
              <label htmlFor="edit-is-active" className="text-sm text-ink">
                Akun aktif {editingIsSelf && <span className="text-xs text-muted">(tidak dapat dinonaktifkan sendiri)</span>}
              </label>
            </div>
          </fieldset>

          <div className="flex flex-wrap justify-end gap-2 pt-1">
            <Button variant="secondary" disabled={savingEdit} onClick={() => setEditingUser(null)}>
              Batal
            </Button>
            <Button type="submit" variant="primary" loading={savingEdit}>
              Simpan Perubahan
            </Button>
          </div>
        </form>
      </FormDialog>

      <FormDialog
        open={Boolean(resetTarget)}
        title={`Reset Password: ${resetTarget?.username ?? ''}`}
        description="Setelah password direset, seluruh sesi aktif milik pengguna ini akan langsung dicabut."
        busy={resetting}
        onClose={() => setResetTarget(null)}
      >
        {resetError && (
          <Alert variant="error" className="mb-4">
            {resetError}
          </Alert>
        )}
        <form onSubmit={handleResetPassword} className="space-y-4" noValidate>
          <div className="space-y-1">
            <label htmlFor="reset-new-password" className="block text-sm font-medium text-ink">
              Password Baru <span aria-hidden="true" className="text-danger">*</span>
            </label>
            <div className="relative">
              <input
                ref={resetInputRef}
                id="reset-new-password"
                name="new_password"
                type={showResetPassword ? 'text' : 'password'}
                minLength={8}
                required
                autoComplete="new-password"
                aria-describedby="reset-pw-hint"
                value={resetPassword}
                onChange={(e) => {
                  setResetPassword(e.target.value);
                  if (resetError) setResetError('');
                }}
                placeholder="Minimal 8 karakter"
                className="w-full rounded-md border border-border bg-surface py-2 pr-10 pl-3 text-sm text-ink"
              />
              <button
                type="button"
                aria-pressed={showResetPassword}
                aria-label={showResetPassword ? 'Sembunyikan password' : 'Tampilkan password'}
                onClick={() => setShowResetPassword((p) => !p)}
                className="absolute top-1/2 right-2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-muted hover:bg-surface-subtle hover:text-ink"
              >
                {showResetPassword ? (
                  <EyeOff className="h-4 w-4" aria-hidden="true" />
                ) : (
                  <Eye className="h-4 w-4" aria-hidden="true" />
                )}
              </button>
            </div>
            <p id="reset-pw-hint" className="text-xs text-muted">
              Minimal 8 karakter. Bagikan password baru secara aman kepada pengguna.
            </p>
          </div>
          <div className="flex flex-wrap justify-end gap-2 pt-1">
            <Button variant="secondary" disabled={resetting} onClick={() => setResetTarget(null)}>
              Batal
            </Button>
            <Button type="submit" variant="primary" loading={resetting}>
              Reset Password &amp; Cabut Sesi
            </Button>
          </div>
        </form>
      </FormDialog>

      <ConfirmDialog
        open={Boolean(revokeTarget)}
        title="Cabut semua sesi pengguna?"
        description={`Seluruh sesi login aktif milik "${revokeTarget?.username ?? ''}" akan langsung dinonaktifkan dan pengguna harus login kembali.`}
        confirmLabel="Cabut Semua Sesi"
        destructive
        loading={revoking}
        onConfirm={() => void handleConfirmRevoke()}
        onCancel={() => setRevokeTarget(null)}
      />

      <ConfirmDialog
        open={Boolean(deleteTarget)}
        title="Hapus pengguna secara permanen?"
        description={`Akun "${deleteTarget?.username ?? ''}" beserta seluruh sesi dan keanggotaan group-nya akan dihapus. Tindakan ini tidak dapat dibatalkan.`}
        confirmLabel="Hapus Pengguna"
        destructive
        loading={deleting}
        onConfirm={() => void handleConfirmDelete()}
        onCancel={() => setDeleteTarget(null)}
      />
    </div>
  );
}
