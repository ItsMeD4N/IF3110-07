import type { ReactNode } from 'react';
import type { AccessLevel, ExecutionStatus, JobStatus, WorkflowStatus } from '@/types/api';

export type BadgeTone = 'neutral' | 'accent' | 'ok' | 'warn' | 'danger';

const toneStyles: Record<BadgeTone, string> = {
  neutral: 'bg-transparent text-muted border-border',
  accent: 'bg-transparent text-ink border-ink',
  ok: 'bg-transparent text-ink border-border-strong',
  warn: 'bg-transparent text-muted border-border-strong border-dashed',
  danger: 'bg-ink text-bg border-ink',
};

export function Badge({
  tone = 'neutral',
  children,
  className = '',
}: {
  tone?: BadgeTone;
  children: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 text-xs font-medium whitespace-nowrap ${toneStyles[tone]} ${className}`}
    >
      {children}
    </span>
  );
}

export function AccessBadge({ level, label }: { level: AccessLevel; label?: string }) {
  if (level === 'view_edit') {
    return <Badge tone="accent">{label || 'View + Edit'}</Badge>;
  }
  if (level === 'view_only') {
    return <Badge tone="neutral">{label || 'View Only'}</Badge>;
  }
  return <Badge tone="neutral">{label || 'Tidak ada akses'}</Badge>;
}

export function StatusBadge({ status }: { status: WorkflowStatus | ExecutionStatus | JobStatus }) {
  switch (status) {
    case 'applied':
    case 'approved':
    case 'success':
    case 'completed':
      return <Badge tone="ok">{status}</Badge>;
    case 'pending_review':
    case 'queued':
    case 'pending':
    case 'running':
    case 'retrying':
    case 'partial':
    case 'partially_reverted':
      return <Badge tone="warn">{status}</Badge>;
    case 'failed':
    case 'rejected':
    case 'uncertain':
      return <Badge tone="danger">{status}</Badge>;
    default:
      return <Badge tone="neutral">{status}</Badge>;
  }
}
