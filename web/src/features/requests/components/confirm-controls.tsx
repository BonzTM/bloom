import { useEffect, useRef, useState, type ReactNode } from "react";

type ConfirmControlsProps = Readonly<{
  // "Remove Default", "Confirm removing Default", "Cancel removing Default".
  action: string;
  confirming: string;
  subject: string;
  busy: boolean;
  busyLabel: string;
  danger?: boolean;
  onConfirm: () => void;
}>;

// A destructive action is two clicks so a stray click cannot do harm. Focus
// follows the confirmation and comes back to the first control on cancel.
export function ConfirmControls({
  action,
  confirming,
  subject,
  busy,
  busyLabel,
  danger = true,
  onConfirm,
}: ConfirmControlsProps): ReactNode {
  const [open, setOpen] = useState(false);
  const openRef = useRef<HTMLButtonElement>(null);
  const confirmRef = useRef<HTMLButtonElement>(null);
  const returnFocus = useRef(false);
  useEffect(() => {
    if (open) {
      confirmRef.current?.focus();
    } else if (returnFocus.current) {
      returnFocus.current = false;
      openRef.current?.focus();
    }
  }, [open]);
  if (busy) {
    return <span role="status">{busyLabel}</span>;
  }
  if (!open) {
    return (
      <button
        ref={openRef}
        type="button"
        onClick={() => {
          setOpen(true);
        }}
      >
        {action} {subject}
      </button>
    );
  }
  return (
    <span className="confirm-remove">
      <button
        ref={confirmRef}
        type="button"
        className={danger ? "btn-danger" : "btn-primary"}
        onClick={() => {
          setOpen(false);
          onConfirm();
        }}
      >
        Confirm {confirming} {subject}
      </button>
      <button
        type="button"
        className="btn-ghost"
        onClick={() => {
          returnFocus.current = true;
          setOpen(false);
        }}
      >
        Cancel {confirming} {subject}
      </button>
    </span>
  );
}
