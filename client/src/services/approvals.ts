import { apiRequest } from '@/lib/api';
import type { ApprovalRequest, MigrationAction } from '@/types/api';

/**
 * Contract endpoints defined in /api_contract.md Section 2.2 (Workflow & Multi-Group Leader Approval).
 */
export async function submitApprovalRequest(
  dbId: number,
  migrationId: number,
  input: { action: MigrationAction; target_execution_id?: number; note?: string },
): Promise<ApprovalRequest> {
  return apiRequest<ApprovalRequest>(
    `/api/databases/${dbId}/migrations/definitions/${migrationId}/approval-requests`,
    { method: 'POST', body: input },
  );
}

export async function listDatabaseApprovalRequests(
  dbId: number,
  params: { status?: string; migration_id?: number } = {},
): Promise<ApprovalRequest[]> {
  const qs = new URLSearchParams();
  if (params.status) qs.set('status', params.status);
  if (params.migration_id) qs.set('migration_id', String(params.migration_id));
  const suffix = qs.toString() ? `?${qs.toString()}` : '';
  const res = await apiRequest<{ approval_requests: ApprovalRequest[] }>(
    `/api/databases/${dbId}/approval-requests${suffix}`,
  );
  return res.approval_requests ?? [];
}

export async function listPendingApprovals(signal?: AbortSignal): Promise<ApprovalRequest[]> {
  const res = await apiRequest<{ approval_requests: ApprovalRequest[] }>('/api/approvals/pending', {
    signal,
  });
  return res.approval_requests ?? [];
}

export async function approveRequest(
  dbId: number,
  requestId: number,
  comment?: string,
): Promise<ApprovalRequest> {
  return apiRequest<ApprovalRequest>(
    `/api/databases/${dbId}/approval-requests/${requestId}/approve`,
    { method: 'POST', body: { comment: comment ?? '' } },
  );
}

export async function rejectRequest(
  dbId: number,
  requestId: number,
  comment: string,
): Promise<ApprovalRequest> {
  return apiRequest<ApprovalRequest>(
    `/api/databases/${dbId}/approval-requests/${requestId}/reject`,
    { method: 'POST', body: { comment } },
  );
}
