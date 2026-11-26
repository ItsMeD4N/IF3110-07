import { Link } from 'react-router-dom';
import { ArrowLeft, Compass } from 'lucide-react';
import { EmptyState } from '@/components/ui/EmptyState';

export function NotFoundPage() {
  return (
    <div className="mx-auto max-w-lg py-12">
      <EmptyState
        icon={Compass}
        title="Halaman tidak ditemukan"
        description="URL yang kamu buka tidak terdaftar di aplikasi SPA ini."
        action={
          <Link
            to="/databases"
            className="inline-flex min-h-9 items-center gap-2 rounded-md bg-accent px-3.5 py-1.5 text-sm font-medium text-accent-ink no-underline hover:bg-accent-hover"
          >
            <ArrowLeft className="h-4 w-4" aria-hidden="true" />
            <span>Kembali ke Beranda</span>
          </Link>
        }
      />
    </div>
  );
}
