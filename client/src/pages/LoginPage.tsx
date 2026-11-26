import { useState, type FormEvent } from 'react';
import { useLocation, useNavigate } from 'react-router-dom';
import { Eye, EyeOff, Layers, Lock } from 'lucide-react';
import { useAuth } from '@/context/AuthContext';
import { ApiError } from '@/lib/api';
import { Alert } from '@/components/ui/Alert';
import { Button } from '@/components/ui/Button';

export function LoginPage() {
  const { signIn } = useAuth();
  const navigate = useNavigate();
  const location = useLocation();
  const from = (location.state as { from?: string } | null)?.from;

  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [showPassword, setShowPassword] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const [error, setError] = useState('');

  const handleSubmit = async (e: FormEvent) => {
    e.preventDefault();
    const trimmedUser = username.trim();
    if (!trimmedUser || !password) {
      setError('Username dan password wajib diisi.');
      return;
    }

    setError('');
    setSubmitting(true);
    try {
      const me = await signIn(trimmedUser, password);
      const target = from && from !== '/login'
        ? from
        : me.is_platform_admin
          ? '/admin/users'
          : '/databases';
      navigate(target, { replace: true });
    } catch (err) {
      if (err instanceof ApiError) {
        setError(err.message);
      } else {
        setError('Login gagal. Coba beberapa saat lagi.');
      }
    } finally {
      setSubmitting(false);
    }
  };

  return (
    <div className="flex min-h-dvh flex-col items-center justify-center bg-bg px-4 py-10">
      <div className="w-full max-w-sm">
        <div className="mb-6 flex items-center gap-2.5">
          <span className="flex h-9 w-9 items-center justify-center rounded-lg bg-accent text-accent-ink">
            <Layers className="h-5 w-5" aria-hidden="true" />
          </span>
          <div>
            <p className="text-base font-bold tracking-tight text-ink">DB Platform</p>
            <p className="text-xs text-muted">PostgreSQL Access &amp; Migration Control</p>
          </div>
        </div>

        <section aria-labelledby="login-heading" className="card-surface p-6">
          <h1 id="login-heading" className="text-xl font-bold tracking-tight text-ink">
            Masuk ke akun
          </h1>
          <p className="mt-1 text-sm text-muted">
            Gunakan akun yang terdaftar oleh Platform Admin untuk mengelola atau meninjau database.
          </p>

          {error && (
            <Alert variant="error" className="mt-4">
              {error}
            </Alert>
          )}

          <form onSubmit={handleSubmit} className="mt-5 space-y-4" noValidate>
            <div className="space-y-1.5">
              <label htmlFor="login-username" className="block text-sm font-medium text-ink">
                Username
              </label>
              <input
                id="login-username"
                name="username"
                type="text"
                autoComplete="username"
                autoFocus
                required
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="admin"
                className="w-full rounded-md border border-border bg-surface px-3 py-2 text-sm text-ink placeholder:text-muted/70"
              />
            </div>

            <div className="space-y-1.5">
              <label htmlFor="login-password" className="block text-sm font-medium text-ink">
                Password
              </label>
              <div className="relative">
                <input
                  id="login-password"
                  name="password"
                  type={showPassword ? 'text' : 'password'}
                  autoComplete="current-password"
                  required
                  value={password}
                  onChange={(e) => setPassword(e.target.value)}
                  placeholder="Masukkan password"
                  className="w-full rounded-md border border-border bg-surface py-2 pr-10 pl-3 text-sm text-ink placeholder:text-muted/70"
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((prev) => !prev)}
                  aria-label={showPassword ? 'Sembunyikan password' : 'Tampilkan password'}
                  className="absolute top-1/2 right-2 inline-flex h-7 w-7 -translate-y-1/2 items-center justify-center rounded text-muted hover:bg-surface-subtle hover:text-ink"
                >
                  {showPassword ? (
                    <EyeOff className="h-4 w-4" aria-hidden="true" />
                  ) : (
                    <Eye className="h-4 w-4" aria-hidden="true" />
                  )}
                </button>
              </div>
            </div>

            <Button type="submit" variant="primary" loading={submitting} className="w-full">
              <Lock className="h-4 w-4" aria-hidden="true" />
              <span>{submitting ? 'Memverifikasi…' : 'Masuk'}</span>
            </Button>
          </form>
        </section>

        <p className="mt-4 text-center text-xs text-muted">
          Sesi dilindungi cookie HttpOnly, rotasi token berkala, dan proteksi CSRF.
        </p>
      </div>
    </div>
  );
}
