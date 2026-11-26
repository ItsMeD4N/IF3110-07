import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from 'react';
import { onUnauthorized } from '@/lib/api';
import { ensureCSRFToken } from '@/lib/csrf';
import * as authService from '@/services/auth';
import type { AuthMeResponse } from '@/types/api';

interface AuthContextValue {
  user: AuthMeResponse | null;
  loading: boolean;
  isLeader: boolean;
  refreshSession: () => Promise<AuthMeResponse | null>;
  signIn: (username: string, password: string) => Promise<AuthMeResponse>;
  signOut: () => Promise<void>;
}

const AuthContext = createContext<AuthContextValue | null>(null);

export function AuthProvider({ children }: { children: ReactNode }) {
  const [user, setUser] = useState<AuthMeResponse | null>(null);
  const [loading, setLoading] = useState(true);

  const refreshSession = useCallback(async () => {
    try {
      const me = await authService.fetchCurrentSession();
      setUser(me);
      return me;
    } catch {
      setUser(null);
      return null;
    }
  }, []);

  useEffect(() => {
    const controller = new AbortController();
    let mounted = true;

    async function bootstrap() {
      await ensureCSRFToken();
      try {
        const me = await authService.fetchCurrentSession(controller.signal);
        if (mounted) setUser(me);
      } catch {
        if (mounted) setUser(null);
      } finally {
        if (mounted) setLoading(false);
      }
    }

    void bootstrap();

    const unsubscribe = onUnauthorized(() => {
      setUser(null);
    });

    return () => {
      mounted = false;
      controller.abort();
      unsubscribe();
    };
  }, []);

  const signIn = useCallback(async (username: string, password: string) => {
    await authService.login(username, password);
    const me = await authService.fetchCurrentSession();
    setUser(me);
    return me;
  }, []);

  const signOut = useCallback(async () => {
    try {
      await authService.logout();
    } finally {
      setUser(null);
    }
  }, []);

  const value = useMemo<AuthContextValue>(
    () => ({
      user,
      loading,
      isLeader: (user?.led_groups?.length ?? 0) > 0,
      refreshSession,
      signIn,
      signOut,
    }),
    [loading, refreshSession, signIn, signOut, user],
  );

  return <AuthContext.Provider value={value}>{children}</AuthContext.Provider>;
}

export function useAuth(): AuthContextValue {
  const ctx = useContext(AuthContext);
  if (!ctx) {
    throw new Error('useAuth harus dipanggil di dalam AuthProvider');
  }
  return ctx;
}
