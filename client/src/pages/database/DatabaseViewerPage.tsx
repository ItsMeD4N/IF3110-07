import { useCallback, useEffect, useId, useMemo, useRef, useState, type KeyboardEvent } from 'react';
import { useSearchParams } from 'react-router-dom';
import {
  ChevronLeft,
  ChevronRight,
  Columns3,
  Database,
  KeyRound,
  ListTree,
  RefreshCw,
  Rows3,
  Search,
  Table2,
} from 'lucide-react';
import { useDatabaseContext } from '@/components/layout/DatabaseLayout';
import { ApiError } from '@/lib/api';
import { formatNumber } from '@/lib/format';
import { getDatabaseSchema, getTableData } from '@/services/databases';
import type { SchemaResponse, SchemaTable, TableDataResponse } from '@/types/api';
import { Alert } from '@/components/ui/Alert';
import { Badge } from '@/components/ui/Badge';
import { Button } from '@/components/ui/Button';
import { EmptyState } from '@/components/ui/EmptyState';
import { SkeletonLine } from '@/components/ui/Skeleton';
import { TableShell } from '@/components/ui/TableShell';

type ViewerTab = 'structure' | 'data';
const PAGE_SIZES = [25, 50, 100] as const;

function formatCellValue(value: unknown): { text: string; isNull: boolean } {
  if (value === null || value === undefined) {
    return { text: 'NULL', isNull: true };
  }
  if (typeof value === 'boolean') {
    return { text: value ? 'true' : 'false', isNull: false };
  }
  if (typeof value === 'object') {
    try {
      return { text: JSON.stringify(value), isNull: false };
    } catch {
      return { text: String(value), isNull: false };
    }
  }
  return { text: String(value), isNull: false };
}

export function DatabaseViewerPage() {
  const { database } = useDatabaseContext();
  const [searchParams, setSearchParams] = useSearchParams();

  const initialSchema = searchParams.get('schema') || '';
  const initialTable = searchParams.get('table') || '';
  const initialTab: ViewerTab = searchParams.get('tab') === 'data' ? 'data' : 'structure';

  const [schemaData, setSchemaData] = useState<SchemaResponse | null>(null);
  const [selectedSchema, setSelectedSchema] = useState(initialSchema);
  const [selectedTableName, setSelectedTableName] = useState(initialTable);
  const [tableFilter, setTableFilter] = useState('');
  const [activeTab, setActiveTab] = useState<ViewerTab>(initialTab);
  const [focusedTab, setFocusedTab] = useState<ViewerTab>(initialTab);

  const [loadingSchema, setLoadingSchema] = useState(true);
  const [schemaError, setSchemaError] = useState('');

  const [tableData, setTableData] = useState<TableDataResponse | null>(null);
  const [loadingData, setLoadingData] = useState(false);
  const [dataError, setDataError] = useState('');
  const [limit, setLimit] = useState<number>(50);
  const [offset, setOffset] = useState<number>(0);

  const structureTabId = useId();
  const dataTabId = useId();
  const structureTabRef = useRef<HTMLButtonElement>(null);
  const dataTabRef = useRef<HTMLButtonElement>(null);

  const syncUrlParams = useCallback(
    (schema: string, table: string, tab: ViewerTab) => {
      const next = new URLSearchParams();
      if (schema) next.set('schema', schema);
      if (table) next.set('table', table);
      if (tab !== 'structure') next.set('tab', tab);
      setSearchParams(next, { replace: true });
    },
    [setSearchParams],
  );

  const loadSchema = useCallback(
    async (targetSchema?: string) => {
      setLoadingSchema(true);
      setSchemaError('');
      try {
        const res = await getDatabaseSchema(database.id, targetSchema || undefined);
        setSchemaData(res);
        setSelectedSchema(res.selected_schema);

        setSelectedTableName((prev) => {
          if (prev && res.tables.some((t) => t.name === prev)) {
            return prev;
          }
          return res.tables[0]?.name ?? '';
        });
      } catch (err) {
        setSchemaData(null);
        setSchemaError(
          err instanceof ApiError ? err.message : 'Gagal memuat informasi schema database.',
        );
      } finally {
        setLoadingSchema(false);
      }
    },
    [database.id],
  );

  useEffect(() => {
    void loadSchema(initialSchema || undefined);
  }, [database.id]);

  const selectedTable: SchemaTable | null = useMemo(() => {
    if (!schemaData || !selectedTableName) return null;
    return schemaData.tables.find((t) => t.name === selectedTableName) ?? null;
  }, [schemaData, selectedTableName]);

  const filteredTables = useMemo(() => {
    if (!schemaData) return [];
    const q = tableFilter.trim().toLowerCase();
    if (!q) return schemaData.tables;
    return schemaData.tables.filter((t) => t.name.toLowerCase().includes(q));
  }, [schemaData, tableFilter]);

  const loadTableData = useCallback(
    async (schemaName: string, tableName: string, pageLimit: number, pageOffset: number) => {
      if (!schemaName || !tableName) return;
      setLoadingData(true);
      setDataError('');
      try {
        const res = await getTableData(database.id, tableName, {
          schema: schemaName,
          limit: pageLimit,
          offset: pageOffset,
        });
        setTableData(res);
      } catch (err) {
        setTableData(null);
        setDataError(err instanceof ApiError ? err.message : 'Gagal memuat isi data tabel.');
      } finally {
        setLoadingData(false);
      }
    },
    [database.id],
  );

  useEffect(() => {
    if (activeTab === 'data' && selectedSchema && selectedTableName) {
      void loadTableData(selectedSchema, selectedTableName, limit, offset);
    }
  }, [activeTab, limit, loadTableData, offset, selectedSchema, selectedTableName]);

  const handleSchemaChange = (nextSchema: string) => {
    setSelectedSchema(nextSchema);
    setSelectedTableName('');
    setTableData(null);
    setOffset(0);
    syncUrlParams(nextSchema, '', activeTab);
    void loadSchema(nextSchema);
  };

  const handleSelectTable = (tableName: string) => {
    setSelectedTableName(tableName);
    setOffset(0);
    setTableData(null);
    syncUrlParams(selectedSchema, tableName, activeTab);
  };

  const handleSelectTab = (nextTab: ViewerTab) => {
    setActiveTab(nextTab);
    setFocusedTab(nextTab);
    syncUrlParams(selectedSchema, selectedTableName, nextTab);
  };

  const handleTabKeyDown = (e: KeyboardEvent<HTMLButtonElement>) => {
    if (e.key === 'ArrowRight' || e.key === 'ArrowLeft' || e.key === 'Home' || e.key === 'End') {
      e.preventDefault();
      let next: ViewerTab = focusedTab;
      if (e.key === 'Home') next = 'structure';
      else if (e.key === 'End') next = 'data';
      else next = focusedTab === 'structure' ? 'data' : 'structure';

      setFocusedTab(next);
      const targetRef = next === 'structure' ? structureTabRef : dataTabRef;
      targetRef.current?.focus();
    }
  };

  const totalRows = tableData?.total ?? 0;
  const rangeStart = totalRows === 0 ? 0 : offset + 1;
  const rangeEnd = Math.min(offset + (tableData?.rows.length ?? 0), totalRows);
  const canPrev = offset > 0;
  const canNext = offset + limit < totalRows;

  if (loadingSchema && !schemaData) {
    return (
      <div className="grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)]" role="status" aria-label="Memuat schema database">
        <div className="card-surface space-y-3 p-4">
          <SkeletonLine className="h-9 w-full" />
          <SkeletonLine className="h-5 w-3/4" />
          <SkeletonLine className="h-5 w-4/5" />
          <SkeletonLine className="h-5 w-2/3" />
        </div>
        <div className="card-surface space-y-3 p-5">
          <SkeletonLine className="h-6 w-48" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-full" />
          <SkeletonLine className="h-5 w-5/6" />
        </div>
      </div>
    );
  }

  if (schemaError) {
    return (
      <div className="space-y-4">
        <Alert variant="error" title="Gagal memuat schema database">
          {schemaError}
        </Alert>
        <Button variant="secondary" size="sm" onClick={() => void loadSchema(selectedSchema || undefined)}>
          <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
          <span>Coba Lagi</span>
        </Button>
      </div>
    );
  }

  if (!schemaData || schemaData.schemas.length === 0) {
    return (
      <EmptyState
        icon={Database}
        title="Belum ada schema yang dapat ditampilkan"
        description="Database target tidak memiliki schema pengguna selain schema sistem PostgreSQL."
        action={
          <Button variant="secondary" size="sm" onClick={() => void loadSchema()}>
            <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Muat Ulang</span>
          </Button>
        }
      />
    );
  }

  return (
    <div className="grid gap-5 lg:grid-cols-[260px_minmax(0,1fr)] lg:items-start">
      <section className="card-surface space-y-3 p-4 lg:hidden" aria-label="Pemilih schema dan tabel">
        <div className="grid gap-3 sm:grid-cols-2">
          <div className="space-y-1">
            <label htmlFor="mobile-schema-select" className="block text-xs font-semibold text-muted">
              Schema
            </label>
            <select
              id="mobile-schema-select"
              name="schema"
              value={selectedSchema}
              onChange={(e) => handleSchemaChange(e.target.value)}
              className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink"
            >
              {schemaData.schemas.map((sc) => (
                <option key={sc} value={sc}>
                  {sc}
                </option>
              ))}
            </select>
          </div>
          <div className="space-y-1">
            <label htmlFor="mobile-table-select" className="block text-xs font-semibold text-muted">
              Tabel / View ({schemaData.tables.length})
            </label>
            <select
              id="mobile-table-select"
              name="table"
              value={selectedTableName}
              disabled={schemaData.tables.length === 0}
              onChange={(e) => handleSelectTable(e.target.value)}
              className="w-full rounded-md border border-border bg-surface px-3 py-2 font-mono text-sm text-ink disabled:opacity-60"
            >
              {schemaData.tables.length === 0 ? (
                <option value="">Tidak ada tabel</option>
              ) : (
                schemaData.tables.map((t) => (
                  <option key={t.name} value={t.name}>
                    {t.name} {t.type === 'VIEW' ? '(view)' : ''}
                  </option>
                ))
              )}
            </select>
          </div>
        </div>
      </section>

      <aside
        aria-label="Daftar schema dan tabel"
        className="card-surface hidden flex-col p-3.5 lg:sticky lg:top-6 lg:flex lg:max-h-[calc(100dvh-4rem)]"
      >
        <div className="space-y-2.5 border-b border-border pb-3">
          <div className="flex items-center justify-between gap-2">
            <label htmlFor="desktop-schema-select" className="text-xs font-semibold text-muted">
              Schema
            </label>
            <Button
              variant="ghost"
              size="sm"
              loading={loadingSchema}
              onClick={() => void loadSchema(selectedSchema)}
              title="Muat ulang schema"
              className="h-7 px-2"
            >
              <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
              <span className="sr-only-live">Muat ulang schema</span>
            </Button>
          </div>
          <select
            id="desktop-schema-select"
            name="schema"
            value={selectedSchema}
            onChange={(e) => handleSchemaChange(e.target.value)}
            className="w-full rounded-md border border-border bg-surface px-2.5 py-1.5 font-mono text-sm text-ink"
          >
            {schemaData.schemas.map((sc) => (
              <option key={sc} value={sc}>
                {sc}
              </option>
            ))}
          </select>

          <search className="relative">
            <label htmlFor="filter-tables" className="sr-only-live">
              Filter nama tabel
            </label>
            <Search
              className="pointer-events-none absolute top-1/2 left-2.5 h-3.5 w-3.5 -translate-y-1/2 text-muted"
              aria-hidden="true"
            />
            <input
              id="filter-tables"
              name="q"
              type="search"
              autoCorrect="off"
              autoCapitalize="none"
              spellCheck={false}
              value={tableFilter}
              onChange={(e) => setTableFilter(e.target.value)}
              placeholder="Filter tabel…"
              className="w-full rounded-md border border-border bg-surface py-1.5 pr-2.5 pl-8 text-xs text-ink"
            />
          </search>
        </div>

        <div className="mt-2.5 flex items-center justify-between px-1 text-[11px] font-semibold text-muted">
          <span>Tabel &amp; View</span>
          <span className="tabular-nums">{filteredTables.length}</span>
        </div>

        <ul className="mt-1.5 min-h-0 flex-1 space-y-0.5 overflow-y-auto">
          {filteredTables.length === 0 ? (
            <li className="px-2 py-4 text-center text-xs text-muted">
              {tableFilter ? 'Tidak ada tabel yang cocok.' : 'Schema ini belum memiliki tabel.'}
            </li>
          ) : (
            filteredTables.map((t) => {
              const selected = t.name === selectedTableName;
              return (
                <li key={t.name}>
                  <button
                    type="button"
                    onClick={() => handleSelectTable(t.name)}
                    aria-current={selected ? 'true' : undefined}
                    className={`flex w-full cursor-pointer items-center justify-between gap-2 rounded-md px-2.5 py-1.5 text-left text-xs transition-colors ${
                      selected
                        ? 'bg-accent-soft font-semibold text-accent'
                        : 'text-ink hover:bg-surface-subtle'
                    }`}
                  >
                    <span className="flex min-w-0 items-center gap-1.5">
                      <Table2 className="h-3.5 w-3.5 shrink-0 opacity-75" aria-hidden="true" />
                      <span className="truncate font-mono">{t.name}</span>
                    </span>
                    <span className="shrink-0 text-[11px] text-muted tabular-nums">
                      {t.type === 'VIEW' ? 'VIEW' : `${t.columns.length} col`}
                    </span>
                  </button>
                </li>
              );
            })
          )}
        </ul>
      </aside>

      <div className="min-w-0 space-y-4">
        {!selectedTable ? (
          <EmptyState
            icon={Table2}
            title={`Schema "${selectedSchema}" kosong`}
            description="Belum ada tabel atau view pada schema ini. Kamu bisa membuat tabel baru melalui halaman Migrations."
          />
        ) : (
          <>
            <div className="flex flex-col gap-2 sm:flex-row sm:items-center sm:justify-between">
              <div className="flex min-w-0 flex-wrap items-center gap-2">
                <h2 className="truncate font-mono text-lg font-bold text-ink">
                  {selectedSchema}.{selectedTable.name}
                </h2>
                <Badge tone={selectedTable.type === 'VIEW' ? 'accent' : 'neutral'}>
                  {selectedTable.type}
                </Badge>
              </div>
              <div className="flex flex-wrap items-center gap-2 text-xs text-muted tabular-nums">
                <span>{selectedTable.columns.length} kolom</span>
                <span>·</span>
                <span>{selectedTable.constraints.length} constraint</span>
                <span>·</span>
                <span>{selectedTable.indexes.length} index</span>
              </div>
            </div>

            <div role="tablist" aria-label="Mode tampilan tabel" className="tab-strip">
              <button
                ref={structureTabRef}
                id={structureTabId}
                type="button"
                role="tab"
                aria-selected={activeTab === 'structure'}
                tabIndex={focusedTab === 'structure' ? 0 : -1}
                onClick={() => handleSelectTab('structure')}
                onKeyDown={handleTabKeyDown}
                className="tab-item"
              >
                <Columns3 className="h-4 w-4 shrink-0" aria-hidden="true" />
                <span>Struktur</span>
              </button>
              <button
                ref={dataTabRef}
                id={dataTabId}
                type="button"
                role="tab"
                aria-selected={activeTab === 'data'}
                tabIndex={focusedTab === 'data' ? 0 : -1}
                onClick={() => handleSelectTab('data')}
                onKeyDown={handleTabKeyDown}
                className="tab-item"
              >
                <Rows3 className="h-4 w-4 shrink-0" aria-hidden="true" />
                <span>Data</span>
              </button>
            </div>

            <div
              role="tabpanel"
              aria-labelledby={structureTabId}
              hidden={activeTab !== 'structure'}
            >
              <div className="space-y-6">
                <section aria-labelledby="cols-heading">
                  <div className="mb-2.5 flex items-center justify-between">
                    <h3
                      id="cols-heading"
                      className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink"
                    >
                      <Columns3 className="h-4 w-4 text-accent" aria-hidden="true" />
                      <span>Kolom ({selectedTable.columns.length})</span>
                    </h3>
                  </div>
                  <TableShell ariaLabelledBy="cols-heading">
                    <table>
                      <thead>
                        <tr>
                          <th scope="col" className="w-12">
                            #
                          </th>
                          <th scope="col">Nama Kolom</th>
                          <th scope="col">Tipe Data</th>
                          <th scope="col">Nullable</th>
                          <th scope="col">Default</th>
                        </tr>
                      </thead>
                      <tbody>
                        {selectedTable.columns.map((col) => (
                          <tr key={col.name} className="hover:bg-surface-subtle/50">
                            <td className="text-xs text-muted tabular-nums">{col.ordinal}</td>
                            <th scope="row" className="text-left font-mono text-xs font-semibold text-ink">
                              {col.name}
                            </th>
                            <td className="font-mono text-xs text-ink">{col.type}</td>
                            <td>
                              {col.nullable ? (
                                <Badge tone="neutral">NULL</Badge>
                              ) : (
                                <Badge tone="accent">NOT NULL</Badge>
                              )}
                            </td>
                            <td className="font-mono text-xs text-muted">
                              {col.default ? col.default : '—'}
                            </td>
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TableShell>
                </section>

                <section aria-labelledby="constraints-heading">
                  <div className="mb-2.5 flex items-center justify-between">
                    <h3
                      id="constraints-heading"
                      className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink"
                    >
                      <KeyRound className="h-4 w-4 text-accent" aria-hidden="true" />
                      <span>Constraints ({selectedTable.constraints.length})</span>
                    </h3>
                  </div>
                  {selectedTable.constraints.length === 0 ? (
                    <p className="rounded-lg border border-dashed border-border bg-surface px-4 py-4 text-xs text-muted">
                      Tidak ada constraint pada tabel ini.
                    </p>
                  ) : (
                    <TableShell ariaLabelledBy="constraints-heading">
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">Nama</th>
                            <th scope="col">Tipe</th>
                            <th scope="col">Definisi</th>
                          </tr>
                        </thead>
                        <tbody>
                          {selectedTable.constraints.map((c) => (
                            <tr key={`${c.name}-${c.type}`} className="hover:bg-surface-subtle/50">
                              <th scope="row" className="text-left font-mono text-xs font-semibold text-ink">
                                {c.name}
                              </th>
                              <td>
                                <Badge tone={c.type === 'PRIMARY KEY' ? 'accent' : 'neutral'}>
                                  {c.type}
                                </Badge>
                              </td>
                              <td className="font-mono text-xs text-muted">{c.definition}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </TableShell>
                  )}
                </section>

                <section aria-labelledby="indexes-heading">
                  <div className="mb-2.5 flex items-center justify-between">
                    <h3
                      id="indexes-heading"
                      className="inline-flex items-center gap-1.5 text-sm font-semibold text-ink"
                    >
                      <ListTree className="h-4 w-4 text-accent" aria-hidden="true" />
                      <span>Indexes ({selectedTable.indexes.length})</span>
                    </h3>
                  </div>
                  {selectedTable.indexes.length === 0 ? (
                    <p className="rounded-lg border border-dashed border-border bg-surface px-4 py-4 text-xs text-muted">
                      Tidak ada index pada tabel ini.
                    </p>
                  ) : (
                    <TableShell ariaLabelledBy="indexes-heading">
                      <table>
                        <thead>
                          <tr>
                            <th scope="col">Nama Index</th>
                            <th scope="col">Definisi</th>
                          </tr>
                        </thead>
                        <tbody>
                          {selectedTable.indexes.map((idx) => (
                            <tr key={idx.name} className="hover:bg-surface-subtle/50">
                              <th scope="row" className="text-left font-mono text-xs font-semibold text-ink">
                                {idx.name}
                              </th>
                              <td className="font-mono text-xs text-muted">{idx.definition}</td>
                            </tr>
                          ))}
                        </tbody>
                      </table>
                    </TableShell>
                  )}
                </section>
              </div>
            </div>

            <div
              role="tabpanel"
              aria-labelledby={dataTabId}
              hidden={activeTab !== 'data'}
            >
              <div className="space-y-3">
                <div className="flex flex-col gap-2.5 sm:flex-row sm:items-center sm:justify-between">
                  <div className="flex flex-wrap items-center gap-2 text-xs text-muted tabular-nums">
                    {tableData ? (
                      <span>
                        Menampilkan{' '}
                        <strong className="text-ink">
                          {formatNumber(rangeStart)}–{formatNumber(rangeEnd)}
                        </strong>{' '}
                        dari <strong className="text-ink">{formatNumber(totalRows)}</strong> baris
                      </span>
                    ) : (
                      <span>Memuat data tabel…</span>
                    )}
                  </div>

                  <div className="flex flex-wrap items-center gap-2">
                    <div
                      role="group"
                      aria-label="Baris per halaman"
                      className="inline-flex overflow-hidden rounded-md border border-border bg-surface text-xs"
                    >
                      {PAGE_SIZES.map((size) => (
                        <button
                          key={size}
                          type="button"
                          aria-pressed={limit === size}
                          onClick={() => {
                            setLimit(size);
                            setOffset(0);
                          }}
                          className={`cursor-pointer px-2.5 py-1 font-medium tabular-nums transition-colors ${
                            limit === size
                              ? 'bg-accent-soft text-accent'
                              : 'text-muted hover:bg-surface-subtle hover:text-ink'
                          }`}
                        >
                          {size}
                        </button>
                      ))}
                    </div>

                    <div className="inline-flex items-center gap-1">
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={!canPrev || loadingData}
                        onClick={() => setOffset((prev) => Math.max(0, prev - limit))}
                      >
                        <ChevronLeft className="h-3.5 w-3.5" aria-hidden="true" />
                        <span>Prev</span>
                      </Button>
                      <Button
                        variant="secondary"
                        size="sm"
                        disabled={!canNext || loadingData}
                        onClick={() => setOffset((prev) => prev + limit)}
                      >
                        <span>Next</span>
                        <ChevronRight className="h-3.5 w-3.5" aria-hidden="true" />
                      </Button>
                    </div>

                    <Button
                      variant="secondary"
                      size="sm"
                      loading={loadingData}
                      onClick={() =>
                        void loadTableData(selectedSchema, selectedTableName, limit, offset)
                      }
                    >
                      <RefreshCw className="h-3.5 w-3.5" aria-hidden="true" />
                      <span className="sr-only-live">Muat ulang data</span>
                    </Button>
                  </div>
                </div>

                {dataError && (
                  <Alert variant="error" title="Gagal memuat data tabel">
                    {dataError}
                  </Alert>
                )}

                {loadingData && !tableData ? (
                  <div className="card-surface space-y-2.5 p-5" role="status" aria-label="Memuat baris tabel">
                    <SkeletonLine className="h-5 w-full" />
                    <SkeletonLine className="h-5 w-full" />
                    <SkeletonLine className="h-5 w-4/5" />
                  </div>
                ) : tableData && tableData.rows.length === 0 ? (
                  <EmptyState
                    icon={Rows3}
                    title="Tabel belum memiliki data"
                    description={`Belum ada baris data pada ${selectedSchema}.${selectedTable.name}.`}
                  />
                ) : tableData ? (
                  <TableShell ariaLabel={`Data tabel ${selectedSchema}.${selectedTable.name}`}>
                    <table>
                      <thead>
                        <tr>
                          {tableData.columns.map((colName) => (
                            <th key={colName} scope="col" className="font-mono normal-case">
                              {colName}
                            </th>
                          ))}
                        </tr>
                      </thead>
                      <tbody>
                        {tableData.rows.map((row, rowIdx) => (
                          <tr key={rowIdx} className="hover:bg-surface-subtle/50">
                            {row.map((cell, colIdx) => {
                              const formatted = formatCellValue(cell);
                              return (
                                <td
                                  key={colIdx}
                                  title={formatted.text}
                                  className="max-w-[22rem] truncate font-mono text-xs tabular-nums"
                                >
                                  {formatted.isNull ? (
                                    <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[11px] text-muted italic">
                                      NULL
                                    </span>
                                  ) : (
                                    formatted.text
                                  )}
                                </td>
                              );
                            })}
                          </tr>
                        ))}
                      </tbody>
                    </table>
                  </TableShell>
                ) : null}
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
