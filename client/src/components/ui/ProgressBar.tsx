interface ProgressBarProps {
  value: number;
  max: number;
  label: string;
  active?: boolean;
  tone?: 'accent' | 'ok' | 'warn' | 'danger';
}

const toneFill: Record<NonNullable<ProgressBarProps['tone']>, string> = {
  accent: 'bg-accent',
  ok: 'bg-ok',
  warn: 'bg-warn',
  danger: 'bg-danger',
};

export function ProgressBar({
  value,
  max,
  label,
  active = false,
  tone = 'accent',
}: ProgressBarProps) {
  const safeMax = Math.max(max, 1);
  const clamped = Math.max(0, Math.min(value, safeMax));
  const rawPct = (clamped / safeMax) * 100;
  const visualPct = active && clamped === 0 ? 4 : rawPct;

  return (
    <div
      role="progressbar"
      aria-label={label}
      aria-valuemin={0}
      aria-valuemax={safeMax}
      aria-valuenow={clamped}
      className="h-2.5 w-full overflow-hidden rounded-full bg-surface-subtle ring-1 ring-inset ring-border"
    >
      <div
        style={{ width: `${visualPct}%` }}
        className={`h-full rounded-full transition-[width] duration-200 ease-out motion-reduce:transition-none ${toneFill[tone]} ${
          active && clamped < safeMax ? 'animate-pulse' : ''
        }`}
      />
    </div>
  );
}
