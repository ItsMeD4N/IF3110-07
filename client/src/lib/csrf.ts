const CSRF_COOKIE_NAME = 'csrf_token';

/**
 * Reads the current CSRF cookie directly from document.cookie.
 * Always reads fresh because the backend rotates csrf_token upon login.
 */
export function readCSRFCookie(): string {
  if (typeof document === 'undefined') return '';
  const cookies = document.cookie.split(';');
  for (const part of cookies) {
    const trimmed = part.trim();
    if (trimmed.startsWith(`${CSRF_COOKIE_NAME}=`)) {
      return decodeURIComponent(trimmed.slice(CSRF_COOKIE_NAME.length + 1));
    }
  }
  return '';
}

let inflightFetch: Promise<string> | null = null;

/**
 * Ensures a valid CSRF token exists before sending mutating requests.
 * Calls GET /api/csrf-token if the cookie is not yet present.
 */
export async function ensureCSRFToken(): Promise<string> {
  const existing = readCSRFCookie();
  if (existing) return existing;

  if (!inflightFetch) {
    inflightFetch = fetch('/api/csrf-token', {
      method: 'GET',
      credentials: 'same-origin',
      headers: { Accept: 'application/json' },
    })
      .then(async (res) => {
        const fromCookie = readCSRFCookie();
        if (fromCookie) return fromCookie;
        if (!res.ok) return '';
        const data = (await res.json()) as { csrf_token?: string };
        return data.csrf_token ?? '';
      })
      .catch(() => '')
      .finally(() => {
        inflightFetch = null;
      });
  }

  return inflightFetch;
}
