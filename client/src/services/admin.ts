import { apiRequest } from '@/lib/api';
import type {
  AccessLevel,
  AuthUser,
  ConnectionInput,
  ConnectionTestResult,
  DBConnection,
  Group,
  GroupDetail,
  GroupGrant,
  GroupMember,
} from '@/types/api';

// Users
export async function listUsers(): Promise<AuthUser[]> {
  const res = await apiRequest<{ users: AuthUser[] }>('/api/admin/users');
  return res.users ?? [];
}

export async function createUser(input: {
  username: string;
  password: string;
  is_platform_admin: boolean;
}): Promise<{ message: string; user: AuthUser }> {
  return apiRequest('/api/admin/users', { method: 'POST', body: input });
}

export async function updateUser(
  userId: string,
  input: { username: string; is_platform_admin: boolean; is_active: boolean },
): Promise<{ message: string; user: AuthUser }> {
  return apiRequest(`/api/admin/users/${encodeURIComponent(userId)}`, { method: 'PUT', body: input });
}

export async function resetUserPassword(userId: string, newPassword: string): Promise<{ message: string }> {
  return apiRequest(`/api/admin/users/${encodeURIComponent(userId)}/reset-password`, {
    method: 'POST',
    body: { new_password: newPassword },
  });
}

export async function revokeUserSessions(userId: string): Promise<{ message: string }> {
  return apiRequest(`/api/admin/users/${encodeURIComponent(userId)}/revoke-sessions`, { method: 'POST' });
}

export async function deleteUser(userId: string): Promise<{ message: string }> {
  return apiRequest(`/api/admin/users/${encodeURIComponent(userId)}`, { method: 'DELETE' });
}

// Groups & Grants
export async function listGroups(): Promise<Group[]> {
  const res = await apiRequest<{ groups: Group[] }>('/api/admin/groups');
  return res.groups ?? [];
}

export async function getGroup(groupId: number): Promise<GroupDetail> {
  return apiRequest<GroupDetail>(`/api/admin/groups/${groupId}`);
}

export async function createGroup(input: { name: string; description: string }): Promise<Group> {
  return apiRequest<Group>('/api/admin/groups', { method: 'POST', body: input });
}

export async function updateGroup(groupId: number, input: { name: string; description: string }): Promise<Group> {
  return apiRequest<Group>(`/api/admin/groups/${groupId}`, { method: 'PUT', body: input });
}

export async function setGroupLeader(groupId: number, userId: string): Promise<Group> {
  return apiRequest<Group>(`/api/admin/groups/${groupId}/leader`, {
    method: 'PUT',
    body: { user_id: userId },
  });
}

export async function deleteGroup(groupId: number): Promise<void> {
  await apiRequest<void>(`/api/admin/groups/${groupId}`, { method: 'DELETE' });
}

export async function addGroupMember(groupId: number, userId: string): Promise<GroupMember[]> {
  const res = await apiRequest<{ members: GroupMember[] }>(`/api/admin/groups/${groupId}/members`, {
    method: 'POST',
    body: { user_id: userId },
  });
  return res.members ?? [];
}

export async function removeGroupMember(groupId: number, userId: string): Promise<void> {
  await apiRequest<void>(`/api/admin/groups/${groupId}/members/${encodeURIComponent(userId)}`, {
    method: 'DELETE',
  });
}

export async function setGroupGrant(
  groupId: number,
  connectionId: number,
  accessLevel: Exclude<AccessLevel, 'none'>,
): Promise<GroupGrant[]> {
  const res = await apiRequest<{ grants: GroupGrant[] }>(
    `/api/admin/groups/${groupId}/grants/${connectionId}`,
    {
      method: 'PUT',
      body: { access_level: accessLevel },
    },
  );
  return res.grants ?? [];
}

export async function deleteGroupGrant(groupId: number, connectionId: number): Promise<void> {
  await apiRequest<void>(`/api/admin/groups/${groupId}/grants/${connectionId}`, { method: 'DELETE' });
}

// Connections
export async function listConnections(): Promise<DBConnection[]> {
  const res = await apiRequest<{ connections: DBConnection[] }>('/api/admin/connections');
  return res.connections ?? [];
}

export async function getConnection(connectionId: number): Promise<DBConnection> {
  return apiRequest<DBConnection>(`/api/admin/connections/${connectionId}`);
}

export async function createConnection(input: ConnectionInput): Promise<DBConnection> {
  return apiRequest<DBConnection>('/api/admin/connections', { method: 'POST', body: input });
}

export async function updateConnection(connectionId: number, input: ConnectionInput): Promise<DBConnection> {
  return apiRequest<DBConnection>(`/api/admin/connections/${connectionId}`, { method: 'PUT', body: input });
}

export async function deleteConnection(connectionId: number): Promise<void> {
  await apiRequest<void>(`/api/admin/connections/${connectionId}`, { method: 'DELETE' });
}

export async function testNewConnection(input: ConnectionInput): Promise<ConnectionTestResult> {
  return apiRequest<ConnectionTestResult>('/api/admin/connections/test', { method: 'POST', body: input });
}

export async function testSavedConnection(connectionId: number): Promise<ConnectionTestResult> {
  return apiRequest<ConnectionTestResult>(`/api/admin/connections/${connectionId}/test`, { method: 'POST' });
}
