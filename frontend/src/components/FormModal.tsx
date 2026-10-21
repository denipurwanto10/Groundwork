import { useEffect } from 'react'
import type { ReactNode } from 'react'
import { createPortal } from 'react-dom'
import { IconX } from './Icons'

interface Props {
  title: string
  subtitle?: string
  titleId: string
  /** Icon or initial shown in the header badge. */
  icon: ReactNode
  onClose: () => void
  /** Action buttons (Cancel / Save). */
  footer: ReactNode
  children: ReactNode
}

/**
 * Shared dialog shell: centered card on desktop, bottom sheet on phones.
 * Rendered in <body> so it always sits above the dock and the map.
 */
export default function FormModal({ title, subtitle, titleId, icon, onClose, footer, children }: Props) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape' && !e.defaultPrevented) onClose()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  return createPortal(
    <div className="um-overlay" onClick={onClose}>
      <div
        className="um-modal"
        role="dialog"
        aria-modal="true"
        aria-labelledby={titleId}
        onClick={(e) => e.stopPropagation()}
      >
        <span className="um-grabber" aria-hidden="true" />
        <header className="um-modal-head">
          <span className="um-modal-avatar" aria-hidden="true">
            {icon}
          </span>
          <div className="um-modal-title">
            <h3 id={titleId}>{title}</h3>
            {subtitle && <p>{subtitle}</p>}
          </div>
          <button type="button" className="um-modal-close" onClick={onClose} aria-label="Close">
            <IconX width={18} height={18} />
          </button>
        </header>
        <div className="um-modal-body">{children}</div>
        <footer className="um-modal-foot">{footer}</footer>
      </div>
    </div>,
    document.body,
  )
}
