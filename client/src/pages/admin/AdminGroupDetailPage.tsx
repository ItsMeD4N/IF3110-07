import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate, useParams } from 'react-router-dom';
import {
  ArrowLeft,
  Crown,
  Database,
  Plus,
  RefreshCw,
  Save,
  Trash2,
  UserMinus,
  UserPlus,
} from 'lucide-react';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import * as adminService from '@/services/admin';
import type { AccessLevel, AuthUser, DBConnection, GroupDetail } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { AccessBadge, Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { ConfirmDialog } from '@/components/ui/ConfirmDialog';
import { SkeletonLine } from '@/components/ui/Skeleton';

export function AdminGroupDetailPage() {
  const { groupId } = useParams();
  const parsedGroupId = Number(groupId);
  const navigate = useNavigate();
  const { pushToast } = useToast();

  const [group, setGroup] = useState<GroupDetail | null>(null);
  const [allUsers, setAllUsers] = useState<AuthUser[]>([]);
  const [allConnections, setAllConnections] = useState<DBConnection[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');

  const [editName, setEditName] = useState('');
  const [editDesc, setEditDesc] = useState('');
  const [savingInfo, setSavingInfo] = useState(false);
  const [infoError, setInfoError] = useState('');
  const infoNameRef = useRef<HTMLInputElement>(null);

  const [selectedLeaderId, setSelectedLeaderId] = useState('');
  const [savingLeader, setSavingLeader] = useState(false);
  const [leaderNote, setLeaderNote] = useState('');

  const [selectedUserId, setSelectedUserId] = useState('');
  const [addingMember, setAddingMember] = useState(false);

  const [selectedConnId, setSelectedConnId] = useState('');
  const [selectedLevel, setSelectedLevel] = useState<Exclude<AccessLevel, 'none'>>('view_only');
  const [savingGrant, setSavingGrant] = useState(false);

  const [grantLevels, setGrantLevels] = useState<Record<number, Exclude<AccessLevel, 'none'>>>({});
  const [updatingConnId, setUpdatingConnId] = useState<number | null>(null);

  const [confirmDeleteGroup, setConfirmDeleteGroup] = useState(false);
  const [deletingGroup, setDeletingGroup] = useState(false);
  const [removingMember, setRemovingMember] = useState<{ id: string; username: string } | null>(null);
  const [removingGrant, setRemovingGrant] = useState<{ connId: number; name: string } | null>(null);
  const [busyConfirm, setBusyConfirm] = useState(false);

  const infoFormId = useId();
  const leaderFormId = useId();
  const memberFormId = useId();
  const grantFormId = useId();

  const loadAll = useCallback(async () => {
    if (!Number.isInteger(parsedGroupId) || parsedGroupId <= 0) {
      setError('ID group tidak valid.');
      setLoading(false);
      return;
    }
    setLoading(true);
    setError('');
    try {
      const [detail, users, conns] = await Promise.all([
        adminService.getGroup(parsedGroupId),
        adminService.listUsers(),
        adminService.listConnections(),
      ]);
      setGroup(detail);
      setAllUsers(users);
      setAllConnections(conns);
      setEditName(detail.name);
      setEditDesc(detail.description);
      setSelectedLeaderId(detail.leader_user_id ?? '');

      const map: Record<number, Exclude<AccessLevel, 'none'>> = {};
      detail.grants.forEach((gr) => {
        if (gr.access_level === 'view_only' || gr.access_level === 'view_edit') {
          map[gr.connection_id] = gr.access_level;
        }
      });
      setGrantLevels(map);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Gagal memuat detail group.');
    } finally {
      setLoading(false);
    }
  }, [parsedGroupId]);

  useEffect(() => {
    void loadAll();
  }, [loadAll]);

  const candidateUsers = useMemo(() => {
    if (!group) return [];
    const existing = new Set(group.members.map((m) => m.user_id));
    return allUsers
      .filter((u) => !existing.has(u.id) && (u.is_active ?? true))
      .sort((a, b) => a.username.localeCompare(b.username));
  }, [allUsers, group]);

  const activeMembers = useMemo(() => {
    if (!group) return [];
    return group.members.filter((m) => m.is_active);
  }, [group]);

  const candidateConnections = useMemo(() => {
    if (!group) return [];
    const granted = new Set(group.grants.map((g) => g.connection_id));
    return allConnections
      .filter((c) => !granted.has(c.id))
      .sort((a, b) => a.name.localeCompare(b.name));
  }, [allConnections, group]);

  const handleSaveInfo = async (e: FormEvent) => {
    e.preventDefault();
    if (!group) return;
    const trimmed = editName.trim();
    if (!trimmed) {
      setInfoError('Nama group wajib diisi.');
      infoNameRef.current?.focus();
      return;
    }
    setInfoError('');
    setSavingInfo(true);
    try {
      const updated = await adminService.updateGroup(group.id, {
        name: trimmed,
        description: editDesc.trim(),
      });
      setGroup((prev) => (prev ? { ...prev, ...updated } : prev));
      pushToast('success', 'Informasi group berhasil diperbarui.');
    } catch (err) {
      setInfoError(err instanceof ApiError ? err.message : 'Gagal memperbarui group.');
    } finally {
      setSavingInfo(false);
    }
  };

  const handleSaveLeader = async (e: FormEvent) => {
    e.preventDefault();
    if (!group || !selectedLeaderId) return;
    setSavingLeader(true);
    setLeaderNote('');
    try {
      const updated = await adminService.setGroupLeader(group.id, selectedLeaderId);
      setGroup((prev) => (prev ? { ...prev, ...updated } : prev));
      pushToast('success', `Leader group "${group.name}" berhasil diperbarui.`);
    } catch (err) {
      if (err instanceof ApiError && err.status === 404) {
        setLeaderNote(
          'Endpoint PUT /api/admin/groups/:groupId/leader belum aktif di backend (lihat api_contract.md §2.1). UI siap otomatis bekerja setelah backend di-merge.',
        );
      } else {
        setLeaderNote(err instanceof ApiError ? err.message : 'Gagal menetapkan leader group.');
      }
    } finally {
      setSavingLeader(false);
    }
  };

  const handleAddMember = async (e: FormEvent) => {
    e.preventDefault();
    if (!group || !selectedUserId) return;
    setAddingMember(true);
    try {
      const members = await adminService.addGroupMember(group.id, selectedUserId);
      setGroup((prev) => (prev ? { ...prev, members, member_count: members.length } : prev));
      setSelectedUserId('');
      pushToast('success', 'Anggota ditambahkan ke group. Sesi user terkait akan dirotasi pada request berikutnya.');
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal menambahkan anggota.');
    } finally {
      setAddingMember(false);
    }
  };

  const handleConfirmRemoveMember = async () => {
    if (!group || !removingMember) return;
    setBusyConfirm(true);
    try {
      await adminService.removeGroupMember(group.id, removingMember.id);
      pushToast('success', `"${removingMember.username}" dikeluarkan dari group.`);
      setRemovingMember(null);
      await loadAll();
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal mengeluarkan anggota.');
      setRemovingMember(null);
    } finally {
      setBusyConfirm(false);
    }
  };

  const handleAddGrant = async (e: FormEvent) => {
    e.preventDefault();
    if (!group || !selectedConnId) return;
    const connId = Number(selectedConnId);
    if (!connId) return;
    setSavingGrant(true);
    try {
      const grants = await adminService.setGroupGrant(group.id, connId, selectedLevel);
      setGroup((prev) => (prev ? { ...prev, grants, grant_count: grants.length } : prev));
      setGrantLevels((prev) => ({ ...prev, [connId]: selectedLevel }));
      setSelectedConnId('');
      pushToast('success', 'Akses database berhasil diberikan.');
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal memberikan akses database.');
    } finally {
      setSavingGrant(false);
    }
  };

  const handleUpdateGrant = async (e: FormEvent, connId: number) => {
    e.preventDefault();
    if (!group) return;
    const level = grantLevels[connId];
    if (!level) return;
    setUpdatingConnId(connId);
    try {
      const grants = await adminService.setGroupGrant(group.id, connId, level);
      setGroup((prev) => (prev ? { ...prev, grants, grant_count: grants.length } : prev));
      pushToast('success', 'Tingkat akses database diperbarui.');
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal memperbarui tingkat akses.');
    } finally {
      setUpdatingConnId(null);
    }
  };

  const handleConfirmRemoveGrant = async () => {
    if (!group || !removingGrant) return;
    setBusyConfirm(true);
    try {
      await adminService.deleteGroupGrant(group.id, removingGrant.connId);
      pushToast('success', `Akses ke "${removingGrant.name}" dicabut.`);
      setRemovingGrant(null);
      await loadAll();
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal mencabut akses database.');
      setRemovingGrant(null);
    } finally {
      setBusyConfirm(false);
    }
  };

  const handleConfirmDeleteGroup = async () => {
    if (!group) return;
    setDeletingGroup(true);
    try {
      await adminService.deleteGroup(group.id);
      pushToast('success', `Group "${group.name}" berhasil dihapus.`);
      navigate('/admin/groups', { replace: true });
    } catch (err) {
      pushToast('error', err instanceof ApiError ? err.message : 'Gagal menghapus group.');
      setDeletingGroup(false);
      setConfirmDeleteGroup(false);
    }
  };

  if (loading) {
    return (
      <div className="space-y-4" role="status" aria-label="Memuat detail group">
        <SkeletonLine className="h-7 w-56" />
        <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
          <div className="card-surface min-w-0 space-y-3 p-5">
            <SkeletonLine className="h-5 w-40" />
            <SkeletonLine className="h-9 w-full" />
            <SkeletonLine className="h-9 w-full" />
          </div>
          <div className="card-surface space-y-3 p-5">
            <SkeletonLine className="h-5 w-40" />
            <SkeletonLine className="h-9 w-full" />
            <SkeletonLine className="h-9 w-full" />
          </div>
        </div>
      </div>
    );
  }

  if (error || !group) {
    return (
      <div className="space-y-4">
        <Alert variant="error">{error || 'Group tidak ditemukan.'}</Alert>
        <Link
          to="/admin/groups"
          className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border bg-surface px-3.5 py-1.5 text-sm font-medium text-ink no-underline hover:bg-surface-subtle"
        >
          <ArrowLeft className="h-4 w-4" aria-hidden="true" />
          <span>Kembali ke Daftar Group</span>
        </Link>
      </div>
    );
  }

  return (
    <div>
      <PageHeader
        title={group.name}
        description={group.description || 'Kelola anggota, penetapan leader untuk approval migration, serta hak akses database untuk group ini.'}
        breadcrumbs={[
          { label: 'Group & Akses', to: '/admin/groups' },
          { label: group.name },
        ]}
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => void loadAll()}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Muat Ulang</span>
            </Button>
            <Button variant="danger-ghost" size="sm" onClick={() => setConfirmDeleteGroup(true)}>
              <Trash2 className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Hapus Group</span>
            </Button>
          </>
        }
      />

      <div className="grid grid-cols-1 gap-5 lg:grid-cols-2">
        <section className="card-surface min-w-0 p-4 sm:p-5" aria-labelledby={infoFormId}>
          <h2 id={infoFormId} className="text-base font-semibold text-ink">
            Informasi Group
          </h2>
          {infoError && (
            <Alert variant="error" className="mt-3">
              {infoError}
            </Alert>
          )}
          <form aria-labelledby={infoFormId} onSubmit={handleSaveInfo} className="mt-4 space-y-3.5" noValidate>
            <div className="space-y-1">
              <label htmlFor="detail-group-name" className="block text-sm font-medium text-ink">
                Nama Group <span aria-hidden="true" className="text-danger">*</span>
              </label>
              <input
                ref={infoNameRef}
                id="detail-group-name"
                name="name"
                type="text"
                maxLength={64}
                required
                value={editName}
                onChange={(e) => {
                  setEditName(e.target.value);
                  if (infoError) setInfoError('');
                }}
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
              />
            </div>
            <div className="space-y-1">
              <label htmlFor="detail-group-desc" className="block text-sm font-medium text-ink">
                Deskripsi
              </label>
              <textarea
                id="detail-group-desc"
                name="description"
                rows={2}
                maxLength={500}
                value={editDesc}
                onChange={(e) => setEditDesc(e.target.value)}
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
              />
            </div>
            <div className="flex justify-end">
              <Button type="submit" variant="primary" size="sm" loading={savingInfo}>
                <Save className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Simpan Info</span>
              </Button>
            </div>
          </form>
        </section>

        <section className="card-surface min-w-0 p-4 sm:p-5" aria-labelledby={leaderFormId}>
          <div className="flex items-center justify-between gap-2">
            <h2 id={leaderFormId} className="inline-flex items-center gap-2 text-base font-semibold text-ink">
              <Crown className="h-4 w-4 text-accent" aria-hidden="true" />
              <span>Leader Group (Approval M2)</span>
            </h2>
            {group.leader_username ? (
              <Badge tone="accent">{group.leader_username}</Badge>
            ) : (
              <Badge tone="warn">Belum ditetapkan</Badge>
            )}
          </div>
          <p className="mt-1.5 text-xs leading-relaxed text-muted">
            Sesuai spesifikasi Milestone 2, setiap perubahan database (termasuk rollback dan forward) wajib disetujui oleh leader setiap group yang mengakses database tersebut, baik View Only maupun View + Edit.
          </p>

          {leaderNote && (
            <Alert variant="warning" className="mt-3">
              {leaderNote}
            </Alert>
          )}

          <form aria-labelledby={leaderFormId} onSubmit={handleSaveLeader} className="mt-4 space-y-3" noValidate>
            <div className="space-y-1">
              <label htmlFor="group-leader-select" className="block text-sm font-medium text-ink">
                Pilih Leader dari Anggota Aktif
              </label>
              <select
                id="group-leader-select"
                name="leader_user_id"
                value={selectedLeaderId}
                disabled={activeMembers.length === 0}
                onChange={(e) => setSelectedLeaderId(e.target.value)}
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink disabled:opacity-60"
              >
                <option value="">
                  {activeMembers.length === 0
                    ? 'Tambahkan anggota aktif terlebih dahulu'
                    : 'Pilih leader group'}
                </option>
                {activeMembers.map((m) => (
                  <option key={m.user_id} value={m.user_id}>
                    {m.username}
                  </option>
                ))}
              </select>
            </div>
            <div className="flex justify-end">
              <Button
                type="submit"
                variant="primary"
                size="sm"
                loading={savingLeader}
                disabled={!selectedLeaderId || activeMembers.length === 0}
              >
                <Crown className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Tetapkan Leader</span>
              </Button>
            </div>
          </form>
        </section>

        <section className="card-surface min-w-0 p-4 sm:p-5" aria-labelledby={memberFormId}>
          <div className="mb-3 flex items-center justify-between gap-2">
            <h2 id={memberFormId} className="text-base font-semibold text-ink">
              Anggota Group
            </h2>
            <Badge tone="neutral" className="tabular-nums">
              {group.members.length} anggota
            </Badge>
          </div>

          <form aria-labelledby={memberFormId} onSubmit={handleAddMember} className="mb-4 flex flex-wrap gap-2">
            <label htmlFor="add-member-select" className="sr-only-live">
              Pilih pengguna untuk ditambahkan ke group
            </label>
            <select
              id="add-member-select"
              name="user_id"
              value={selectedUserId}
              disabled={candidateUsers.length === 0}
              onChange={(e) => setSelectedUserId(e.target.value)}
              className="w-full min-w-0 rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-ink disabled:opacity-60 sm:w-auto sm:flex-1"
            >
              <option value="">
                {candidateUsers.length === 0
                  ? 'Semua user aktif sudah tergabung'
                  : 'Pilih pengguna aktif'}
              </option>
              {candidateUsers.map((u) => (
                <option key={u.id} value={u.id}>
                  {u.username}
                </option>
              ))}
            </select>
            <Button
              type="submit"
              variant="primary"
              size="sm"
              loading={addingMember}
              disabled={!selectedUserId}
            >
              <UserPlus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Tambah</span>
            </Button>
          </form>

          {group.members.length === 0 ? (
            <p className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-sm text-muted">
              Belum ada anggota di group ini.
            </p>
          ) : (
            <ul className="divide-y divide-border border-t border-border">
              {group.members.map((m) => {
                const isGroupLeader = group.leader_user_id === m.user_id;
                return (
                  <li key={m.user_id} className="flex items-center justify-between gap-2 py-2.5">
                    <div className="flex min-w-0 flex-wrap items-center gap-2">
                      <span className="truncate font-mono text-sm font-medium text-ink">
                        {m.username}
                      </span>
                      {isGroupLeader && (
                        <Badge tone="accent">
                          <Crown className="h-3 w-3" aria-hidden="true" />
                          <span>Leader</span>
                        </Badge>
                      )}
                      {!m.is_active && <Badge tone="danger">Nonaktif</Badge>}
                    </div>
                    <Button
                      variant="danger-ghost"
                      size="sm"
                      onClick={() => setRemovingMember({ id: m.user_id, username: m.username })}
                    >
                      <UserMinus className="h-3.5 w-3.5" aria-hidden="true" />
                      <span>Keluarkan</span>
                    </Button>
                  </li>
                );
              })}
            </ul>
          )}
        </section>

        <section className="card-surface min-w-0 p-4 sm:p-5" aria-labelledby={grantFormId}>
          <div className="mb-3 flex items-center justify-between gap-2">
            <h2 id={grantFormId} className="inline-flex items-center gap-2 text-base font-semibold text-ink">
              <Database className="h-4 w-4 text-accent" aria-hidden="true" />
              <span>Akses Database</span>
            </h2>
            <Badge tone="neutral" className="tabular-nums">
              {group.grants.length} database
            </Badge>
          </div>

          <form aria-labelledby={grantFormId} onSubmit={handleAddGrant} className="mb-4 flex flex-wrap gap-2">
            <label htmlFor="grant-conn-select" className="sr-only-live">
              Pilih koneksi database
            </label>
            <select
              id="grant-conn-select"
              name="connection_id"
              value={selectedConnId}
              disabled={candidateConnections.length === 0}
              onChange={(e) => setSelectedConnId(e.target.value)}
              className="w-full min-w-0 rounded-md border border-border bg-surface px-3 py-1.5 text-sm text-ink disabled:opacity-60 sm:w-auto sm:flex-1"
            >
              <option value="">
                {allConnections.length === 0
                  ? 'Belum ada koneksi database'
                  : candidateConnections.length === 0
                    ? 'Semua koneksi sudah diberi akses'
                    : 'Pilih database'}
              </option>
              {candidateConnections.map((c) => (
                <option key={c.id} value={c.id}>
                  {c.name} ({c.database_name})
                </option>
              ))}
            </select>

            <label htmlFor="grant-level-select" className="sr-only-live">
              Pilih tingkat akses
            </label>
            <select
              id="grant-level-select"
              name="access_level"
              value={selectedLevel}
              onChange={(e) => setSelectedLevel(e.target.value as Exclude<AccessLevel, 'none'>)}
              className="rounded-md border border-border bg-surface px-2.5 py-1.5 text-sm text-ink"
            >
              <option value="view_only">View Only</option>
              <option value="view_edit">View + Edit</option>
            </select>

            <Button
              type="submit"
              variant="primary"
              size="sm"
              loading={savingGrant}
              disabled={!selectedConnId}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Beri Akses</span>
            </Button>
          </form>

          {group.grants.length === 0 ? (
            <p className="rounded-lg border border-dashed border-border px-4 py-6 text-center text-sm text-muted">
              Group ini belum memiliki akses ke database mana pun.
            </p>
          ) : (
            <ul className="divide-y divide-border border-t border-border">
              {group.grants.map((gr) => {
                const currentLevel = grantLevels[gr.connection_id] ?? (gr.access_level as Exclude<AccessLevel, 'none'>);
                const changed = currentLevel !== gr.access_level;
                return (
                  <li
                    key={gr.connection_id}
                    className="flex flex-col gap-2 py-2.5 sm:flex-row sm:items-center sm:justify-between"
                  >
                    <div className="flex min-w-0 items-center gap-2">
                      <span className="truncate text-sm font-medium text-ink">
                        {gr.connection_name}
                      </span>
                      <AccessBadge level={gr.access_level} />
                    </div>
                    <form
                      onSubmit={(e) => void handleUpdateGrant(e, gr.connection_id)}
                      className="flex flex-wrap items-center gap-1.5"
                    >
                      <label htmlFor={`grant-lvl-${gr.connection_id}`} className="sr-only-live">
                        Ubah level akses {gr.connection_name}
                      </label>
                      <select
                        id={`grant-lvl-${gr.connection_id}`}
                        name="access_level"
                        value={currentLevel}
                        onChange={(e) =>
                          setGrantLevels((prev) => ({
                            ...prev,
                            [gr.connection_id]: e.target.value as Exclude<AccessLevel, 'none'>,
                          }))
                        }
                        className="rounded-md border border-border bg-surface px-2 py-1 text-xs text-ink"
                      >
                        <option value="view_only">View Only</option>
                        <option value="view_edit">View + Edit</option>
                      </select>
                      <Button
                        type="submit"
                        variant="secondary"
                        size="sm"
                        disabled={!changed}
                        loading={updatingConnId === gr.connection_id}
                      >
                        Simpan
                      </Button>
                      <Button
                        variant="danger-ghost"
                        size="sm"
                        onClick={() =>
                          setRemovingGrant({ connId: gr.connection_id, name: gr.connection_name })
                        }
                      >
                        Cabut
                      </Button>
                    </form>
                  </li>
                );
              })}
            </ul>
          )}

          <p className="mt-4 border-t border-dashed border-border pt-3 text-xs text-muted">
            Jika user tergabung di beberapa group untuk database yang sama, sistem memakai hak akses efektif tertinggi (<strong className="text-ink">View + Edit</strong> menang atas <strong className="text-ink">View Only</strong>).
          </p>
        </section>
      </div>

      <ConfirmDialog
        open={Boolean(removingMember)}
        title="Keluarkan anggota dari group?"
        description={`"${removingMember?.username ?? ''}" akan langsung kehilangan akses database yang berasal dari group "${group.name}".`}
        confirmLabel="Keluarkan Anggota"
        destructive
        loading={busyConfirm}
        onConfirm={() => void handleConfirmRemoveMember()}
        onCancel={() => setRemovingMember(null)}
      />

      <ConfirmDialog
        open={Boolean(removingGrant)}
        title="Cabut akses database?"
        description={`Seluruh anggota group "${group.name}" tidak lagi memperoleh akses ke "${removingGrant?.name ?? ''}" melalui group ini.`}
        confirmLabel="Cabut Akses"
        destructive
        loading={busyConfirm}
        onConfirm={() => void handleConfirmRemoveGrant()}
        onCancel={() => setRemovingGrant(null)}
      />

      <ConfirmDialog
        open={confirmDeleteGroup}
        title={`Hapus group "${group.name}"?`}
        description="Seluruh keanggotaan dan grant akses database milik group ini akan dihapus, dan sesi anggota yang terdampak akan dirotasi."
        confirmLabel="Hapus Group"
        destructive
        loading={deletingGroup}
        onConfirm={() => void handleConfirmDeleteGroup()}
        onCancel={() => setConfirmDeleteGroup(false)}
      />
    </div>
  );
}
