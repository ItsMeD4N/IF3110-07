import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from 'react';
import { Link, NavLink, useLocation, useNavigate } from 'react-router-dom';
import {
  CheckSquare,
  Database,
  Layers,
  LogOut,
  Menu,
  Server,
  Shield,
  Users,
  X,
} from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { useToast } from '@/context/ToastContext';
import { getInitials } from '@/lib/format';
import { Button } from '@/components/ui/Button';

interface NavEntry {
  to: string;
  label: string;
  icon: typeof Database;
}

function SidebarContent({
  onNavigate,
  onSignOut,
  signingOut,
}: {
  onNavigate?: () => void;
  onSignOut: () => void;
  signingOut: boolean;
}) {
  const { user, isLeader } = useAuth();

  const workspaceLinks: NavEntry[] = [
    { to: '/databases', label: 'Database Saya', icon: Database },
    { to: '/approvals', label: isLeader ? 'Pending Approvals' : 'Approval Workflow', icon: CheckSquare },
  ];

  const adminLinks: NavEntry[] = [
    { to: '/admin/users', label: 'Manajemen Pengguna', icon: Users },
    { to: '/admin/groups', label: 'Group & Akses', icon: Shield },
    { to: '/admin/connections', label: 'Koneksi Database', icon: Server },
  ];

  const linkClasses = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-2.5 rounded-md px-2.5 py-2 text-sm font-medium transition-colors duration-150 ${
      isActive
        ? 'bg-accent-soft text-accent'
        : 'text-ink hover:bg-surface-subtle'
    }`;

  return (
    <div className="flex h-full flex-col">
      <div className="flex items-center justify-between gap-2 px-4 py-4">
        <Link
          to="/databases"
          onClick={onNavigate}
          className="inline-flex items-center gap-2 text-sm font-bold tracking-tight text-ink no-underline"
        >
          <span className="flex h-7 w-7 items-center justify-center rounded-md bg-accent text-accent-ink">
            <Layers className="h-4 w-4" aria-hidden="true" />
          </span>
          <span>DB Platform</span>
        </Link>
      </div>

      <nav aria-label="Navigasi utama" className="flex-1 space-y-5 overflow-y-auto px-3 py-2">
        <div>
          <p className="mb-1.5 px-2.5 text-[11px] font-semibold tracking-wider text-muted uppercase">
            Workspace
          </p>
          <ul className="space-y-0.5">
            {workspaceLinks.map((item) => {
              const Icon = item.icon;
              return (
                <li key={item.to}>
                  <NavLink to={item.to} onClick={onNavigate} className={linkClasses}>
                    <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
                    <span>{item.label}</span>
                  </NavLink>
                </li>
              );
            })}
          </ul>
        </div>

        {user?.is_platform_admin && (
          <div>
            <p className="mb-1.5 px-2.5 text-[11px] font-semibold tracking-wider text-muted uppercase">
              Platform Admin
            </p>
            <ul className="space-y-0.5">
              {adminLinks.map((item) => {
                const Icon = item.icon;
                return (
                  <li key={item.to}>
                    <NavLink to={item.to} onClick={onNavigate} className={linkClasses}>
                      <Icon className="h-4 w-4 shrink-0" aria-hidden="true" />
                      <span>{item.label}</span>
                    </NavLink>
                  </li>
                );
              })}
            </ul>
          </div>
        )}
      </nav>

      {user && (
        <div className="border-t border-border p-3">
          <div className="mb-2.5 flex items-center gap-2.5 px-1.5">
            <span
              aria-hidden="true"
              className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-accent-soft text-xs font-bold text-accent"
            >
              {getInitials(user.username)}
            </span>
            <div className="min-w-0 flex-1">
              <p className="truncate text-sm font-semibold text-ink">{user.username}</p>
              <p className="truncate text-xs text-muted">
                {user.is_platform_admin
                  ? 'Platform Admin'
                  : isLeader
                    ? 'Group Leader'
                    : 'Member'}
              </p>
            </div>
          </div>
          <Button
            variant="secondary"
            size="sm"
            loading={signingOut}
            onClick={onSignOut}
            className="w-full"
          >
            <LogOut className="h-3.5 w-3.5" aria-hidden="true" />
            <span>Keluar</span>
          </Button>
        </div>
      )}
    </div>
  );
}

export function AppShell({ children }: { children: ReactNode }) {
  const { signOut } = useAuth();
  const { pushToast } = useToast();
  const navigate = useNavigate();
  const location = useLocation();
  const drawerId = useId();

  const [signingOut, setSigningOut] = useState(false);
  const [drawerExpanded, setDrawerExpanded] = useState(false);

  const drawerRef = useRef<HTMLDialogElement>(null);
  const scrollerRef = useRef<HTMLDivElement>(null);
  const sheetRef = useRef<HTMLDivElement>(null);

  const openDrawer = useCallback(async () => {
    const drawer = drawerRef.current;
    const scroller = scrollerRef.current;
    if (!drawer || !scroller) return;

    if (!drawer.open) {
      drawer.showModal();
    }

    if (!CSS.supports('scroll-initial-target', 'nearest')) {
      scroller.scrollTo({ left: scroller.offsetWidth, behavior: 'instant' });
      await new Promise<void>((resolve) =>
        requestAnimationFrame(() => requestAnimationFrame(() => resolve())),
      );
    }

    scroller.scrollTo({ left: 0, behavior: 'auto' });
  }, []);

  const closeDrawer = useCallback(() => {
    const scroller = scrollerRef.current;
    const drawer = drawerRef.current;
    if (!drawer?.open) return;
    if (!scroller) {
      drawer.close();
      setDrawerExpanded(false);
      return;
    }
    scroller.scrollTo({ left: scroller.offsetWidth, behavior: 'auto' });
  }, []);

  // Close drawer on route change
  useEffect(() => {
    const drawer = drawerRef.current;
    if (drawer?.open) {
      drawer.close();
      setDrawerExpanded(false);
    }
  }, [location.pathname]);

  // IntersectionObserver source of truth + scroll backdrop fallback
  useEffect(() => {
    const drawer = drawerRef.current;
    const scroller = scrollerRef.current;
    const sheet = sheetRef.current;
    if (!drawer || !scroller || !sheet) return;

    const visibleThreshold = 1 / Math.max(window.innerWidth, 320);

    const observer = new IntersectionObserver(
      (entries) => {
        const entry = entries[entries.length - 1];
        if (!entry) return;
        if (entry.intersectionRatio < visibleThreshold && drawer.open) {
          drawer.close();
          setDrawerExpanded(false);
        } else if (entry.intersectionRatio >= 0.99 && drawer.open) {
          setDrawerExpanded(true);
        }
      },
      { root: drawer, threshold: [visibleThreshold, 0.99] },
    );
    observer.observe(sheet);

    const handleCancel = (e: Event) => {
      if (e.cancelable) {
        e.preventDefault();
        closeDrawer();
      }
    };

    const handleBackdropClick = (e: MouseEvent) => {
      if (e.target instanceof Node && !sheet.contains(e.target)) {
        closeDrawer();
      }
    };

    drawer.addEventListener('cancel', handleCancel);
    drawer.addEventListener('click', handleBackdropClick);

    const supportsScrollTimeline = CSS.supports('animation-timeline: scroll()');
    const handleScroll = () => {
      const width = sheet.offsetWidth || 1;
      const ratio = Math.max(0, Math.min(1, 1 - scroller.scrollLeft / width));
      drawer.style.setProperty('--drawer-backdrop', String(ratio));
    };

    if (!supportsScrollTimeline) {
      scroller.addEventListener('scroll', handleScroll, { passive: true });
    }

    return () => {
      observer.disconnect();
      drawer.removeEventListener('cancel', handleCancel);
      drawer.removeEventListener('click', handleBackdropClick);
      if (!supportsScrollTimeline) {
        scroller.removeEventListener('scroll', handleScroll);
      }
    };
  }, [closeDrawer]);

  const handleSignOut = async () => {
    setSigningOut(true);
    try {
      await signOut();
      pushToast('info', 'Kamu telah keluar dari sesi.');
      navigate('/login', { replace: true });
    } finally {
      setSigningOut(false);
    }
  };

  return (
    <div className="min-h-dvh lg:grid lg:grid-cols-[248px_minmax(0,1fr)]">
      {/* Desktop persistent sidebar */}
      <aside className="hidden border-r border-border bg-sidebar lg:sticky lg:top-0 lg:block lg:h-dvh">
        <SidebarContent onSignOut={handleSignOut} signingOut={signingOut} />
      </aside>

      {/* Mobile swipeable navigation drawer (<dialog>) */}
      <dialog
        ref={drawerRef}
        id={drawerId}
        aria-label="Navigasi utama"
        className="Drawer lg:hidden"
      >
        <div ref={scrollerRef} className="Drawer-scroller">
          <div ref={sheetRef} className="Drawer-sheet">
            <div className="flex items-center justify-end border-b border-border px-3 py-2">
              <button
                type="button"
                aria-label="Tutup menu navigasi"
                autoFocus
                onClick={closeDrawer}
                className="inline-flex h-9 w-9 items-center justify-center rounded-md text-muted hover:bg-surface-subtle hover:text-ink"
              >
                <X className="h-5 w-5" aria-hidden="true" />
              </button>
            </div>
            <div className="min-h-0 flex-1">
              <SidebarContent
                onNavigate={closeDrawer}
                onSignOut={handleSignOut}
                signingOut={signingOut}
              />
            </div>
          </div>
        </div>
      </dialog>

      <div className="flex min-w-0 flex-col">
        {/* Mobile top navigation bar */}
        <header className="sticky top-0 z-30 flex items-center justify-between gap-3 border-b border-border bg-sidebar px-4 py-2.5 lg:hidden">
          <button
            type="button"
            aria-label="Buka menu navigasi"
            aria-expanded={drawerExpanded}
            aria-controls={drawerId}
            onClick={() => void openDrawer()}
            className="inline-flex h-9 w-9 items-center justify-center rounded-md border border-border bg-surface text-ink hover:bg-surface-subtle"
          >
            <Menu className="h-5 w-5" aria-hidden="true" />
          </button>
          <Link
            to="/databases"
            className="inline-flex items-center gap-2 text-sm font-bold text-ink no-underline"
          >
            <Layers className="h-4 w-4 text-accent" aria-hidden="true" />
            <span>DB Platform</span>
          </Link>
          <div className="w-9" aria-hidden="true" />
        </header>

        <main className="mx-auto w-full max-w-[1220px] min-w-0 flex-1 px-4 py-6 sm:px-6 lg:px-8 lg:py-8">
          {children}
        </main>
      </div>
    </div>
  );
}
