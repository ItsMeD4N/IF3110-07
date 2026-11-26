import { apiRequest } from '@/lib/api';
import { ensureCSRFToken } from '@/lib/csrf';
import type { AuthMeResponse, AuthUser } from '@/types/api';

export async function fetchCurrentSession(signal?: AbortSignal): Promise<AuthMeResponse> {
  return apiRequest<AuthMeResponse>('/api/authz/me', {
    method: 'GET',
    skipAuthRedirect: true,
    signal,
  });
}

export async function login(username: string, password: string): Promise<{ message: string; user: AuthUser }> {
  await ensureCSRFToken();
  return apiRequest<{ message: string; user: AuthUser }>('/api/auth/login', {
    method: 'POST',
    body: { username, password },
    skipAuthRedirect: true,
  });
}

export async function logout(): Promise<{ message: string }> {
  return apiRequest<{ message: string }>('/api/auth/logout', {
    method: 'POST',
    skipAuthRedirect: true,
  });
}
