import { apiRequest } from '@/lib/api';
import type { MyDatabase, SchemaResponse, TableDataResponse } from '@/types/api';

export async function listMyDatabases(signal?: AbortSignal): Promise<MyDatabase[]> {
  const res = await apiRequest<{ databases: MyDatabase[] }>('/api/databases', { signal });
  return res.databases ?? [];
}

export async function getMyDatabase(dbId: number, signal?: AbortSignal): Promise<MyDatabase> {
  return apiRequest<MyDatabase>(`/api/databases/${dbId}`, { signal });
}

export async function checkDatabaseStatus(dbId: number): Promise<{ ok: boolean }> {
  return apiRequest<{ ok: boolean }>(`/api/databases/${dbId}/status`);
}

export async function getDatabaseSchema(dbId: number, schema?: string): Promise<SchemaResponse> {
  const query = schema ? `?schema=${encodeURIComponent(schema)}` : '';
  return apiRequest<SchemaResponse>(`/api/databases/${dbId}/schema${query}`);
}

export async function getTableData(
  dbId: number,
  table: string,
  params: { schema?: string; limit?: number; offset?: number } = {},
): Promise<TableDataResponse> {
  const qs = new URLSearchParams();
  if (params.schema) qs.set('schema', params.schema);
  if (params.limit) qs.set('limit', String(params.limit));
  if (params.offset !== undefined) qs.set('offset', String(params.offset));
  const suffix = qs.toString() ? `?${qs.toString()}` : '';
  return apiRequest<TableDataResponse>(
    `/api/databases/${dbId}/tables/${encodeURIComponent(table)}/data${suffix}`,
  );
}
