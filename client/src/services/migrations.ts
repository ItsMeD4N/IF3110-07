import { apiRequest } from '@/lib/api';
import { generateIdempotencyKey } from '@/lib/format';
import type {
  EnqueueResponse,
  ExplainResponse,
  LockInfo,
  MigrationDefinition,
  MigrationDefinitionInput,
  MigrationExecution,
  MigrationJob,
} from '@/types/api';

export async function listDefinitions(dbId: number): Promise<MigrationDefinition[]> {
  const res = await apiRequest<{ migrations: MigrationDefinition[] }>(
    `/api/databases/${dbId}/migrations/definitions`,
  );
  return res.migrations ?? [];
}

export async function getDefinition(dbId: number, migrationId: number): Promise<MigrationDefinition> {
  return apiRequest<MigrationDefinition>(`/api/databases/${dbId}/migrations/definitions/${migrationId}`);
}

export async function createDefinition(
  dbId: number,
  input: MigrationDefinitionInput,
): Promise<MigrationDefinition> {
  return apiRequest<MigrationDefinition>(`/api/databases/${dbId}/migrations/definitions`, {
    method: 'POST',
    body: input,
  });
}

export async function updateDefinition(
  dbId: number,
  migrationId: number,
  input: MigrationDefinitionInput,
): Promise<MigrationDefinition> {
  return apiRequest<MigrationDefinition>(`/api/databases/${dbId}/migrations/definitions/${migrationId}`, {
    method: 'PUT',
    body: input,
  });
}

export async function enqueueExecute(
  dbId: number,
  migrationId: number,
  idempotencyKey = generateIdempotencyKey('exec'),
): Promise<EnqueueResponse> {
  return apiRequest<EnqueueResponse>(
    `/api/databases/${dbId}/migrations/definitions/${migrationId}/execute`,
    { method: 'POST', idempotencyKey },
  );
}

export async function enqueueRollback(
  dbId: number,
  migrationId: number,
  idempotencyKey = generateIdempotencyKey('rb'),
): Promise<EnqueueResponse> {
  return apiRequest<EnqueueResponse>(
    `/api/databases/${dbId}/migrations/definitions/${migrationId}/rollback`,
    { method: 'POST', idempotencyKey },
  );
}

export async function enqueueForward(
  dbId: number,
  migrationId: number,
  idempotencyKey = generateIdempotencyKey('fwd'),
): Promise<EnqueueResponse> {
  return apiRequest<EnqueueResponse>(
    `/api/databases/${dbId}/migrations/definitions/${migrationId}/forward`,
    { method: 'POST', idempotencyKey },
  );
}

export async function listExecutions(
  dbId: number,
  hideRollback: boolean,
): Promise<{ hide_rollback: boolean; executions: MigrationExecution[] }> {
  return apiRequest(`/api/databases/${dbId}/migrations/executions?hide_rollback=${hideRollback ? 'true' : 'false'}`);
}

export async function getExecution(dbId: number, executionId: number): Promise<MigrationExecution> {
  return apiRequest<MigrationExecution>(`/api/databases/${dbId}/migrations/executions/${executionId}`);
}

export async function enqueueBatchResume(
  dbId: number,
  executionId: number,
  idempotencyKey = generateIdempotencyKey('resume'),
): Promise<EnqueueResponse> {
  return apiRequest<EnqueueResponse>(
    `/api/databases/${dbId}/migrations/executions/${executionId}/resume`,
    { method: 'POST', idempotencyKey },
  );
}

export async function enqueueBatchRevert(
  dbId: number,
  executionId: number,
  idempotencyKey = generateIdempotencyKey('revert'),
): Promise<EnqueueResponse> {
  return apiRequest<EnqueueResponse>(
    `/api/databases/${dbId}/migrations/executions/${executionId}/revert`,
    { method: 'POST', idempotencyKey },
  );
}

export async function reconcileExecution(
  dbId: number,
  executionId: number,
): Promise<{ message: string; execution: MigrationExecution }> {
  return apiRequest(`/api/databases/${dbId}/migrations/executions/${executionId}/reconcile`, {
    method: 'POST',
  });
}

export async function listJobs(dbId: number, limit = 50): Promise<MigrationJob[]> {
  const res = await apiRequest<{ jobs: MigrationJob[] }>(`/api/databases/${dbId}/jobs?limit=${limit}`);
  return res.jobs ?? [];
}

export async function getJob(
  dbId: number,
  jobId: string,
): Promise<{ job: MigrationJob; execution?: MigrationExecution }> {
  return apiRequest(`/api/databases/${dbId}/jobs/${encodeURIComponent(jobId)}`);
}

export async function getLockInfo(dbId: number): Promise<LockInfo> {
  return apiRequest<LockInfo>(`/api/databases/${dbId}/lock`);
}

export async function explainSQL(dbId: number, sql: string, analyze: boolean): Promise<ExplainResponse> {
  return apiRequest<ExplainResponse>(`/api/databases/${dbId}/migrations/explain`, {
    method: 'POST',
    body: { sql, analyze },
  });
}
