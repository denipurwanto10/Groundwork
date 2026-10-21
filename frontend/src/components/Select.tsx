import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import type { CSSProperties, KeyboardEvent } from 'react'
import { createPortal } from 'react-dom'

export interface SelectOption {
  value: string
  label: string
}

interface Props {
  value: string
  options: SelectOption[]
  onChange: (value: string) => void
  id?: string
  /** Shown in the bottom sheet header and used as the accessible name. */
  label?: string
  placeholder?: string
  disabled?: boolean
  invalid?: boolean
  describedBy?: string
  /** Search box inside the menu. Defaults to on for long lists. */
  searchable?: boolean
  className?: string
}

const SHEET_QUERY = '(max-width: 640px)'
const MENU_MAX = 300
const GAP = 6
const EDGE = 12

interface Place {
  left: number
  width: number
  top?: number
  bottom?: number
  maxHeight: number
}

/**
 * Dropdown that replaces the browser's own <select> popup: it never clips or
 * overflows, matches the rest of the UI, and turns into a bottom sheet on phones.
 */
export default function Select({
  value,
  options,
  onChange,
  id,
  label,
  placeholder = 'Select…',
  disabled,
  invalid,
  describedBy,
  searchable,
  className = '',
}: Props) {
  const [open, setOpen] = useState(false)
  const [sheet, setSheet] = useState(false)
  const [query, setQuery] = useState('')
  const [active, setActive] = useState(0)
  const [place, setPlace] = useState<Place | null>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const menuRef = useRef<HTMLDivElement>(null)
  const searchRef = useRef<HTMLInputElement>(null)

  const selected = options.find((o) => o.value === value)
  const showSearch = searchable ?? options.length > 8

  const visible = useMemo(() => {
    const needle = query.trim().toLowerCase()
    return needle ? options.filter((o) => o.label.toLowerCase().includes(needle)) : options
  }, [options, query])

  function openMenu() {
    if (disabled) return
    const idx = options.findIndex((o) => o.value === value)
    setQuery('')
    setActive(Math.max(0, idx))
    setSheet(window.matchMedia(SHEET_QUERY).matches)
    setOpen(true)
  }

  function close(refocus = true) {
    setOpen(false)
    if (refocus) triggerRef.current?.focus()
  }

  function choose(next: string) {
    onChange(next)
    close()
  }

  // Popover placement: below the trigger, flipped above when there is no room.
  useLayoutEffect(() => {
    if (!open || sheet) return
    const update = () => {
      const el = triggerRef.current
      if (!el) return
      const r = el.getBoundingClientRect()
      const below = window.innerHeight - r.bottom - EDGE - GAP
      const above = r.top - EDGE - GAP
      const want = Math.min(MENU_MAX, options.length * 40 + (showSearch ? 64 : 16))
      const flip = below < Math.min(want, 180) && above > below
      const room = flip ? above : below
      setPlace({
        left: r.left,
        width: r.width,
        ...(flip ? { bottom: window.innerHeight - r.top + GAP } : { top: r.bottom + GAP }),
        maxHeight: Math.max(120, Math.min(MENU_MAX, room)),
      })
    }
    update()
    window.addEventListener('resize', update)
    window.addEventListener('scroll', update, true)
    return () => {
      window.removeEventListener('resize', update)
      window.removeEventListener('scroll', update, true)
    }
  }, [open, sheet, options.length, showSearch])

  // Move focus into the menu when it opens.
  useEffect(() => {
    if (!open) return
    const t = window.setTimeout(() => (showSearch ? searchRef.current : menuRef.current)?.focus({ preventScroll: true }), 0)
    return () => window.clearTimeout(t)
  }, [open, showSearch, sheet])

  // Keep the highlighted row in view.
  useEffect(() => {
    if (!open) return
    menuRef.current?.querySelector<HTMLElement>(`[data-i="${active}"]`)?.scrollIntoView({ block: 'nearest' })
  }, [active, open, visible.length])

  function onMenuKey(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.nativeEvent.stopPropagation()
      close()
    } else if (e.key === 'ArrowDown') {
      e.preventDefault()
      setActive((i) => Math.min(visible.length - 1, i + 1))
    } else if (e.key === 'ArrowUp') {
      e.preventDefault()
      setActive((i) => Math.max(0, i - 1))
    } else if (e.key === 'Home') {
      e.preventDefault()
      setActive(0)
    } else if (e.key === 'End') {
      e.preventDefault()
      setActive(Math.max(0, visible.length - 1))
    } else if (e.key === 'Enter') {
      e.preventDefault()
      const o = visible[active]
      if (o) choose(o.value)
    } else if (e.key === 'Tab') {
      close(false)
    }
  }

  const menuStyle: CSSProperties | undefined =
    !sheet && place
      ? { left: place.left, width: place.width, top: place.top, bottom: place.bottom, maxHeight: place.maxHeight }
      : undefined

  return (
    <>
      <button
        ref={triggerRef}
        id={id}
        type="button"
        className={`sel-trigger${open ? ' open' : ''}${className ? ` ${className}` : ''}`}
        aria-haspopup="listbox"
        aria-expanded={open}
        aria-label={label && !id ? label : undefined}
        aria-invalid={invalid ? true : undefined}
        aria-describedby={describedBy}
        disabled={disabled}
        onClick={() => (open ? close(false) : openMenu())}
        onKeyDown={(e) => {
          if (e.key === 'ArrowDown' || e.key === 'ArrowUp') {
            e.preventDefault()
            openMenu()
          }
        }}
      >
        <span className={`sel-value${selected ? '' : ' placeholder'}`}>{selected?.label ?? placeholder}</span>
        <svg className="sel-chevron" width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
          <path d="m6 9 6 6 6-6" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" />
        </svg>
      </button>

      {open &&
        createPortal(
          <div className={`sel-layer${sheet ? ' sheet' : ''}`} onPointerDown={() => close(false)}>
            <div
              ref={menuRef}
              className="sel-menu"
              role="listbox"
              aria-label={label}
              tabIndex={-1}
              style={menuStyle}
              onPointerDown={(e) => e.stopPropagation()}
              onKeyDown={onMenuKey}
            >
              {sheet && (
                <div className="sel-sheet-head">
                  <span className="sel-grabber" aria-hidden="true" />
                  {label && <b>{label}</b>}
                </div>
              )}

              {showSearch && (
                <div className="sel-search">
                  <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                    <circle cx="11" cy="11" r="7" stroke="currentColor" strokeWidth="2" />
                    <path d="m20 20-3.5-3.5" stroke="currentColor" strokeWidth="2" strokeLinecap="round" />
                  </svg>
                  <input
                    ref={searchRef}
                    type="search"
                    value={query}
                    placeholder="Search…"
                    aria-label="Search options"
                    autoComplete="off"
                    onChange={(e) => {
                      setQuery(e.target.value)
                      setActive(0)
                    }}
                  />
                </div>
              )}

              <div className="sel-list">
                {visible.length === 0 && <div className="sel-empty">No results</div>}
                {visible.map((o, i) => {
                  const on = o.value === value
                  return (
                    <button
                      key={o.value || `empty-${i}`}
                      type="button"
                      role="option"
                      data-i={i}
                      aria-selected={on}
                      className={`sel-option${on ? ' on' : ''}${i === active ? ' active' : ''}`}
                      onPointerEnter={() => setActive(i)}
                      onClick={() => choose(o.value)}
                    >
                      <span>{o.label}</span>
                      {on && (
                        <svg width="16" height="16" viewBox="0 0 24 24" fill="none" aria-hidden="true">
                          <path d="m5 12.5 4.5 4.5L19 7.5" stroke="currentColor" strokeWidth="2.2" strokeLinecap="round" strokeLinejoin="round" />
                        </svg>
                      )}
                    </button>
                  )
                })}
              </div>
            </div>
          </div>,
          document.body,
        )}
    </>
  )
}
