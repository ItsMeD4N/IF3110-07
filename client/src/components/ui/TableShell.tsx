import type { ReactNode } from 'react';

interface TableShellProps {
  ariaLabel?: string;
  ariaLabelledBy?: string;
  children: ReactNode;
  className?: string;
}

/**
 * Keyboard-scrollable accessible data table wrapper (tabindex=0, role=region, sticky thead).
 */
export function TableShell({ ariaLabel, ariaLabelledBy, children, className = '' }: TableShellProps) {
  return (
    <div
      tabIndex={0}
      role="region"
      aria-label={ariaLabel}
      aria-labelledby={ariaLabelledBy}
      className={`table-wrapper ${className}`}
    >
      {children}
    </div>
  );
}
