import { useEffect, useId, useRef } from 'react';
import { Button } from '@/components/ui/Button';

interface ConfirmDialogProps {
  open: boolean;
  title: string;
  description: string;
  confirmLabel?: string;
  cancelLabel?: string;
  destructive?: boolean;
  loading?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/**
 * Accessible native modal <dialog> using showModal().
 * Omits redundant role="dialog"/aria-modal/manual Esc trap per native modal dialog spec.
 */
export function ConfirmDialog({
  open,
  title,
  description,
  confirmLabel = 'Konfirmasi',
  cancelLabel = 'Batal',
  destructive = false,
  loading = false,
  onConfirm,
  onCancel,
}: ConfirmDialogProps) {
  const dialogRef = useRef<HTMLDialogElement>(null);
  const titleId = useId();
  const descId = useId();

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;

    if (open && !dialog.open) {
      dialog.showModal();
    } else if (!open && dialog.open) {
      dialog.close();
    }
  }, [open]);

  useEffect(() => {
    const dialog = dialogRef.current;
    if (!dialog) return;

    const handleClose = () => {
      const value = dialog.returnValue;
      dialog.returnValue = '';
      if (value !== 'confirm') {
        onCancel();
      }
    };

    dialog.addEventListener('close', handleClose);

    // Progressive enhancement fallback for click-outside light dismiss where closedby="any" is unsupported
    const supportsClosedBy = 'closedBy' in HTMLDialogElement.prototype;
    let pressedOutside = false;

    const isOutside = (e: PointerEvent) => {
      const r = dialog.getBoundingClientRect();
      return e.clientX < r.left || e.clientX > r.right || e.clientY < r.top || e.clientY > r.bottom;
    };

    const handlePointerDown = (e: PointerEvent) => {
      pressedOutside = isOutside(e);
    };
    const handlePointerUp = (e: PointerEvent) => {
      if (!loading && pressedOutside && isOutside(e)) {
        dialog.close('cancel');
      }
    };

    if (!supportsClosedBy) {
      dialog.addEventListener('pointerdown', handlePointerDown);
      dialog.addEventListener('pointerup', handlePointerUp);
    }

    return () => {
      dialog.removeEventListener('close', handleClose);
      if (!supportsClosedBy) {
        dialog.removeEventListener('pointerdown', handlePointerDown);
        dialog.removeEventListener('pointerup', handlePointerUp);
      }
    };
  }, [loading, onCancel]);

  return (
    <dialog
      ref={dialogRef}
      aria-labelledby={titleId}
      aria-describedby={descId}
      className="ConfirmDialog"
      {...({ closedby: loading ? 'none' : 'any' } as Record<string, string>)}
    >
      <div className="p-5">
        <h1 id={titleId} className="text-base font-semibold text-ink">
          {title}
        </h1>
        <p id={descId} className="mt-2 text-sm leading-relaxed text-muted">
          {description}
        </p>
        <div className="mt-5 flex flex-wrap justify-end gap-2">
          <Button
            variant="secondary"
            autoFocus={destructive}
            disabled={loading}
            onClick={() => dialogRef.current?.close('cancel')}
          >
            {cancelLabel}
          </Button>
          <Button
            variant={destructive ? 'danger' : 'primary'}
            autoFocus={!destructive}
            loading={loading}
            onClick={onConfirm}
          >
            {confirmLabel}
          </Button>
        </div>
      </div>
    </dialog>
  );
}
