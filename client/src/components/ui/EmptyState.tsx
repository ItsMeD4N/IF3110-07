import type { ReactNode } from 'react';
import type { LucideIcon } from 'lucide-react';

interface EmptyStateProps {
  icon: LucideIcon;
  title: string;
  description: string;
  action?: ReactNode;
}

export function EmptyState({ icon: Icon, title, description, action }: EmptyStateProps) {
  return (
    <div className="flex flex-col items-center justify-center rounded-md border border-dashed border-border-strong px-6 py-10 text-center">
      <div className="flex h-10 w-10 items-center justify-center rounded-lg bg-surface-subtle text-muted">
        <Icon className="h-5 w-5" aria-hidden="true" />
      </div>
      <h2 className="mt-3 text-base font-semibold text-ink">{title}</h2>
      <p className="mt-1 max-w-md text-sm text-muted">{description}</p>
      {action && <div className="mt-4">{action}</div>}
    </div>
  );
}
