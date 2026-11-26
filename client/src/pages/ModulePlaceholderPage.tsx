import { Link, useParams } from 'react-router-dom';
import { ArrowLeft, Wrench } from 'lucide-react';
import { PageHeader, type BreadcrumbItem } from '@/components/layout/PageHeader';
import { EmptyState } from '@/components/ui/EmptyState';

interface ModulePlaceholderPageProps {
  title: string;
  description: string;
  endpoints: string[];
  parentTo?: string;
  parentLabel?: string;
}

export function ModulePlaceholderPage({
  title,
  description,
  endpoints,
  parentTo = '/databases',
  parentLabel = 'Database Saya',
}: ModulePlaceholderPageProps) {
  const { dbId } = useParams();

  const breadcrumbs: BreadcrumbItem[] = dbId
    ? [
        { label: 'Database Saya', to: '/databases' },
        { label: `Database #${dbId}`, to: `/databases/${dbId}` },
        { label: title },
      ]
    : [{ label: parentLabel, to: parentTo }, { label: title }];

  return (
    <div>
      <PageHeader title={title} description={description} breadcrumbs={breadcrumbs} />
      <EmptyState
        icon={Wrench}
        title="Halaman siap diimplementasikan"
        description={`Fondasi SPA, autentikasi, CSRF, dan service API untuk modul ini sudah terhubung (${endpoints.join(', ')}).`}
        action={
          <Link
            to={parentTo}
            className="inline-flex min-h-9 items-center gap-2 rounded-md border border-border bg-surface px-3.5 py-1.5 text-sm font-medium text-ink no-underline hover:bg-surface-subtle"
          >
            <ArrowLeft className="h-4 w-4" aria-hidden="true" />
            <span>Kembali ke {parentLabel}</span>
          </Link>
        }
      />
    </div>
  );
}
