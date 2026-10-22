import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { createPortal } from 'react-dom'
import { IconChevronRight } from './Icons'

interface Props {
  /** Selected day, YYYY-MM-DD. */
  value: string
  /** Latest day that can be picked, YYYY-MM-DD. */
  max?: string
  onChange(value: string): void
  ariaLabel?: string
}

const MONTHS = [
  'January', 'February', 'March', 'April', 'May', 'June',
  'July', 'August', 'September', 'October', 'November', 'December',
]
const WEEKDAYS = ['Su', 'Mo', 'Tu', 'We', 'Th', 'Fr', 'Sa']

const pad = (n: number) => String(n).padStart(2, '0')
const toISO = (y: number, m: number, d: number) => `${y}-${pad(m + 1)}-${pad(d)}`

function parse(iso: string): { y: number; m: number; d: number } | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(iso)
  if (!match) return null
  return { y: +match[1], m: +match[2] - 1, d: +match[3] }
}

function todayISO(): string {
  const n = new Date()
  return toISO(n.getFullYear(), n.getMonth(), n.getDate())
}

function IconCal() {
  return (
    <svg width="15" height="15" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">
      <rect x="3.5" y="5" width="17" height="15.5" rx="3" />
      <path d="M8 3v4M16 3v4M3.5 10h17" />
    </svg>
  )
}

/**
 * A small calendar that opens right under the field. The native date popup
 * is drawn by the browser and can't be styled, so it never matched the app.
 */
export default function DatePicker({ value, max, onChange, ariaLabel }: Props) {
  const rootRef = useRef<HTMLDivElement>(null)
  const popRef = useRef<HTMLDivElement>(null)
  const [open, setOpen] = useState(false)
  // The calendar is drawn in <body> with fixed coordinates, so no scrolling
  // or clipping parent (modal body, toolbar) can cut it off.
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  const selected = parse(value)
  const limit = max ?? todayISO()

  const now = new Date()
  const [view, setView] = useState({ y: selected?.y ?? now.getFullYear(), m: selected?.m ?? now.getMonth() })

  // Show the month of the selected day each time the calendar opens.
  useEffect(() => {
    if (open && selected) setView({ y: selected.y, m: selected.m })
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open])

  useLayoutEffect(() => {
    if (!open) {
      setPos(null)
      return
    }
    const place = () => {
      const field = rootRef.current?.getBoundingClientRect()
      if (!field) return
      const pop = popRef.current
      const w = pop?.offsetWidth ?? 264
      const h = pop?.offsetHeight ?? 320
      const gap = 8
      const left = Math.max(8, Math.min(field.left, window.innerWidth - w - 8))
      const below = field.bottom + gap
      const fitsBelow = below + h <= window.innerHeight - 8
      const top = fitsBelow ? below : Math.max(8, field.top - gap - h)
      setPos((p) => (p && p.left === left && p.top === top ? p : { left, top }))
    }
    place()
    window.addEventListener('resize', place)
    window.addEventListener('scroll', place, true)
    return () => {
      window.removeEventListener('resize', place)
      window.removeEventListener('scroll', place, true)
    }
  }, [open, view])

  useEffect(() => {
    if (!open) return
    const onDown = (e: MouseEvent | TouchEvent) => {
      const t = e.target as Node
      if (!rootRef.current?.contains(t) && !popRef.current?.contains(t)) setOpen(false)
    }
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        // Close only the calendar, not the playback dialog behind it.
        e.preventDefault()
        e.stopPropagation()
        setOpen(false)
      }
    }
    document.addEventListener('mousedown', onDown)
    document.addEventListener('touchstart', onDown)
    window.addEventListener('keydown', onKey, true)
    return () => {
      document.removeEventListener('mousedown', onDown)
      document.removeEventListener('touchstart', onDown)
      window.removeEventListener('keydown', onKey, true)
    }
  }, [open])

  const cells = useMemo(() => {
    const first = new Date(view.y, view.m, 1).getDay()
    const days = new Date(view.y, view.m + 1, 0).getDate()
    const out: (number | null)[] = Array(first).fill(null)
    for (let d = 1; d <= days; d++) out.push(d)
    while (out.length % 7 !== 0) out.push(null)
    return out
  }, [view])

  const maxP = parse(limit)
  const canNext = !maxP || view.y < maxP.y || (view.y === maxP.y && view.m < maxP.m)

  const shift = (delta: number) => {
    const d = new Date(view.y, view.m + delta, 1)
    setView({ y: d.getFullYear(), m: d.getMonth() })
  }

  const label = selected ? `${pad(selected.d)}/${pad(selected.m + 1)}/${selected.y}` : 'Pick a day'
  const today = todayISO()

  return (
    <div className="dp" ref={rootRef}>
      <button
        type="button"
        className="dp-field"
        onClick={() => setOpen((o) => !o)}
        aria-haspopup="dialog"
        aria-expanded={open}
        aria-label={ariaLabel}
      >
        <span>{label}</span>
        <IconCal />
      </button>

      {open && createPortal(
        <div
          ref={popRef}
          className="dp-pop"
          role="dialog"
          aria-label="Choose a day"
          style={{ left: pos?.left ?? 0, top: pos?.top ?? 0, visibility: pos ? 'visible' : 'hidden' }}
        >
          <div className="dp-nav">
            <button type="button" className="dp-arrow" onClick={() => shift(-1)} aria-label="Previous month">
              <span className="dp-flip"><IconChevronRight width={16} height={16} /></span>
            </button>
            <span className="dp-month">{MONTHS[view.m]} {view.y}</span>
            <button type="button" className="dp-arrow" onClick={() => shift(1)} disabled={!canNext} aria-label="Next month">
              <IconChevronRight width={16} height={16} />
            </button>
          </div>

          <div className="dp-grid dp-week" aria-hidden="true">
            {WEEKDAYS.map((w) => <span key={w}>{w}</span>)}
          </div>

          <div className="dp-grid">
            {cells.map((d, i) => {
              if (d == null) return <span key={i} />
              const iso = toISO(view.y, view.m, d)
              const disabled = iso > limit
              return (
                <button
                  key={i}
                  type="button"
                  className={`dp-day${iso === value ? ' sel' : ''}${iso === today ? ' today' : ''}`}
                  disabled={disabled}
                  onClick={() => {
                    onChange(iso)
                    setOpen(false)
                  }}
                >
                  {d}
                </button>
              )
            })}
          </div>

          <div className="dp-foot">
            <button
              type="button"
              className="dp-link"
              onClick={() => {
                onChange(today <= limit ? today : limit)
                setOpen(false)
              }}
            >
              Today
            </button>
          </div>
        </div>,
        document.body,
      )}
    </div>
  )
}
