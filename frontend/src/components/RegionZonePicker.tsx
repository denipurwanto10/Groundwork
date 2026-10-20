import { useCallback, useEffect, useMemo, useRef, useState } from 'react'
import { api } from '../lib/api'
import type { IslandGroup, RegionFeature, ZoneFeature } from '../lib/types'
import { IconChevronUp, IconMapPin, IconPolygon, IconSearch } from './Icons'

/**
 * "Take the polygon from a region" instead of drawing it by hand.
 *
 * Official boundaries already exist in admin_regions (province, regency,
 * district), so the operator picks one — an island group or a single
 * kecamatan — and the backend copies that polygon into zones. admin_regions
 * itself is never written to, which is why these zones can be deleted and
 * recreated freely.
 */

type Mode = 'island' | 1 | 2 | 3

const MODES: { key: Mode; label: string }[] = [
  { key: 'island', label: 'Pulau' },
  { key: 1, label: 'Provinsi' },
  { key: 2, label: 'Kab/Kota' },
  { key: 3, label: 'Kecamatan' },
]

const CATEGORIES = ['operational', 'restricted', 'planned', 'other'] as const

const LEVEL_LABEL: Record<number, string> = { 1: 'Provinsi', 2: 'Kab/Kota', 3: 'Kecamatan' }

/** BPS stores names in upper case; show them the way people write them. */
function prettyName(raw: string): string {
  const lower = raw.toLocaleLowerCase('id-ID')
  return lower.replace(/(^|[\s/(-])([a-z])/g, (_, sep: string, ch: string) => sep + ch.toLocaleUpperCase('id-ID'))
}

interface Props {
  /** Called after a zone was created, so the shell can refresh and select it. */
  onCreated: (zone: ZoneFeature) => void
}

export default function RegionZonePicker({ onCreated }: Props) {
  const [open, setOpen] = useState(false)
  const [mode, setMode] = useState<Mode>('island')

  const [islands, setIslands] = useState<IslandGroup[]>([])
  const [islandsError, setIslandsError] = useState<string | null>(null)
  const [islandsLoading, setIslandsLoading] = useState(false)

  const [query, setQuery] = useState('')
  const [results, setResults] = useState<RegionFeature[]>([])
  const [resultsLoading, setResultsLoading] = useState(false)
  const [resultsError, setResultsError] = useState<string | null>(null)

  const [picked, setPicked] = useState<{ kind: 'island'; island: IslandGroup } | { kind: 'region'; region: RegionFeature } | null>(null)
  const [zoneName, setZoneName] = useState('')
  const [category, setCategory] = useState<string>('other')
  const [color, setColor] = useState('#2563eb')
  const [saving, setSaving] = useState(false)
  const [saveError, setSaveError] = useState<string | null>(null)
  const [savedNote, setSavedNote] = useState<string | null>(null)

  const searchSeq = useRef(0)

  const loadIslands = useCallback(async () => {
    setIslandsLoading(true)
    setIslandsError(null)
    try {
      const data = await api.listIslands()
      setIslands(data.islands)
    } catch (err) {
      setIslandsError(err instanceof Error ? err.message : 'Could not load the island list')
    } finally {
      setIslandsLoading(false)
    }
  }, [])

  // Islands are needed once, the first time the section is opened.
  useEffect(() => {
    if (!open || islands.length > 0 || islandsLoading) return
    void loadIslands()
  }, [open, islands.length, islandsLoading, loadIslands])

  // A province list is short enough to show whole; districts are only ever
  // searched, never dumped into the browser.
  useEffect(() => {
    if (!open || mode === 'island') return
    if (mode !== 1 && query.trim().length < 2) {
      setResults([])
      setResultsError(null)
      return
    }

    const seq = ++searchSeq.current
    setResultsLoading(true)
    setResultsError(null)
    const timer = window.setTimeout(async () => {
      try {
        const data = await api.listRegions({
          level: mode,
          q: mode === 1 ? undefined : query.trim(),
          geom: false,
          limit: mode === 1 ? 60 : 40,
        })
        if (searchSeq.current !== seq) return
        setResults(data.features)
      } catch (err) {
        if (searchSeq.current !== seq) return
        setResults([])
        setResultsError(err instanceof Error ? err.message : 'Could not search the region list')
      } finally {
        if (searchSeq.current === seq) setResultsLoading(false)
      }
    }, mode === 1 ? 0 : 320)

    return () => {
      window.clearTimeout(timer)
    }
  }, [open, mode, query])

  const pickIsland = (island: IslandGroup) => {
    setPicked({ kind: 'island', island })
    setZoneName(island.label)
    setSaveError(null)
    setSavedNote(null)
  }

  const pickRegion = (region: RegionFeature) => {
    setPicked({ kind: 'region', region })
    setZoneName(prettyName(region.properties.name))
    setSaveError(null)
    setSavedNote(null)
  }

  const create = async () => {
    if (!picked) return
    setSaving(true)
    setSaveError(null)
    setSavedNote(null)
    try {
      const input = {
        name: zoneName.trim(),
        category,
        color,
      }
      const zone =
        picked.kind === 'island'
          ? await api.zoneFromIsland({ island: picked.island.key, ...input })
          : await api.zoneFromRegion({ region_code: picked.region.properties.code, ...input })
      setSavedNote(`"${zone.properties.name}" created from ${picked.kind === 'island' ? picked.island.label : prettyName(picked.region.properties.name)}.`)
      setPicked(null)
      setZoneName('')
      onCreated(zone)
    } catch (err) {
      setSaveError(err instanceof Error ? err.message : 'Could not create the zone')
    } finally {
      setSaving(false)
    }
  }

  const pickedLabel = useMemo(() => {
    if (!picked) return null
    if (picked.kind === 'island') return `${picked.island.label} · ${picked.island.provinces} provinsi`
    const p = picked.region.properties
    return `${prettyName(p.name)} · ${LEVEL_LABEL[p.level] ?? `level ${p.level}`} · ${p.code}`
  }, [picked])

  return (
    <section className="zone-panel region-picker" aria-label="Create a zone from a boundary">
      <header className="zone-panel-header">
        <h2>
          <IconPolygon width={17} height={17} />
          From boundary
        </h2>
        <div className="zone-actions">
          <button
            type="button"
            className="icon-btn"
            aria-expanded={open}
            title={open ? 'Hide the boundary picker' : 'Create a zone from an island or a district'}
            aria-label={open ? 'Hide the boundary picker' : 'Create a zone from an island or a district'}
            onClick={() => setOpen((v) => !v)}
          >
            <IconChevronUp width={16} height={16} style={open ? undefined : { transform: 'rotate(180deg)' }} />
          </button>
        </div>
      </header>

      {open && (
        <div className="region-picker-body">
          <div className="seg-row" role="tablist" aria-label="Boundary source">
            {MODES.map((m) => (
              <button
                key={String(m.key)}
                type="button"
                role="tab"
                aria-selected={mode === m.key}
                className={mode === m.key ? 'seg active' : 'seg'}
                onClick={() => {
                  setMode(m.key)
                  setPicked(null)
                  setResults([])
                  setQuery('')
                  setSaveError(null)
                }}
              >
                {m.label}
              </button>
            ))}
          </div>

          {mode === 'island' ? (
            <>
              {islandsError && (
                <div className="zone-empty" role="alert">
                  <span>{islandsError}</span>
                  <button type="button" className="btn secondary small" onClick={() => void loadIslands()}>
                    Retry
                  </button>
                </div>
              )}
              {!islandsError && islandsLoading && <div className="zone-empty">Loading islands…</div>}
              {!islandsError && !islandsLoading && (
                <div className="island-grid">
                  {islands.map((island) => (
                    <button
                      key={island.key}
                      type="button"
                      className={picked?.kind === 'island' && picked.island.key === island.key ? 'island-chip active' : 'island-chip'}
                      onClick={() => pickIsland(island)}
                      title={`${island.provinces} province boundaries will be merged`}
                    >
                      <span className="island-name">{island.label}</span>
                      <span className="island-count">{island.provinces} prov</span>
                    </button>
                  ))}
                </div>
              )}
            </>
          ) : (
            <>
              <label className="search-field">
                <IconSearch width={15} height={15} />
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder={mode === 1 ? 'Filter provinces…' : `Search ${LEVEL_LABEL[mode].toLowerCase()} name…`}
                  aria-label={`Search ${LEVEL_LABEL[mode]}`}
                />
              </label>

              {resultsError && (
                <div className="zone-empty" role="alert">
                  {resultsError}
                </div>
              )}
              {!resultsError && resultsLoading && <div className="zone-empty">Searching…</div>}
              {!resultsError && !resultsLoading && mode !== 1 && query.trim().length < 2 && (
                <p className="route-hint">Type at least two letters — 7.069 kecamatan live in the database.</p>
              )}
              {!resultsError && !resultsLoading && results.length === 0 && (mode === 1 || query.trim().length >= 2) && (
                <div className="zone-empty">No boundary matches that name.</div>
              )}

              {results.length > 0 && (
                <div className="zone-list region-results">
                  {results.map((r) => {
                    const active = picked?.kind === 'region' && picked.region.properties.code === r.properties.code
                    return (
                      <button
                        key={r.properties.code}
                        type="button"
                        className={active ? 'zone-list-item selected' : 'zone-list-item'}
                        onClick={() => pickRegion(r)}
                      >
                        <span className="zone-body">
                          <span className="zone-name">{prettyName(r.properties.name)}</span>
                          <span className="zone-meta">
                            <span className="zone-category">{LEVEL_LABEL[r.properties.level] ?? r.properties.level}</span>
                            <span>{r.properties.code}</span>
                          </span>
                        </span>
                      </button>
                    )
                  })}
                </div>
              )}
            </>
          )}

          {picked && (
            <div className="region-picker-form">
              <div className="region-picked">
                <IconMapPin width={15} height={15} />
                <span>{pickedLabel}</span>
              </div>
              <label className="field">
                <span>Zone name</span>
                <input value={zoneName} onChange={(e) => setZoneName(e.target.value)} maxLength={120} />
              </label>
              <div className="region-picker-options">
                <label className="field">
                  <span>Category</span>
                  <select value={category} onChange={(e) => setCategory(e.target.value)}>
                    {CATEGORIES.map((c) => (
                      <option key={c} value={c}>
                        {c}
                      </option>
                    ))}
                  </select>
                </label>
                <label className="field color-field">
                  <span>Colour</span>
                  <input type="color" value={color} onChange={(e) => setColor(e.target.value)} />
                </label>
              </div>
              {saveError && (
                <div className="route-error" role="alert">
                  {saveError}
                </div>
              )}
              <div className="route-foot">
                <button
                  type="button"
                  className="btn primary small"
                  disabled={saving || zoneName.trim().length === 0}
                  onClick={() => void create()}
                >
                  {saving ? 'Copying boundary…' : 'Create zone'}
                </button>
                <button type="button" className="btn secondary small" onClick={() => setPicked(null)} disabled={saving}>
                  Cancel
                </button>
              </div>
            </div>
          )}

          {savedNote && (
            <p className="region-note" role="status">
              {savedNote}
            </p>
          )}
        </div>
      )}
    </section>
  )
}
