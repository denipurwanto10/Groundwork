import { useEffect, useMemo, useRef, useState } from 'react'
import type { Entity } from '../lib/types'
import { IconAlert, IconPin, IconPlus, IconRefresh, IconSearch } from './Icons'

type Filter = 'all' | 'active' | 'inactive'

interface Props {
  entities: Entity[]
  selectedId: number | null
  loading: boolean
  error: string | null
  onSelect: (id: number | null) => void
  onAdd: () => void
  onRetry: () => void
}

const FILTERS: { key: Filter; label: string }[] = [
  { key: 'all', label: 'All' },
  { key: 'active', label: 'Active' },
  { key: 'inactive', label: 'Inactive' },
]

export default function EntityList({ entities, selectedId, loading, error, onSelect, onAdd, onRetry }: Props) {
  const [query, setQuery] = useState('')
  const [filter, setFilter] = useState<Filter>('all')
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

  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase()
    return entities.filter((e) => {
      if (filter !== 'all' && e.status !== filter) return false
      if (!q) return true
      return `${e.name} ${e.type} ${e.status}`.toLowerCase().includes(q)
    })
  }, [entities, query, filter])

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
  }

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
                <li key={i} className="skeleton" />
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
              <p className="empty-title">No matches</p>
              <p className="muted">Try a different search or status.</p>
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
