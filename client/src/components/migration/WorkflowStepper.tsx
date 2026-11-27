import { Check, Circle, X } from 'lucide-react';
import type { WorkflowStatus } from '@/types/api';

const STEPS = [
  { key: 'draft', label: 'Draft' },
  { key: 'pending_review', label: 'Pending Review' },
  { key: 'approved', label: 'Approved' },
  { key: 'queued', label: 'Queue' },
  { key: 'running', label: 'Running' },
  { key: 'terminal', label: 'Result' },
] as const;

function resolveStepIndex(status: WorkflowStatus): { index: number; terminalState?: 'ok' | 'fail' | 'warn' } {
  switch (status) {
    case 'draft':
      return { index: 0 };
    case 'pending_review':
      return { index: 1 };
    case 'rejected':
      return { index: 1, terminalState: 'fail' };
    case 'approved':
      return { index: 2 };
    case 'queued':
      return { index: 3 };
    case 'running':
      return { index: 4 };
    case 'applied':
    case 'rolled_back':
      return { index: 5, terminalState: 'ok' };
    case 'partial':
    case 'partially_reverted':
      return { index: 5, terminalState: 'warn' };
    case 'failed':
    case 'uncertain':
      return { index: 5, terminalState: 'fail' };
    default:
      return { index: 0 };
  }
}

export function WorkflowStepper({ status }: { status: WorkflowStatus }) {
  const { index, terminalState } = resolveStepIndex(status);

  return (
    <nav aria-label="Alur status workflow migration" className="overflow-x-auto pb-1">
      <ol className="flex min-w-max items-center gap-1.5 text-xs">
        {STEPS.map((step, i) => {
          const isCompleted = i < index || (i === 5 && index === 5 && terminalState === 'ok');
          const isCurrent = i === index;
          const isFailed = isCurrent && terminalState === 'fail';
          const isWarn = isCurrent && terminalState === 'warn';

          const finalLabel =
            step.key === 'terminal'
              ? status === 'applied'
                ? 'Success (Applied)'
                : status === 'rolled_back'
                  ? 'Rolled Back'
                  : status === 'failed'
                    ? 'Failed'
                    : status === 'partial'
                      ? 'Partial'
                      : status === 'partially_reverted'
                        ? 'Partially Reverted'
                        : status === 'uncertain'
                          ? 'Uncertain'
                          : 'Success / Failed'
              : step.key === 'pending_review' && status === 'rejected'
                ? 'Rejected'
                : step.label;

          const pillClass = isFailed
            ? 'border-ink bg-ink text-bg font-semibold'
            : isWarn
              ? 'border-border-strong border-dashed text-ink font-semibold'
              : isCompleted
                ? 'border-border-strong text-ink'
                : isCurrent
                  ? 'border-ink text-ink font-semibold'
                  : 'border-border text-muted';

          return (
            <li key={step.key} className="flex items-center gap-1.5">
              {i > 0 && <span aria-hidden="true" className="h-px w-4 bg-border-strong" />}
              <span
                aria-current={isCurrent ? 'step' : undefined}
                className={`inline-flex items-center gap-1 rounded-sm border px-2 py-0.5 whitespace-nowrap ${pillClass}`}
              >
                {isFailed ? (
                  <X className="h-3 w-3" aria-hidden="true" />
                ) : isCompleted ? (
                  <Check className="h-3 w-3" aria-hidden="true" />
                ) : (
                  <Circle className="h-2.5 w-2.5" aria-hidden="true" />
                )}
                <span>{finalLabel}</span>
              </span>
            </li>
          );
        })}
      </ol>
    </nav>
  );
}
