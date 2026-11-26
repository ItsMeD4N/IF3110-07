import type { ButtonHTMLAttributes, ReactNode } from 'react';
import { Loader2 } from 'lucide-react';

export type ButtonVariant = 'primary' | 'secondary' | 'danger' | 'danger-ghost' | 'ghost';
export type ButtonSize = 'sm' | 'md';

interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  loading?: boolean;
  children: ReactNode;
}

const variantClasses: Record<ButtonVariant, string> = {
  primary:
    'bg-accent text-accent-ink border-accent hover:bg-accent-hover disabled:opacity-55',
  secondary:
    'bg-surface text-ink border-border hover:bg-surface-subtle disabled:opacity-55',
  danger:
    'bg-danger text-white border-danger hover:brightness-110 disabled:opacity-55',
  'danger-ghost':
    'bg-transparent text-danger border-transparent hover:bg-danger-soft disabled:opacity-55',
  ghost:
    'bg-transparent text-ink border-transparent hover:bg-surface-subtle disabled:opacity-55',
};

const sizeClasses: Record<ButtonSize, string> = {
  sm: 'min-h-8 px-2.5 py-1 text-xs gap-1.5',
  md: 'min-h-9 px-3.5 py-1.5 text-sm gap-2',
};

export function Button({
  variant = 'secondary',
  size = 'md',
  loading = false,
  disabled,
  className = '',
  type = 'button',
  children,
  ...rest
}: ButtonProps) {
  return (
    <button
      type={type}
      disabled={disabled || loading}
      className={`inline-flex cursor-pointer items-center justify-center rounded-md border font-medium whitespace-nowrap transition-colors duration-150 disabled:cursor-not-allowed ${variantClasses[variant]} ${sizeClasses[size]} ${className}`}
      {...rest}
    >
      {loading && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />}
      {children}
    </button>
  );
}
