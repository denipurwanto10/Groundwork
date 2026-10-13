import { useEffect, useMemo, useRef, useState } from 'react'
import type { Entity } from '../lib/types'
import { IconAlert, IconPin, IconPlus, IconRefresh, IconSearch } from './Icons'

type Filter = 'all' | 'active' | 'inactive'
type SortMode = 'name-asc' | 'name-desc' | 'newest' | 'oldest'

interface Props {
  entities: Entity[]
  selectedId: number | null
  loading: boolean
  error: string | null
  onSelect: (id: number | null) => void
  onAdd: () => void
  onRetry: () => void
  onVisibleEntitiesChange?: (entities: Entity[]) => void
}

const FILTERS: { key: Filter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'active', label: 'Active' },
  { key: 'inactive', label: 'Inactive' },
]

type DropdownOption = { value: string; label: string }

function FilterDropdown({ label, value, options, onChange }: { label: string; value: string; options: DropdownOption[]; onChange: (value: string) => void }) {
  const [open, setOpen] = useState(false)
  const rootRef = useRef<HTMLDivElement>(null)
  const triggerRef = useRef<HTMLButtonElement>(null)
  const selected = options.find((option) => option.value === value) ?? options[0]

  useEffect(() => {
    if (!open) return
    const onPointerDown = (event: PointerEvent) => {
      if (rootRef.current && !rootRef.current.contains(event.target as Node)) setOpen(false)
    }
    const onKeyDown = (event: KeyboardEvent) => {
      if (event.key === 'Escape') { setOpen(false); triggerRef.current?.focus() }
    }
    document.addEventListener('pointerdown', onPointerDown)
    document.addEventListener('keydown', onKeyDown)
    return () => {
      document.removeEventListener('pointerdown', onPointerDown)
      document.removeEventListener('keydown', onKeyDown)
    }
  }, [open])

  return (
    <div className={`filter-control custom-select ${open ? 'is-open' : ''}`} ref={rootRef}>
      <span>{label}</span>
      <button ref={triggerRef} type="button" className="filter-select-trigger" aria-label={label} aria-haspopup="listbox" aria-expanded={open} onClick={() => setOpen((current) => !current)}>
        <span className="filter-select-value">{selected?.label ?? ''}</span>
        <svg className="filter-select-chevron" width="14" height="14" viewBox="0 0 24 24" fill="none" aria-hidden="true"><path d="m6 9 6 6 6-6" stroke="currentColor" strokeWidth="1.8" strokeLinecap="round" strokeLinejoin="round" /></svg>
      </button>
      {open && (
        <div className="filter-select-menu" role="listbox" aria-label={label}>
          {options.map((option) => (
            <button key={option.value} type="button" role="option" aria-selected={value === option.value} className="filter-select-option" onClick={() => { onChange(option.value); setOpen(false); triggerRef.current?.focus() }}>
              <span>{option.label}</span>{value === option.value && <span className="filter-select-check" aria-hidden="true">✓</span>}
            </button>
          ))}
        </div>
      )}
    </div>
  )
}

export default function EntityList({ entities, selectedId, loading, error, onSelect, onAdd, onRetry, onVisibleEntitiesChange }: Props) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
  const [typeFilter, setTypeFilter] = useState('all')
  const [sortMode, setSortMode] = useState<SortMode>('name-asc')
  // Phones only: the list lives in a bottom sheet that can be peeked or expanded.
  const [expanded, setExpanded] = useState(false)
  const dragStart = useRef<number | null>(null)
  const scrollRef = useRef<HTMLDivElement>(null)

  const counts = useMemo(
    () => ({
      all: entities.length,
      active: entities.filter((e) => e.status === 'active').length,
      inactive: entities.filter((e) => e.status === 'inactive').length,
    }),
    [entities],
  )

  const types = useMemo(() => [...new Set(entities.map((e) => e.type.trim()).filter(Boolean))].sort((a, b) => a.localeCompare(b)), [entities])

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    const result = entities.filter((e) => {
      if (filter !== 'all' && e.status !== filter) return false
      if (typeFilter !== 'all' && e.type !== typeFilter) return false
      if (!q) return true
      return `${e.name} ${e.type} ${e.status} ${e.latitude} ${e.longitude}`.toLowerCase().includes(q)
    })
    return result.sort((a, b) => {
      if (sortMode === 'name-desc') return b.name.localeCompare(a.name)
      if (sortMode === 'newest') return new Date(b.created_at).getTime() - new Date(a.created_at).getTime()
      if (sortMode === 'oldest') return new Date(a.created_at).getTime() - new Date(b.created_at).getTime()
      return a.name.localeCompare(b.name)
    })
  }, [entities, query, filter, typeFilter, sortMode])

  useEffect(() => {
    onVisibleEntitiesChange?.(filtered)
  }, [filtered, onVisibleEntitiesChange])

  // Keep the selected row visible when the selection came from a map pin.
  useEffect(() => {
    if (selectedId == null) return
    const row = scrollRef.current?.querySelector<HTMLElement>(`[data-id="${selectedId}"]`)
    const calm = window.matchMedia('(prefers-reduced-motion: reduce)').matches
    row?.scrollIntoView({ block: 'nearest', behavior: calm ? 'auto' : 'smooth' })
  }, [selectedId])

  const select = (id: number | null) => {
    onSelect(id)
    setExpanded(false)
  }

  const clearFilters = () => {
    setQuery('')
    setFilter('all')
    setTypeFilter('all')
    setSortMode('name-asc')
  }

  const hasFilters = query.trim() !== '' || filter !== 'all' || typeFilter !== 'all'

  return (
    <section className={expanded ? 'glass list-panel expanded' : 'glass list-panel'} aria-label="Entities">
      <button
        type="button"
        className="sheet-handle"
        aria-expanded={expanded}
        aria-label={expanded ? 'Collapse entity list' : 'Expand entity list'}
        onPointerDown={(e) => {
          dragStart.current = e.clientY
        }}
        onPointerCancel={() => {
          dragStart.current = null
        }}
        onPointerUp={(e) => {
          const dy = e.clientY - (dragStart.current ?? e.clientY)
          dragStart.current = null
          if (dy < -24) setExpanded(true)
          else if (dy > 24) setExpanded(false)
          else setExpanded((v) => !v)
        }}
        onClick={(e) => {
          // Pointer taps are handled above; this only fires for keyboard activation.
          if (e.detail === 0) setExpanded((v) => !v)
        }}
      >
        <span className="grip" aria-hidden="true" />
      </button>

      <div className="panel-header">
        <h2>
          Entities
          {entities.length > 0 && <span className="count">{entities.length}</span>}
        </h2>
        <button className="btn primary" onClick={onAdd}>
          <IconPlus width={18} height={18} />
          <span>Add</span>
        </button>
      </div>

      <div className="list-body">
        {entities.length > 0 && (
          <>
            <label className="search">
              <IconSearch width={18} height={18} />
              <input
                type="search"
                value={query}
                onChange={(e) => setQuery(e.target.value)}
                onFocus={() => setExpanded(true)}
                placeholder="Search name or type"
                aria-label="Search entities"
              />
            </label>

            <div className="segmented" role="group" aria-label="Filter by status">
              {FILTERS.map((f) => (
                <button
                  key={f.key}
                  type="button"
                  className="seg"
                  aria-pressed={filter === f.key}
                  onClick={() => setFilter(f.key)}
                >
                  {f.label}
                  <span className="seg-count">{counts[f.key]}</span>
                </button>
              ))}
            </div>
            <div className="filter-row">
              <FilterDropdown
                label="Type"
                value={typeFilter}
                onChange={setTypeFilter}
                options={[{ value: 'all', label: 'All types' }, ...types.map((type) => ({ value: type, label: type }))]}
              />
              <FilterDropdown
                label="Sort"
                value={sortMode}
                onChange={(value) => setSortMode(value as SortMode)}
                options={[
                  { value: 'name-asc', label: 'Name A–Z' },
                  { value: 'name-desc', label: 'Name Z–A' },
                  { value: 'newest', label: 'Newest' },
                  { value: 'oldest', label: 'Oldest' },
                ]}
              />
            </div>
            {(hasFilters || sortMode !== 'name-asc') && (
              <div className="filter-summary" role="status" aria-live="polite">
                <span>Showing <strong>{filtered.length}</strong> of {entities.length}</span>
                <button type="button" className="text-action" onClick={clearFilters}>Reset</button>
              </div>
            )}
          </>
        )}

        {error && entities.length > 0 && (
          <div className="inline-alert" role="alert">
            <IconAlert width={18} height={18} />
            <span>{error}</span>
            <button className="btn secondary small" onClick={onRetry}>
              Retry
            </button>
          </div>
        )}

        <div className="list-scroll" ref={scrollRef}>
          {loading && entities.length === 0 ? (
            <ul className="entity-list" aria-busy="true" aria-label="Loading entities">
              {[0, 1, 2, 3].map((i) => (
                <li key={i} className="skeleton-row">
                  <span className="skeleton-avatar" />
                  <span className="skeleton-copy"><i /><i /></span>
                  <span className="skeleton-badge" />
                </li>
              ))}
            </ul>
          ) : error && entities.length === 0 ? (
            <div className="empty" role="alert">
              <span className="empty-icon danger">
                <IconAlert width={22} height={22} />
              </span>
              <p className="empty-title">Can’t load entities</p>
              <p className="muted">{error}</p>
              <button className="btn secondary" onClick={onRetry}>
                <IconRefresh width={16} height={16} />
                <span>Try again</span>
              </button>
            </div>
          ) : entities.length === 0 ? (
            <div className="empty">
              <span className="empty-icon">
                <IconPin width={22} height={22} />
              </span>
              <p className="empty-title">No entities yet</p>
              <p className="muted">Add your first one and place it on the map.</p>
              <button className="btn primary" onClick={onAdd}>
                <IconPlus width={18} height={18} />
                <span>Add entity</span>
              </button>
            </div>
          ) : filtered.length === 0 ? (
            <div className="empty">
              <span className="empty-icon">
                <IconSearch width={22} height={22} />
              </span>
              <p className="empty-title">No matching entities</p>
              <p className="muted">Try another name, type, or status filter.</p>
              <button className="btn secondary" onClick={clearFilters}>
                Clear filters
              </button>
            </div>
          ) : (
            <ul className="entity-list">
              {filtered.map((e) => (
                <li key={e.id}>
                  <button
                    className={e.id === selectedId ? 'entity-item selected' : 'entity-item'}
                    data-id={e.id}
                    onClick={() => select(e.id === selectedId ? null : e.id)}
                    aria-pressed={e.id === selectedId}
                  >
                    <span className={`entity-avatar ${e.status}`} aria-hidden="true">
                      <IconPin width={20} height={20} />
                    </span>
                    <span className="entity-body">
                      <span className="entity-name">{e.name}</span>
                      <span className="entity-meta">
                        <span className="entity-type">{e.type}</span>
                        <span className="entity-coords">
                          {e.latitude.toFixed(4)}, {e.longitude.toFixed(4)}
                        </span>
                      </span>
                    </span>
                    <span className={`badge ${e.status}`}>{e.status}</span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </section>
  )
}
