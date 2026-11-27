import type { ReactNode } from 'react';
import { AlertCircle, AlertTriangle, CheckCircle2, Info } from 'lucide-react';

export type AlertVariant = 'info' | 'success' | 'warning' | 'error';

interface AlertProps {
  variant?: AlertVariant;
  title?: string;
  children: ReactNode;
  className?: string;
}

const variantConfig: Record<AlertVariant, { classes: string; Icon: typeof Info; role: 'status' | 'alert' }> = {
  info: { classes: 'bg-surface text-ink border-border', Icon: Info, role: 'status' },
  success: { classes: 'bg-surface text-ink border-border-strong', Icon: CheckCircle2, role: 'status' },
  warning: { classes: 'bg-surface text-ink border-border-strong border-dashed', Icon: AlertTriangle, role: 'status' },
  error: { classes: 'bg-surface text-ink border-ink', Icon: AlertCircle, role: 'alert' },
};

export function Alert({ variant = 'info', title, children, className = '' }: AlertProps) {
  const { classes, Icon, role } = variantConfig[variant];
  return (
    <div
      role={role}
      className={`flex items-start gap-2.5 rounded-lg border px-3.5 py-2.5 text-sm leading-relaxed ${classes} ${className}`}
    >
      <Icon className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
      <div className="min-w-0 flex-1 space-y-0.5">
        {title && <p className="font-semibold">{title}</p>}
        <div>{children}</div>
      </div>
    </div>
  );
}
