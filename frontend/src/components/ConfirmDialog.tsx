import { useEffect, useId, useRef } from 'react'
import { IconTrash } from './Icons'

interface Props {
  open: boolean
  title: string
  message: string
  confirmLabel: string
  busy: boolean
  onConfirm: () => void
  onCancel: () => void
}

const FOCUSABLE = 'button:not([disabled]), [href], input, select, textarea, [tabindex]:not([tabindex="-1"])'

export default function ConfirmDialog({ open, title, message, confirmLabel, busy, onConfirm, onCancel }: Props) {
  const titleId = useId()
  const descId = useId()
  const cardRef = useRef<HTMLDivElement>(null)
  const cancelRef = useRef<HTMLButtonElement>(null)

  // Destructive dialog: land on the safe action, and give focus back on close.
  useEffect(() => {
    if (!open) return
    const previous = document.activeElement as HTMLElement | null
    cancelRef.current?.focus()
    return () => previous?.focus?.()
  }, [open])

  if (!open) return null

  // Keep Tab inside the dialog while it is open.
  const trapTab = (e: React.KeyboardEvent) => {
    if (e.key !== 'Tab') return
    const items = cardRef.current?.querySelectorAll<HTMLElement>(FOCUSABLE)
    if (!items || items.length === 0) return
    const first = items[0]
    const last = items[items.length - 1]
    if (e.shiftKey && document.activeElement === first) {
      e.preventDefault()
      last.focus()
    } else if (!e.shiftKey && document.activeElement === last) {
      e.preventDefault()
      first.focus()
    }
  }

  return (
    <div className="overlay dialog-overlay" onClick={busy ? undefined : onCancel}>
      <div
        ref={cardRef}
        className="card dialog-card"
        onClick={(e) => e.stopPropagation()}
        onKeyDown={trapTab}
        role="alertdialog"
        aria-modal="true"
        aria-labelledby={titleId}
        aria-describedby={descId}
      >
        <span className="sheet-grip" aria-hidden="true" />
        <span className="dialog-icon" aria-hidden="true">
          <IconTrash width={22} height={22} />
        </span>
        <h2 id={titleId}>{title}</h2>
        <p id={descId} className="muted">
          {message}
        </p>
        <div className="form-actions">
          <button ref={cancelRef} className="btn secondary" onClick={onCancel} disabled={busy}>
            Cancel
          </button>
          <button className="btn danger-solid" onClick={onConfirm} disabled={busy}>
            {busy ? 'Working…' : confirmLabel}
          </button>
        </div>
      </div>
    </div>
  )
}
