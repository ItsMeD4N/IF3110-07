import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useMemo,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { AlertCircle, CheckCircle2, Info, X } from 'lucide-react';

export type ToastKind = 'success' | 'error' | 'info';

interface ToastItem {
  id: string;
  kind: ToastKind;
  message: string;
  persistent?: boolean;
}

interface ToastContextValue {
  pushToast: (kind: ToastKind, message: string, options?: { persistent?: boolean }) => void;
  dismissToast: (id: string) => void;
}

const ToastContext = createContext<ToastContextValue | null>(null);

function announceToScreenReader(message: string, fallbackSetter: (text: string) => void) {
  const docWithAriaNotify = document as Document & {
    ariaNotify?: (text: string, opts?: { priority?: 'normal' | 'high' }) => void;
  };
  if (typeof docWithAriaNotify.ariaNotify === 'function') {
    docWithAriaNotify.ariaNotify(message, { priority: 'normal' });
    return;
  }
  fallbackSetter('');
  requestAnimationFrame(() => fallbackSetter(message));
}

export function ToastProvider({ children }: { children: ReactNode }) {
  const [toasts, setToasts] = useState<ToastItem[]>([]);
  const [liveAnnouncement, setLiveAnnouncement] = useState('');
  const pausedIdsRef = useRef(new Set<string>());
  const timersRef = useRef(new Map<string, number>());
  const dismissRefs = useRef(new Map<string, HTMLButtonElement | null>());

  const clearTimer = useCallback((id: string) => {
    const timer = timersRef.current.get(id);
    if (timer !== undefined) {
      window.clearTimeout(timer);
      timersRef.current.delete(id);
    }
  }, []);

  const dismissToast = useCallback(
    (id: string) => {
      clearTimer(id);
      pausedIdsRef.current.delete(id);
      setToasts((prev) => {
        const idx = prev.findIndex((t) => t.id === id);
        const remaining = prev.filter((t) => t.id !== id);
        if (idx !== -1 && remaining.length > 0) {
          const nextTarget = remaining[Math.min(idx, remaining.length - 1)];
          requestAnimationFrame(() => {
            dismissRefs.current.get(nextTarget.id)?.focus();
          });
        }
        return remaining;
      });
    },
    [clearTimer],
  );

  const scheduleAutoDismiss = useCallback(
    (id: string) => {
      clearTimer(id);
      const timer = window.setTimeout(() => {
        if (!pausedIdsRef.current.has(id)) {
          dismissToast(id);
        }
      }, 5500);
      timersRef.current.set(id, timer);
    },
    [clearTimer, dismissToast],
  );

  const pushToast = useCallback(
    (kind: ToastKind, message: string, options?: { persistent?: boolean }) => {
      const id = `toast-${Date.now()}-${Math.random().toString(36).slice(2, 7)}`;
      const persistent = options?.persistent ?? kind === 'error';
      setToasts((prev) => [...prev.slice(-3), { id, kind, message, persistent }]);
      announceToScreenReader(message, setLiveAnnouncement);
      if (!persistent) {
        scheduleAutoDismiss(id);
      }
    },
    [scheduleAutoDismiss],
  );

  useEffect(() => {
    const timers = timersRef.current;
    return () => {
      timers.forEach((t) => window.clearTimeout(t));
      timers.clear();
    };
  }, []);

  const value = useMemo(() => ({ pushToast, dismissToast }), [dismissToast, pushToast]);

  return (
    <ToastContext.Provider value={value}>
      {children}

      <p className="sr-only-live">Notifikasi aplikasi</p>
      <div className="sr-only-live" aria-live="polite" aria-atomic="true">
        {liveAnnouncement}
      </div>

      <div
        role="region"
        aria-label="Notifikasi"
        className="pointer-events-none fixed right-4 bottom-4 left-4 z-50 flex flex-col items-end gap-2 sm:left-auto sm:w-96"
      >
        {toasts.map((toast) => {
          const Icon =
            toast.kind === 'success' ? CheckCircle2 : toast.kind === 'error' ? AlertCircle : Info;
          const toneClasses =
            toast.kind === 'success'
              ? 'bg-ok-soft text-ok border-ok/25'
              : toast.kind === 'error'
                ? 'bg-danger-soft text-danger border-danger/30'
                : 'bg-accent-soft text-accent border-accent/25';

          return (
            <div
              key={toast.id}
              onFocus={() => {
                pausedIdsRef.current.add(toast.id);
                clearTimer(toast.id);
              }}
              onBlur={() => {
                pausedIdsRef.current.delete(toast.id);
                if (!toast.persistent) {
                  scheduleAutoDismiss(toast.id);
                }
              }}
              className={`toast-item pointer-events-auto flex w-full items-start gap-2.5 rounded-lg border px-3.5 py-2.5 text-sm shadow-lg ${toneClasses}`}
            >
              <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
              <p className="min-w-0 flex-1 leading-snug break-words">{toast.message}</p>
              <button
                ref={(el) => {
                  dismissRefs.current.set(toast.id, el);
                }}
                type="button"
                aria-label="Tutup notifikasi"
                onClick={() => dismissToast(toast.id)}
                className="inline-flex h-6 w-6 shrink-0 items-center justify-center rounded hover:bg-black/10 dark:hover:bg-white/10"
              >
                <X className="h-3.5 w-3.5" aria-hidden="true" />
              </button>
            </div>
          );
        })}
      </div>
    </ToastContext.Provider>
  );
}

export function useToast(): ToastContextValue {
  const ctx = useContext(ToastContext);
  if (!ctx) {
    throw new Error('useToast harus dipanggil di dalam ToastProvider');
  }
  return ctx;
}
