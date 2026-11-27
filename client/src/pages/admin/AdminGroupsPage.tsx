import { useCallback, useEffect, useId, useMemo, useRef, useState, type FormEvent } from 'react';
import { Link, useNavigate } from 'react-router-dom';
import { ArrowRight, Crown, Plus, RefreshCw, Search, Shield, Users } from 'lucide-react';
import { useToast } from '@/context/ToastContext';
import { ApiError } from '@/lib/api';
import * as adminService from '@/services/admin';
import type { Group } from '@/types/api';
import { PageHeader } from '@/components/layout/PageHeader';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

export function AdminGroupsPage() {
  const { pushToast } = useToast();
  const navigate = useNavigate();

  const [groups, setGroups] = useState<Group[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const [query, setQuery] = useState('');

  const [showCreate, setShowCreate] = useState(false);
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [createError, setCreateError] = useState('');
  const [creating, setCreating] = useState(false);
  const nameRef = useRef<HTMLInputElement>(null);

  const createHeadingId = useId();
  const tableCaptionId = useId();

  const loadGroups = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const data = await adminService.listGroups();
      setGroups(data);
    } catch (err) {
      setError(err instanceof ApiError ? err.message : 'Gagal memuat daftar group.');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => {
    void loadGroups();
  }, [loadGroups]);

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    if (!q) return groups;
    return groups.filter(
      (g) =>
        g.name.toLowerCase().includes(q) ||
        g.description.toLowerCase().includes(q) ||
        (g.leader_username ?? '').toLowerCase().includes(q),
    );
  }, [groups, query]);

  const handleCreateGroup = async (e: FormEvent) => {
    e.preventDefault();
    const trimmedName = name.trim();
    if (!trimmedName) {
      setCreateError('Nama group wajib diisi.');
      nameRef.current?.focus();
      return;
    }
    if (trimmedName.length > 64) {
      setCreateError('Nama group maksimal 64 karakter.');
      nameRef.current?.focus();
      return;
    }

    setCreateError('');
    setCreating(true);
    try {
      const created = await adminService.createGroup({
        name: trimmedName,
        description: description.trim(),
      });
      pushToast('success', `Group "${created.name}" dibuat. Tambahkan anggota, leader, dan grant database.`);
      navigate(`/admin/groups/${created.id}`);
    } catch (err) {
      setCreateError(err instanceof ApiError ? err.message : 'Gagal membuat group.');
    } finally {
      setCreating(false);
    }
  };

  return (
    <div>
      <PageHeader
        title="Group & Akses Database"
        description="Akses database diberikan melalui group. Tetapkan leader untuk setiap group agar approval migration multi-tim dapat berjalan."
        actions={
          <>
            <Button variant="secondary" size="sm" onClick={() => void loadGroups()} loading={loading}>
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span>Muat Ulang</span>
            </Button>
            <Button
              variant="primary"
              size="sm"
              aria-expanded={showCreate}
              onClick={() => {
                setShowCreate((p) => !p);
                setCreateError('');
              }}
            >
              <Plus className="h-3.5 w-3.5" aria-hidden="true" />
              <span>{showCreate ? 'Tutup Form' : 'Buat Group'}</span>
            </Button>
          </>
        }
      />

      {showCreate && (
        <section className="card-surface mb-6 p-5" aria-labelledby={createHeadingId}>
          <h2 id={createHeadingId} className="text-base font-semibold text-ink">
            Buat Group Baru
          </h2>
          <p className="mt-1 text-xs text-muted">
            Setelah group dibuat, kamu akan diarahkan ke halaman detail untuk menambah anggota, menetapkan leader, dan memberi akses database.
          </p>

          {createError && (
            <Alert variant="error" className="mt-3">
              {createError}
            </Alert>
          )}

          <form aria-labelledby={createHeadingId} onSubmit={handleCreateGroup} className="mt-4 space-y-4" noValidate>
            <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
              <div className="space-y-1">
                <label htmlFor="group-name" className="block text-sm font-medium text-ink">
                  Nama Group <span aria-hidden="true" className="text-danger">*</span>
                </label>
                <input
                  ref={nameRef}
                  id="group-name"
                  name="name"
                  type="text"
                  maxLength={64}
                  required
                  aria-invalid={Boolean(createError)}
                  aria-describedby="group-name-hint"
                  value={name}
                  onChange={(e) => {
                    setName(e.target.value);
                    if (createError) setCreateError('');
                  }}
                  placeholder="mis. Backend Team"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
                />
                <p id="group-name-hint" className="text-xs text-muted">
                  Maksimal 64 karakter, unik.
                </p>
              </div>

              <div className="space-y-1">
                <label htmlFor="group-desc" className="block text-sm font-medium text-ink">
                  Deskripsi
                </label>
                <input
                  id="group-desc"
                  name="description"
                  type="text"
                  maxLength={500}
                  value={description}
                  onChange={(e) => setDescription(e.target.value)}
                  placeholder="Tim pengelola layanan inti & schema orders"
                  className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink"
                />
              </div>
            </div>

            <div className="flex justify-end gap-2">
              <Button variant="secondary" disabled={creating} onClick={() => setShowCreate(false)}>
                Batal
              </Button>
              <Button type="submit" variant="primary" loading={creating}>
                Buat &amp; Kelola Group
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
          <label htmlFor="search-groups" className="sr-only-live">
            Cari group
          </label>
          <Search
            className="pointer-events-none absolute top-1/2 left-3 h-4 w-4 -translate-y-1/2 text-muted"
            aria-hidden="true"
          />
          <input
            id="search-groups"
            name="q"
            type="search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder="Cari nama group atau leader…"
            className="w-full rounded-md border border-border bg-surface py-2 pr-3 pl-9 text-sm text-ink"
          />
        </search>
        <p className="text-xs text-muted tabular-nums">
          Total <span className="font-semibold text-ink">{filtered.length}</span> group
        </p>
      </div>

      {loading ? (
        <div className="card-surface space-y-3 p-5" role="status" aria-label="Memuat daftar group">
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-3/4" />
        </div>
      ) : filtered.length === 0 ? (
        <EmptyState
          icon={Shield}
          title={query ? 'Group tidak ditemukan' : 'Belum ada group'}
          description={
            query
              ? `Tidak ada group yang cocok dengan "${query}".`
              : 'Buat group pertama untuk mengelompokkan pengguna dan menetapkan hak akses database.'
          }
          action={
            !query ? (
              <Button variant="primary" size="sm" onClick={() => setShowCreate(true)}>
                <Plus className="h-3.5 w-3.5" aria-hidden="true" />
                <span>Buat Group Pertama</span>
              </Button>
            ) : undefined
          }
        />
      ) : (
        <TableShell ariaLabelledBy={tableCaptionId}>
          <table>
            <caption id={tableCaptionId} className="sr-only-live">
              Daftar group dan ringkasan akses database
            </caption>
            <thead>
              <tr>
                <th scope="col">Group</th>
                <th scope="col">Leader</th>
                <th scope="col" className="text-right">
                  Anggota
                </th>
                <th scope="col" className="text-right">
                  Akses DB
                </th>
                <th scope="col" className="text-right">
                  Aksi
                </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((g) => (
                <tr key={g.id} className="hover:bg-surface-subtle/50">
                  <th scope="row" className="text-left">
                    <Link
                      to={`/admin/groups/${g.id}`}
                      className="font-semibold text-ink no-underline hover:text-accent hover:underline"
                    >
                      {g.name}
                    </Link>
                    {g.description && (
                      <p className="mt-0.5 max-w-md truncate text-xs font-normal text-muted">
                        {g.description}
                      </p>
                    )}
                  </th>
                  <td>
                    {g.leader_username ? (
                      <Badge tone="accent">
                        <Crown className="h-3 w-3" aria-hidden="true" />
                        <span>{g.leader_username}</span>
                      </Badge>
                    ) : (
                      <Badge tone="warn">Belum ditetapkan</Badge>
                    )}
                  </td>
                  <td className="text-right tabular-nums">
                    <span className="inline-flex items-center gap-1 text-sm text-ink">
                      <Users className="h-3.5 w-3.5 text-muted" aria-hidden="true" />
                      {g.member_count}
                    </span>
                  </td>
                  <td className="text-right text-sm text-ink tabular-nums">{g.grant_count}</td>
                  <td className="text-right">
                    <Link
                      to={`/admin/groups/${g.id}`}
                      className="inline-flex min-h-8 items-center gap-1 rounded-md border border-border bg-surface px-2.5 py-1 text-xs font-medium text-ink no-underline hover:bg-surface-subtle"
                    >
                      <span>Kelola</span>
                      <ArrowRight className="h-3.5 w-3.5" aria-hidden="true" />
                    </Link>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </TableShell>
      )}
    </div>
  );
}
