import type { ReactNode } from 'react';
import { Link } from 'react-router-dom';
import { ChevronRight } from 'lucide-react';

export interface BreadcrumbItem {
  label: string;
  to?: string;
}

interface PageHeaderProps {
  title: string;
  description?: string;
  breadcrumbs?: BreadcrumbItem[];
  actions?: ReactNode;
}

export function PageHeader({ title, description, breadcrumbs, actions }: PageHeaderProps) {
  return (
    <header className="mb-6 flex flex-col gap-3 sm:flex-row sm:items-start sm:justify-between">
      <div className="min-w-0">
        {breadcrumbs && breadcrumbs.length > 0 && (
          <nav aria-label="Breadcrumb" className="mb-1.5 flex flex-wrap items-center gap-1 text-xs text-muted">
            {breadcrumbs.map((crumb, idx) => (
              <span key={`${crumb.label}-${idx}`} className="inline-flex items-center gap-1">
                {idx > 0 && <ChevronRight className="h-3.5 w-3.5 shrink-0" aria-hidden="true" />}
                {crumb.to ? (
                  <Link to={crumb.to} className="hover:text-ink hover:underline">
                    {crumb.label}
                  </Link>
                ) : (
                  <span className="text-ink/80">{crumb.label}</span>
                )}
              </span>
            ))}
          </nav>
        )}
        <h1 className="text-2xl font-bold tracking-tight text-ink">{title}</h1>
        {description && (
          <p className="mt-1 max-w-[68ch] text-sm leading-relaxed text-muted">{description}</p>
        )}
      </div>
      {actions && <div className="flex shrink-0 flex-wrap items-center gap-2">{actions}</div>}
    </header>
  );
}
