import { ensureCSRFToken, readCSRFCookie } from '@/lib/csrf';
import type { ApiErrorPayload } from '@/types/api';

const MUTATING_METHODS = new Set(['POST', 'PUT', 'PATCH', 'DELETE']);

export class ApiError extends Error {
  readonly status: number;
  readonly code: string;
  readonly detail?: string;
  readonly lockState?: string;
  readonly blockingExecutionId?: number;

  constructor(status: number, payload: ApiErrorPayload, fallbackMessage: string) {
    super(payload.error || fallbackMessage);
    this.name = 'ApiError';
    this.status = status;
    this.code = payload.code || 'unknown_error';
    this.detail = payload.detail;
    this.lockState = payload.lock_state;
    this.blockingExecutionId = payload.blocking_execution_id;
  }
}

type UnauthorizedListener = () => void;
const unauthorizedListeners = new Set<UnauthorizedListener>();

export function onUnauthorized(listener: UnauthorizedListener): () => void {
  unauthorizedListeners.add(listener);
  return () => {
    unauthorizedListeners.delete(listener);
  };
}

function notifyUnauthorized() {
  unauthorizedListeners.forEach((fn) => fn());
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PUT' | 'PATCH' | 'DELETE';
  body?: unknown;
  headers?: Record<string, string>;
  idempotencyKey?: string;
  skipAuthRedirect?: boolean;
  signal?: AbortSignal;
}

export async function apiRequest<T>(path: string, options: RequestOptions = {}): Promise<T> {
  const method = options.method ?? 'GET';
  const headers: Record<string, string> = {
    Accept: 'application/json',
    ...options.headers,
  };

  if (options.body !== undefined) {
    headers['Content-Type'] = 'application/json';
  }

  if (options.idempotencyKey) {
    headers['Idempotency-Key'] = options.idempotencyKey;
  }

  if (MUTATING_METHODS.has(method)) {
    const token = readCSRFCookie() || (await ensureCSRFToken());
    if (token) {
      headers['X-CSRF-Token'] = token;
    }
  }

  let response: Response;
  try {
    response = await fetch(path, {
      method,
      credentials: 'same-origin',
      headers,
      body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
      signal: options.signal,
    });
  } catch (err) {
    if (err instanceof DOMException && err.name === 'AbortError') {
      throw err;
    }
    throw new ApiError(
      0,
      { code: 'network_error', error: 'Tidak dapat terhubung ke server. Periksa koneksi atau status backend.' },
      'Tidak dapat terhubung ke server.',
    );
  }

  if (response.status === 204) {
    return undefined as T;
  }

  const contentType = response.headers.get('content-type') || '';
  const isJSON = contentType.includes('application/json');
  const parsed = isJSON ? await response.json().catch(() => ({})) : {};

  if (!response.ok) {
    if (response.status === 401 && !options.skipAuthRedirect) {
      notifyUnauthorized();
    }
    const payload = (typeof parsed === 'object' && parsed !== null ? parsed : {}) as ApiErrorPayload;
    throw new ApiError(
      response.status,
      payload,
      defaultStatusMessage(response.status),
    );
  }

  return parsed as T;
}

function defaultStatusMessage(status: number): string {
  switch (status) {
    case 400:
      return 'Permintaan tidak valid.';
    case 401:
      return 'Sesi berakhir. Silakan login kembali.';
    case 403:
      return 'Kamu tidak memiliki izin untuk aksi ini.';
    case 404:
      return 'Data tidak ditemukan atau kamu tidak punya akses.';
    case 409:
      return 'Terjadi konflik status data.';
    case 422:
      return 'Data yang dikirim tidak lolos validasi.';
    case 503:
      return 'Database target tidak dapat dijangkau saat ini.';
    default:
      return 'Terjadi kesalahan pada server.';
  }
}
